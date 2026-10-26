// Package hyper3d implements the Rodin asynchronous wire protocol. Public Task
// state remains owned by generation; Rodin's private IDs live in Attempt data.
package hyper3d

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
)

type Config struct {
	BaseURL         string
	APIKey          string
	AuthHeader      string
	AuthPrefix      string
	RequestTimeout  time.Duration
	ErrorMapping    *generation.ErrorMapping
	StatusMapping   *generation.StatusMapping
	HTTPClient      *http.Client
	AllowHTTPAssets bool
	InputAssets     generation.InputAssetResolver
	// ScratchDir holds private upload copies (spooled multipart bodies).
	// Empty means os.TempDir(); production passes the storage volume's tmp
	// directory because the container's /tmp is a small tmpfs (#321 A).
	ScratchDir string
}

type Adapter struct {
	client          *httpjson.Client
	inputs          generation.InputAssetResolver
	errorMapping    *generation.ErrorMapping
	statusMapping   *generation.StatusMapping
	allowHTTPAssets bool
	scratchDir      string
}

type wireOptions struct {
	Endpoint         string          `json:"endpoint"`
	ObserveEndpoint  string          `json:"observe_endpoint"`
	DownloadEndpoint string          `json:"download_endpoint"`
	RequestTemplate  json.RawMessage `json:"request_template"`
	DownloadFiles    json.RawMessage `json:"download_files,omitempty"`
}

// downloadFile selects one file from the download list by exact basename or
// basename suffix. Role and media type still follow the file extension, so a
// declaration can only narrow which returned files become claims.
type downloadFile struct {
	Name       string `json:"name,omitempty"`
	NameSuffix string `json:"name_suffix,omitempty"`
}

func (f downloadFile) matches(name string) bool {
	if f.Name != "" {
		return name == f.Name
	}
	return strings.HasSuffix(name, f.NameSuffix)
}

// parseDownloadFiles returns nil when the Offering declares no selection,
// which keeps every recognized file as a candidate claim.
func parseDownloadFiles(raw json.RawMessage, offering generation.Offering) ([]downloadFile, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var files []downloadFile
	if err := decoder.Decode(&files); err != nil || len(files) == 0 {
		return nil, fmt.Errorf("Rodin download_files 必须是非空的文件选择列表")
	}
	requiredModel := false
	for _, file := range files {
		selector := file.Name
		if (file.Name == "") == (file.NameSuffix == "") {
			return nil, fmt.Errorf("Rodin download_files 每项必须恰好声明 name 或 name_suffix")
		}
		if selector == "" {
			selector = file.NameSuffix
		}
		if strings.ContainsAny(selector, `/\`) || strings.TrimSpace(selector) != selector {
			return nil, fmt.Errorf("Rodin download_files 只能匹配文件名")
		}
		role, _, variant := fileKind(selector)
		if role == "" {
			return nil, fmt.Errorf("Rodin download_files 包含不识别的文件类型")
		}
		if role == "model" {
			if !declaredRequiredModel(offering, variant) {
				return nil, fmt.Errorf("Rodin download_files 选择了未声明为必需产物的模型格式")
			}
			requiredModel = true
		}
	}
	if !requiredModel {
		return nil, fmt.Errorf("Rodin download_files 必须选择必需模型")
	}
	return files, nil
}

const (
	bangEndpoint    = "bang"
	textureEndpoint = "rodin_texture_only"
)

// checkpoint persists both Rodin identifiers: the top-level UUID is used for
// downloads, while jobs.subscription_key is used only for status polling.
type checkpoint struct {
	TaskUUID        string `json:"task_uuid"`
	SubscriptionKey string `json:"subscription_key"`
	PollCount       int    `json:"poll_count,omitempty"`
	// UnmatchedLists counts completed-job download lists that contained files
	// but none selected as the required model by download_files.
	UnmatchedLists int `json:"unmatched_lists,omitempty"`
	// ConsumedCredit is the submission response's numeric `consumed` kept
	// verbatim as private billing evidence. Usage rows reference the same
	// top-level uuid; neither replaces the frozen quote or becomes public.
	ConsumedCredit json.RawMessage `json:"consumed_credit,omitempty"`
}

// maxUnmatchedLists bounds polling when a declared pack file never appears
// (for example after an upstream rename): about five minutes at the capped
// poll delay instead of the full job timeout.
const maxUnmatchedLists = 10

type rodinError struct {
	Error json.RawMessage `json:"error"`
}

type submitResponse struct {
	rodinError
	UUID string `json:"uuid"`
	Jobs struct {
		SubscriptionKey string `json:"subscription_key"`
	} `json:"jobs"`
	Consumed json.RawMessage `json:"consumed"`
}

// numericEvidence returns a JSON number verbatim, or nil for an absent,
// null, quoted or structured value; a malformed receipt is never read as 0.
func numericEvidence(raw json.RawMessage) json.RawMessage {
	value := bytes.TrimSpace(raw)
	if len(value) == 0 || (value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
		return nil
	}
	var number float64
	if json.Unmarshal(value, &number) != nil {
		return nil
	}
	return append(json.RawMessage(nil), value...)
}

type statusResponse struct {
	rodinError
	Jobs []struct {
		Status string `json:"status"`
	} `json:"jobs"`
}

type downloadResponse struct {
	rodinError
	List []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"list"`
}

func New(config Config) (*Adapter, error) {
	if config.StatusMapping == nil {
		return nil, fmt.Errorf("hyper3d: 缺少声明式状态词表")
	}
	for _, status := range []string{"waiting", "generating", "done", "failed"} {
		rule, ok := config.StatusMapping.UpstreamStatus[status]
		if !ok {
			return nil, fmt.Errorf("hyper3d: 状态词表缺少 %q", status)
		}
		want := map[string]string{"waiting": "accepted", "generating": "running", "done": "completed", "failed": "failed"}[status]
		if rule.Phase != want {
			return nil, fmt.Errorf("hyper3d: 状态词表 %q 相位必须为 %q", status, want)
		}
	}
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:        config.BaseURL,
		APIKey:         config.APIKey,
		AuthHeader:     config.AuthHeader,
		AuthPrefix:     config.AuthPrefix,
		RequestTimeout: config.RequestTimeout,
		HTTPClient:     config.HTTPClient,
		ErrorMapping:   config.ErrorMapping,
		SpoolDir:       config.ScratchDir,
	})
	if err != nil {
		return nil, fmt.Errorf("hyper3d: %w", err)
	}
	return &Adapter{
		client:          client,
		inputs:          config.InputAssets,
		errorMapping:    config.ErrorMapping,
		statusMapping:   config.StatusMapping,
		allowHTTPAssets: config.AllowHTTPAssets,
		scratchDir:      strings.TrimSpace(config.ScratchDir),
	}, nil
}

// ValidateOffering rejects wire configurations that would silently fall back
// from Rodin Gen-2.5 to Gen-1/1.5, or put duplicate tier values on the form.
func ValidateOffering(offering generation.Offering) error {
	var options wireOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return fmt.Errorf("provider_options JSON 无效: %w", err)
	}
	if _, err := parseDownloadFiles(options.DownloadFiles, offering); err != nil {
		return err
	}
	if options.Endpoint == bangEndpoint {
		return validateBangOffering(offering, options)
	}
	if options.Endpoint == textureEndpoint {
		return validateTextureOffering(offering, options)
	}
	if offering.Capability != generation.TextTo3D && offering.Capability != generation.ImageTo3D {
		return fmt.Errorf("Rodin 仅支持文生 3D 与图生 3D")
	}
	for _, endpoint := range []string{options.Endpoint, options.ObserveEndpoint, options.DownloadEndpoint} {
		if !safeEndpoint(endpoint) {
			return fmt.Errorf("Rodin endpoint 必须是安全的相对路径段")
		}
	}
	var template map[string]any
	if err := json.Unmarshal(options.RequestTemplate, &template); err != nil || len(template) == 0 {
		return fmt.Errorf("Rodin multipart request_template 不能为空")
	}
	for field := range template {
		if field == "images" || field == "prompt" {
			return fmt.Errorf("Rodin request_template 不得覆盖输入字段 %q", field)
		}
	}
	tier, tierInTemplate := template["tier"].(string)
	if template["geometry_file_format"] != "glb" {
		return fmt.Errorf("Rodin 首版必须声明 GLB 模型格式")
	}
	modelContract := false
	for _, output := range offering.Outputs {
		if output.Role == "model" && (output.Minimum == nil || *output.Minimum >= 1) &&
			len(output.MediaTypes) == 1 && output.MediaTypes[0] == "model/gltf-binary" {
			modelContract = true
		}
	}
	if !modelContract {
		return fmt.Errorf("Rodin 首版必须声明必需 GLB 产物")
	}
	tierParameter := false
	hasPrompt, hasImages := false, false
	for _, parameter := range offering.Parameters {
		wireName := effectiveWireName(parameter)
		switch wireName {
		case "tier":
			tierParameter = true
			if !parameter.Required && len(parameter.Default) == 0 {
				return fmt.Errorf("Rodin tier 参数必须必填或有默认值")
			}
			for _, option := range parameter.Options {
				if !strings.HasPrefix(option.Value, "Gen-2.5-") {
					return fmt.Errorf("Rodin tier 选项必须是 Gen-2.5")
				}
			}
			if len(parameter.Default) > 0 {
				var defaultTier string
				if json.Unmarshal(parameter.Default, &defaultTier) != nil || !strings.HasPrefix(defaultTier, "Gen-2.5-") {
					return fmt.Errorf("Rodin tier 默认值必须是 Gen-2.5")
				}
			}
		case "prompt":
			hasPrompt = true
		case "images":
			hasImages = parameter.Type == "asset-array" && parameter.MaxBytes > 0 && parameter.MinItems >= 1 && parameter.MaxItems >= 1 && parameter.MaxItems <= 5
		}
	}
	if tierInTemplate == tierParameter {
		return fmt.Errorf("Rodin tier 必须恰好从模板或参数提供一次")
	}
	if tierInTemplate && !strings.HasPrefix(tier, "Gen-2.5-") {
		return fmt.Errorf("Rodin tier 必须显式选择 Gen-2.5")
	}
	if !hasPrompt || offering.Capability == generation.ImageTo3D && !hasImages {
		return fmt.Errorf("Rodin Offering 缺少 prompt 或 images 输入映射")
	}
	return nil
}

func (a *Adapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	if err := ValidateOffering(request.Offering); err != nil {
		return generation.Observation{}, fail(generation.FailureProtocol, false, err.Error())
	}
	var options wireOptions
	_ = json.Unmarshal(request.Offering.ProviderOptions, &options)
	if options.Endpoint == bangEndpoint || options.Endpoint == textureEndpoint {
		return a.startProcessing(ctx, request, options)
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil || input == nil {
		return generation.Observation{}, fail(generation.FailureInvalidInput, false, "输入不是 JSON 对象")
	}
	var template map[string]any
	_ = json.Unmarshal(options.RequestTemplate, &template)
	selectedTier := template["tier"]
	if selectedTier == nil {
		selectedTier = inputValueForWire(request.Offering, input, "tier")
	}
	if tier, ok := selectedTier.(string); !ok || !strings.HasPrefix(tier, "Gen-2.5-") {
		return generation.Observation{}, fail(generation.FailureInvalidInput, false, "生成档位缺失或无效")
	}
	if request.Offering.Capability == generation.TextTo3D {
		prompt, ok := inputValueForWire(request.Offering, input, "prompt").(string)
		if !ok || strings.TrimSpace(prompt) == "" {
			return generation.Observation{}, fail(generation.FailureInvalidInput, false, "文生 3D 缺少提示词")
		}
	} else {
		imageCount := 0
		for _, parameter := range request.Offering.Parameters {
			if effectiveWireName(parameter) == "images" {
				imageCount += len(request.InputAssets[parameter.Name])
			}
		}
		if imageCount < 1 || imageCount > 5 || a.inputs == nil {
			return generation.Observation{}, fail(generation.FailureInvalidInput, false, "图生 3D 输入图片数量或归属不合法")
		}
	}
	// Preflight the complete multipart body before writing a paid-dispatch intent.
	// A local asset read/size failure then cannot strand an intent for a request
	// that was provably never sent. httpjson still protects transport replay.
	body, contentType, err := imagewire.EncodeMultipart(ctx, request, a.inputs)
	if err != nil {
		return generation.Observation{}, multipartInputFailure("上游文件输入无效", err)
	}
	return a.submitMultipart(ctx, request, options, body, contentType, "rodin.create")
}

func (a *Adapter) submitMultipart(ctx context.Context, request generation.StartRequest, options wireOptions, body io.Reader, contentType, stage string) (generation.Observation, error) {
	if closer, ok := body.(io.Closer); ok {
		defer closer.Close()
	}
	file, err := os.CreateTemp(a.scratchDir, httpjson.ScratchFilePrefix+"hyper3d-*")
	if err != nil {
		return generation.Observation{}, fail(generation.FailureTransport, true, "无法准备上游上传")
	}
	defer os.Remove(file.Name())
	defer file.Close()
	if _, err := io.Copy(file, body); err != nil {
		return generation.Observation{}, fail(generation.FailureInvalidInput, false, "本地输入不可读取或超过限制")
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return generation.Observation{}, fail(generation.FailureTransport, true, "无法读取上游上传")
	}
	intent, _ := json.Marshal(map[string]string{
		"stage": stage, "submission_key": request.SubmissionKey, "endpoint": options.Endpoint,
	})
	permit, err := generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: intent})
	if err != nil {
		return generation.Observation{}, err
	}
	var response submitResponse
	if err := a.client.Do(ctx, http.MethodPost, options.Endpoint, file, contentType, request.SubmissionKey, &response, true); err != nil {
		return generation.Observation{}, err
	}
	if err := a.businessError(response.rodinError); err != nil {
		return generation.Observation{}, err
	}
	uuid := strings.TrimSpace(response.UUID)
	key := strings.TrimSpace(response.Jobs.SubscriptionKey)
	if uuid == "" || key == "" {
		return generation.Observation{}, submissionUnknown("上游提交响应缺少可对账标识")
	}
	state := checkpoint{TaskUUID: uuid, SubscriptionKey: key, ConsumedCredit: numericEvidence(response.Consumed)}
	encoded, _ := json.Marshal(state)
	return generation.Observation{
		Phase:  generation.UpstreamAccepted,
		Job:    &generation.UpstreamJob{ProviderID: request.Offering.ProviderID, RemoteID: uuid},
		Opaque: encoded, PollAfter: pollDelay(request.Offering.PollInterval, 0),
		DedupeKey: uuid + ":accepted", Dispatch: permit,
	}, nil
}

func (a *Adapter) Observe(ctx context.Context, request generation.ObserveRequest) (generation.Observation, error) {
	if request.Job.ProviderID != request.Offering.ProviderID || strings.TrimSpace(request.Job.RemoteID) == "" {
		return generation.Observation{}, fail(generation.FailureProtocol, false, "上游任务身份不匹配")
	}
	var state checkpoint
	if err := json.Unmarshal(request.Opaque, &state); err != nil || state.SubscriptionKey == "" || state.TaskUUID != request.Job.RemoteID {
		return generation.Observation{}, fail(generation.FailureProtocol, false, "上游状态订阅键缺失或任务身份不匹配")
	}
	var options wireOptions
	if err := json.Unmarshal(request.Offering.ProviderOptions, &options); err != nil || !safeEndpoint(options.ObserveEndpoint) || !safeEndpoint(options.DownloadEndpoint) {
		return generation.Observation{}, fail(generation.FailureProtocol, false, "上游轮询路由无效")
	}
	var status statusResponse
	if err := a.client.DoJSON(ctx, http.MethodPost, options.ObserveEndpoint,
		map[string]string{"subscription_key": state.SubscriptionKey}, string(request.AttemptID), &status, false); err != nil {
		return generation.Observation{}, err
	}
	if err := a.businessError(status.rodinError); err != nil {
		return generation.Observation{}, err
	}
	if len(status.Jobs) == 0 {
		return generation.Observation{}, fail(generation.FailureProtocol, true, "上游状态响应没有子作业")
	}
	state.PollCount++
	encoded, _ := json.Marshal(state)
	phase, failed := generation.UpstreamAccepted, false
	failure := &generation.Failure{Class: generation.FailureRejected, SafeDetail: "上游子作业生成失败"}
	allDone, anyRunning := true, false
	for _, job := range status.Jobs {
		word := strings.ToLower(strings.TrimSpace(job.Status))
		rule, ok := a.statusMapping.UpstreamStatus[word]
		if !ok {
			return generation.Observation{}, fail(generation.FailureProtocol, false, "上游返回未知子作业状态")
		}
		switch rule.Phase {
		case "accepted":
			allDone = false
		case "running":
			allDone, anyRunning = false, true
		case "completed":
		case "failed":
			failed = true
			if rule.Class != "" {
				failure.Class = generation.FailureClass(rule.Class)
			}
			failure.Retryable = rule.Retryable
		default:
			return generation.Observation{}, fail(generation.FailureProtocol, false, "上游状态词表包含无效相位")
		}
	}
	if failed {
		return generation.Observation{
			Phase: generation.UpstreamFailed, Job: &request.Job, Opaque: encoded,
			Failure:   failure,
			DedupeKey: state.TaskUUID + ":failed",
		}, nil
	}
	if !allDone {
		if anyRunning {
			phase = generation.UpstreamRunning
		}
		return generation.Observation{
			Phase: phase, Job: &request.Job, Opaque: encoded,
			PollAfter: pollDelay(request.Offering.PollInterval, state.PollCount),
			DedupeKey: fmt.Sprintf("%s:%s:%d", state.TaskUUID, phase, state.PollCount),
		}, nil
	}
	var downloads downloadResponse
	if err := a.client.DoJSON(ctx, http.MethodPost, options.DownloadEndpoint,
		map[string]string{"task_uuid": state.TaskUUID}, string(request.AttemptID), &downloads, false); err != nil {
		return generation.Observation{}, err
	}
	if err := a.businessError(downloads.rodinError); err != nil {
		return generation.Observation{}, err
	}
	files, err := parseDownloadFiles(options.DownloadFiles, request.Offering)
	if err != nil {
		return generation.Observation{}, fail(generation.FailureProtocol, false, "上游下载文件声明无效")
	}
	claims := downloadClaims(downloads, request.AttemptID, request.Offering, files)
	modelFound := false
	for _, claim := range claims {
		if claim.Role == "model" {
			modelFound = true
			break
		}
	}
	if !modelFound && files != nil && len(downloads.List) > 0 {
		state.UnmatchedLists++
		encoded, _ = json.Marshal(state)
		if state.UnmatchedLists >= maxUnmatchedLists {
			return generation.Observation{
				Phase: generation.UpstreamFailed, Job: &request.Job, Opaque: encoded,
				Failure:   &generation.Failure{Class: generation.FailureProtocol, SafeDetail: "上游未返回声明的模型文件"},
				DedupeKey: state.TaskUUID + ":files-unmatched",
			}, nil
		}
	}
	if !modelFound {
		return generation.Observation{
			Phase: generation.UpstreamRunning, Job: &request.Job, Opaque: encoded,
			PollAfter: pollDelay(request.Offering.PollInterval, state.PollCount),
			DedupeKey: fmt.Sprintf("%s:files-pending:%d", state.TaskUUID, state.PollCount),
		}, nil
	}
	return generation.Observation{
		Phase: generation.UpstreamCompleted, Job: &request.Job, Opaque: encoded,
		Progress: 100, Artifacts: claims, DedupeKey: state.TaskUUID + ":completed",
	}, nil
}

func (a *Adapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	// Signed URLs are untrusted. OpenClaim enforces scheme, declared host allowlist,
	// and the same constraints on redirects before returning bytes to materialize.
	body, metadata, err := httpjson.OpenClaim(ctx, a.client.HTTPClient, request.Claim, request.Offering.AssetHosts, a.allowHTTPAssets)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	if request.Claim.Role != "model" || request.Claim.Variant != "glb" {
		return body, metadata, nil
	}
	header := make([]byte, 20)
	if _, err := io.ReadFull(body, header); err != nil ||
		!bytes.Equal(header[:4], []byte("glTF")) ||
		binary.LittleEndian.Uint32(header[4:8]) != 2 ||
		binary.LittleEndian.Uint32(header[8:12]) < 24 ||
		binary.LittleEndian.Uint32(header[12:16]) < 4 ||
		binary.LittleEndian.Uint32(header[12:16])%4 != 0 ||
		!bytes.Equal(header[16:20], []byte("JSON")) ||
		int64(binary.LittleEndian.Uint32(header[12:16]))+20 > int64(binary.LittleEndian.Uint32(header[8:12])) ||
		(metadata.Size >= 0 && int64(binary.LittleEndian.Uint32(header[8:12])) != metadata.Size) {
		_ = body.Close()
		return nil, generation.AssetMetadata{}, fail(generation.FailureIntegrity, false, "上游模型文件格式无效")
	}
	return &verifiedGLBBody{
		Reader:    io.MultiReader(bytes.NewReader(header), body),
		Closer:    body,
		remaining: int64(binary.LittleEndian.Uint32(header[8:12])),
	}, metadata, nil
}

// verifiedGLBBody checks the actual byte count even when the HTTP response is
// chunked and has no Content-Length. Materialization must consume through EOF.
type verifiedGLBBody struct {
	io.Reader
	io.Closer
	remaining int64
}

func (b *verifiedGLBBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		var extra [1]byte
		n, err := b.Reader.Read(extra[:])
		if n != 0 {
			return 0, fail(generation.FailureIntegrity, false, "上游模型文件长度与 GLB 声明不符")
		}
		return 0, err
	}
	if int64(len(p)) > b.remaining {
		p = p[:int(b.remaining)]
	}
	n, err := b.Reader.Read(p)
	b.remaining -= int64(n)
	if err == io.EOF && b.remaining > 0 {
		return n, fail(generation.FailureIntegrity, false, "上游模型文件长度与 GLB 声明不符")
	}
	return n, err
}

func effectiveWireName(parameter generation.Parameter) string {
	if parameter.WireName != "" {
		return parameter.WireName
	}
	return parameter.Name
}

func inputValueForWire(offering generation.Offering, input map[string]any, wireName string) any {
	for _, parameter := range offering.Parameters {
		if effectiveWireName(parameter) == wireName {
			return input[parameter.Name]
		}
	}
	return nil
}

func downloadClaims(response downloadResponse, attemptID generation.AttemptID, offering generation.Offering, files []downloadFile) []generation.ArtifactClaim {
	result := make([]generation.ArtifactClaim, 0, len(response.List))
	seen := make(map[string]struct{})
	selected := make([]bool, len(files))
	for _, item := range response.List {
		name := path.Base(strings.TrimSpace(item.Name))
		rule := -1
		for index, file := range files {
			if !selected[index] && file.matches(name) {
				rule = index
				break
			}
		}
		if files != nil && rule < 0 {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(item.URL))
		if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
			continue
		}
		role, mediaType, variant := fileKind(name)
		if role == "model" {
			declared, ok := declaredArtifactMedia(offering, role, variant)
			if !ok {
				continue
			}
			mediaType = declared
		}
		if role == "" {
			continue
		}
		identity := role + ":" + name
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		if rule >= 0 {
			selected[rule] = true
		}
		digest := sha256.Sum256([]byte(identity))
		result = append(result, generation.ArtifactClaim{
			ID:   fmt.Sprintf("%s:%s", attemptID, hex.EncodeToString(digest[:8])),
			Role: role, Variant: variant, Locator: item.URL, MediaType: mediaType,
		})
	}
	return result
}

func fileKind(name string) (role, mediaType, variant string) {
	switch strings.ToLower(path.Ext(name)) {
	case ".glb":
		return "model", "model/gltf-binary", "glb"
	case ".gltf":
		return "model", "model/gltf+json", "gltf"
	case ".obj":
		return "model", "model/obj", "obj"
	case ".fbx":
		return "model", "model/fbx", "fbx"
	case ".stl":
		return "model", "model/stl", "stl"
	case ".usdz":
		return "model", "model/vnd.usdz+zip", "usdz"
	case ".usd":
		return "model", "model/vnd.usd", "usd"
	case ".usda":
		return "model", "model/vnd.usda", "usda"
	case ".usdc":
		return "model", "model/vnd.usdc", "usdc"
	case ".png":
		return "thumbnail", "image/png", "primary"
	case ".jpg", ".jpeg":
		return "thumbnail", "image/jpeg", "primary"
	case ".webp":
		return "thumbnail", "image/webp", "primary"
	default:
		return "", "", ""
	}
}

func modelFileKind(mediaType string) (extension, variant string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(mediaType)) {
	case "model/gltf-binary":
		return ".glb", "glb", true
	case "model/gltf+json":
		return ".gltf", "gltf", true
	case "model/obj":
		return ".obj", "obj", true
	case "model/fbx", "application/vnd.autodesk.fbx":
		return ".fbx", "fbx", true
	case "model/stl", "application/sla":
		return ".stl", "stl", true
	case "model/vnd.usdz+zip", "model/usdz":
		return ".usdz", "usdz", true
	case "model/vnd.usd":
		return ".usd", "usd", true
	case "model/vnd.usda":
		return ".usda", "usda", true
	case "model/vnd.usdc":
		return ".usdc", "usdc", true
	default:
		return "", "", false
	}
}

func declaredOutputMedia(offering generation.Offering, role, variant string) (string, bool) {
	for _, output := range offering.Outputs {
		if output.Role != role || output.Minimum != nil && *output.Minimum < 1 {
			continue
		}
		for _, accepted := range output.MediaTypes {
			if _, acceptedVariant, ok := modelFileKind(accepted); ok && acceptedVariant == variant {
				return accepted, true
			}
		}
	}
	return "", false
}

// declaredRequiredModel reports whether a success-blocking model output accepts
// the variant; background and on-demand slots do not count.
func declaredRequiredModel(offering generation.Offering, variant string) bool {
	for _, output := range offering.Outputs {
		if output.Role != "model" || output.Minimum != nil && *output.Minimum < 1 ||
			output.Delivery != "" && output.Delivery != generation.AssetDeliveryRequired {
			continue
		}
		for _, accepted := range output.MediaTypes {
			if _, acceptedVariant, ok := modelFileKind(accepted); ok && acceptedVariant == variant {
				return true
			}
		}
	}
	return false
}

func declaredArtifactMedia(offering generation.Offering, role, variant string) (string, bool) {
	for _, output := range offering.Outputs {
		if output.Role != role {
			continue
		}
		for _, accepted := range output.MediaTypes {
			if _, acceptedVariant, ok := modelFileKind(accepted); ok && acceptedVariant == variant {
				return accepted, true
			}
		}
	}
	return "", false
}

func (a *Adapter) businessError(response rodinError) error {
	value := strings.TrimSpace(string(response.Error))
	if value == "" || value == "null" || value == `""` {
		return nil
	}
	var code string
	if err := json.Unmarshal(response.Error, &code); err != nil {
		var object struct {
			Code string `json:"code"`
		}
		if json.Unmarshal(response.Error, &object) == nil {
			code = object.Code
		}
	}
	class, retryable := generation.FailureRejected, false
	if a.errorMapping != nil {
		if rule, ok := a.errorMapping.UpstreamCodeMap[code]; ok {
			class, retryable = generation.FailureClass(rule.Class), rule.Retryable
		}
	}
	return &generation.ProviderError{Failure: &generation.Failure{
		Class: class, Retryable: retryable, UpstreamCode: code,
		SafeDetail: "上游返回业务错误",
	}}
}

func pollDelay(configured time.Duration, count int) time.Duration {
	if count < 0 {
		count = 0
	}
	if count > 5 {
		count = 5
	}
	delay := time.Duration(5+count*5) * time.Second
	if configured > 30*time.Second {
		configured = 30 * time.Second
	}
	if configured > delay {
		return configured
	}
	return delay
}

func safeEndpoint(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, `/\\?#`) && url.PathEscape(value) == value
}

func fail(class generation.FailureClass, retryable bool, detail string) error {
	return &generation.ProviderError{Failure: &generation.Failure{
		Class: class, Retryable: retryable, SafeDetail: detail,
	}}
}

// multipartInputFailure refuses a request whose multipart body cannot be
// built. A file without a reliable upload extension says so, because Hyper3D
// reads the model format from the part's filename.
func multipartInputFailure(detail string, err error) error {
	if errors.Is(err, uploadname.ErrNoReliableExtension) {
		detail += "：" + err.Error()
	}
	return fail(generation.FailureInvalidInput, false, detail)
}

func submissionUnknown(detail string) error {
	return &generation.ProviderError{Failure: &generation.Failure{
		Class: generation.FailureSubmissionUnknown, Ambiguous: true, SafeDetail: detail,
	}}
}

var _ generation.ProviderPort = (*Adapter)(nil)
