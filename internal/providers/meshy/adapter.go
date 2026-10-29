package meshy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/schema"
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
	InputPublisher  generation.InputAssetPublisher
	// ScratchDir holds private upload copies (spooled multipart bodies).
	// Empty means os.TempDir(); production passes the storage volume's tmp
	// directory because the container's /tmp is a small tmpfs (#321 A).
	ScratchDir string
}

type Adapter struct {
	client          *httpjson.Client
	allowHTTPAssets bool
	inputAssets     generation.InputAssetResolver
	inputPublisher  generation.InputAssetPublisher
	errorMapping    *generation.ErrorMapping
	statusMapping   *generation.StatusMapping
}

func New(config Config) (*Adapter, error) {
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:        config.BaseURL,
		APIKey:         config.APIKey,
		AuthHeader:     config.AuthHeader,
		AuthPrefix:     config.AuthPrefix,
		RequestTimeout: config.RequestTimeout,
		ErrorMapping:   config.ErrorMapping,
		HTTPClient:     config.HTTPClient,
		SpoolDir:       config.ScratchDir,
	})
	if err != nil {
		return nil, fmt.Errorf("meshy: %w", err)
	}
	return &Adapter{
		client:          client,
		allowHTTPAssets: config.AllowHTTPAssets,
		inputAssets:     config.InputAssets,
		inputPublisher:  config.InputPublisher,
		errorMapping:    config.ErrorMapping,
		statusMapping:   config.StatusMapping,
	}, nil
}

func (a *Adapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	endpoint, version, err := wireRoute(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	var body map[string]any
	if err := json.Unmarshal(request.Input, &body); err != nil {
		return generation.Observation{}, providerFailure(
			generation.FailureInvalidInput,
			false,
			"规范化输入不是 JSON 对象",
		)
	}
	if err := validateRequiredModelFormats(request.Offering, body); err != nil {
		return generation.Observation{}, err
	}
	if request.Offering.Capability == generation.TextToImage || request.Offering.Capability == generation.ImageToImage {
		body, err = imagewire.Build(ctx, request, a.inputAssets)
		if err != nil {
			return generation.Observation{}, providerFailure(generation.FailureInvalidInput, false, err.Error())
		}
	} else {
		if err := a.projectInput(ctx, request, body); err != nil {
			return generation.Observation{}, err
		}
		if endpoint == "multi-image-to-3d" {
			if err := a.addMultiImageInputs(ctx, request, body); err != nil {
				return generation.Observation{}, err
			}
		} else if request.Offering.Capability == generation.ImageTo3D {
			if err := a.addReferenceImages(ctx, request, body); err != nil {
				return generation.Observation{}, err
			}
		}
	}
	// 非幂等付费创建提交前先持久化私有派发意图并取得发送许可（W4 §4.5）。
	dispatchPermit, err := privateDispatchPermit(ctx, request.SubmissionKey, dispatchIntentStage{
		Stage:   "task.create",
		Details: map[string]any{"endpoint": endpoint, "version": version},
	})
	if err != nil {
		return generation.Observation{}, err
	}
	var response struct {
		Result string `json:"result"`
	}
	if err := a.client.DoJSON(
		ctx,
		http.MethodPost,
		fmt.Sprintf("openapi/%s/%s", version, endpoint),
		body,
		request.SubmissionKey,
		&response,
		true,
	); err != nil {
		return generation.Observation{}, expiredSourceFailure(request, err)
	}
	if strings.TrimSpace(response.Result) == "" {
		return generation.Observation{}, submissionUnknown("Meshy 创建响应缺少 result")
	}
	job := &generation.UpstreamJob{
		ProviderID: request.Offering.ProviderID,
		RemoteID:   response.Result,
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       job,
		Progress:  0,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: response.Result + ":accepted",
		Dispatch:  dispatchPermit,
	}, nil
}

// A multi-format Offering freezes one required model slot per requested format.
// Older Offering snapshots with only a generic model slot retain their original
// selectable-format behavior.
func validateRequiredModelFormats(offering generation.Offering, input map[string]any) error {
	// Rigging has two fixed output formats but no target_formats input. Only
	// generation/processing Offerings that declare that input may require the
	// caller to select every output format explicitly.
	declaresTargetFormats := false
	for _, parameter := range offering.Parameters {
		if parameter.Name == "target_formats" {
			declaresTargetFormats = true
			break
		}
	}
	if !declaresTargetFormats {
		return nil
	}
	required := make(map[string]struct{})
	declared := make(map[string]struct{})
	for _, output := range offering.Outputs {
		if output.Role != "model" || output.Variant == "" {
			continue
		}
		declared[output.Variant] = struct{}{}
		if output.Minimum == nil || *output.Minimum < 1 || output.Delivery == "background" {
			continue
		}
		required[output.Variant] = struct{}{}
	}
	if len(required) < 2 {
		return nil
	}
	// Optional (background) formats may be requested too; every required one
	// must be, and nothing outside the declared slots.
	selected, ok := input["target_formats"].([]any)
	if !ok {
		return providerFailure(generation.FailureInvalidInput, false, "导出格式必须覆盖全部必需模型格式")
	}
	seen := make(map[string]struct{}, len(selected))
	for _, raw := range selected {
		format, ok := raw.(string)
		if !ok {
			return providerFailure(generation.FailureInvalidInput, false, "导出格式必须覆盖全部必需模型格式")
		}
		if _, known := declared[format]; !known {
			return providerFailure(generation.FailureInvalidInput, false, "导出格式必须覆盖全部必需模型格式")
		}
		if _, duplicate := seen[format]; duplicate {
			return providerFailure(generation.FailureInvalidInput, false, "导出格式不能重复")
		}
		seen[format] = struct{}{}
	}
	for format := range required {
		if _, ok := seen[format]; !ok {
			return providerFailure(generation.FailureInvalidInput, false, "导出格式必须覆盖全部必需模型格式")
		}
	}
	return nil
}

// dispatchIntentStage 描述一次私有派发意图的阶段与协调标识。
type dispatchIntentStage struct {
	Stage         string         `json:"stage"`
	SubmissionKey string         `json:"submission_key"`
	Details       map[string]any `json:"details,omitempty"`
}

func privateDispatchPermit(
	ctx context.Context,
	submissionKey string,
	stage dispatchIntentStage,
) (*generation.DispatchReceipt, error) {
	stage.SubmissionKey = submissionKey
	encoded, err := json.Marshal(stage)
	if err != nil {
		return nil, providerFailure(generation.FailureInternal, false, fmt.Sprintf("编码私有派发意图失败: %v", err))
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: encoded})
}

func (a *Adapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	endpoint, version, err := wireRoute(request.Offering)
	if err != nil {
		return generation.Observation{}, err
	}
	if refineAfterPreview(request.Offering) {
		return a.observeWithRefine(ctx, request, endpoint, version)
	}
	var response taskResponse
	if err := a.client.DoJSON(
		ctx,
		http.MethodGet,
		fmt.Sprintf("openapi/%s/%s/%s", version, endpoint, url.PathEscape(request.Job.RemoteID)),
		nil,
		string(request.AttemptID),
		&response,
		false,
	); err != nil {
		return generation.Observation{}, err
	}
	return a.observation(request, response)
}

func (a *Adapter) DecodeWebhook(
	_ context.Context,
	request generation.WebhookDecodeRequest,
) (generation.Observation, error) {
	// 先预览后精细化的任务只由轮询推进：预览的 webhook 不能完成任务，
	// 也不带私有检查点。
	if refineAfterPreview(request.Offering) {
		return generation.Observation{}, generation.ErrWebhookNotApplicable
	}
	var response taskResponse
	if err := json.Unmarshal(request.Payload, &response); err != nil {
		return generation.Observation{}, providerFailure(
			generation.FailureProtocol,
			false,
			"Meshy Webhook 返回了无效 JSON",
		)
	}
	payloadID := strings.TrimSpace(response.ID)
	if payloadID == "" {
		if encodedID, exists := response.Raw["task_id"]; exists {
			_ = json.Unmarshal(encodedID, &payloadID)
			payloadID = strings.TrimSpace(payloadID)
		}
	}
	if payloadID == "" || payloadID != request.Job.RemoteID ||
		request.Job.ProviderID != request.Offering.ProviderID {
		return generation.Observation{}, providerFailure(
			generation.FailureProtocol,
			false,
			"Meshy Webhook 任务身份不匹配",
		)
	}
	return a.observation(generation.ObserveRequest{
		TaskID:    request.TaskID,
		AttemptID: request.AttemptID,
		Offering:  request.Offering,
		Job:       request.Job,
	}, response)
}

func (a *Adapter) OpenAsset(
	ctx context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	body, metadata, err := httpjson.OpenClaim(
		ctx,
		a.client.HTTPClient,
		request.Claim,
		request.Offering.AssetHosts,
		a.allowHTTPAssets,
	)
	if err != nil {
		return body, metadata, err
	}
	// Meshy 的 CDN 按宿主机 mime.types 标注下载内容，OBJ 会得到
	// application/x-tgif（TGIF 是一种图像类型）。该标签不携带模型格式
	// 信息，保留 Adapter 按协议声明的精确类型；通用 octet-stream 仍由内核
	// 保留声明，text/html 等具体响应原样参与物化契约校验。
	if declared := request.Claim.MediaType; strings.HasPrefix(declared, "model/") &&
		baseMediaType(metadata.MediaType) == "application/x-tgif" {
		metadata.MediaType = declared
	}
	return body, metadata, nil
}

func (a *Adapter) DeleteProviderTask(
	ctx context.Context,
	request generation.DeleteProviderRequest,
) error {
	endpoint, version, err := wireRoute(request.Offering)
	if err != nil {
		return err
	}
	if strings.TrimSpace(request.Job.RemoteID) == "" ||
		request.Job.ProviderID != request.Offering.ProviderID {
		return providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy 删除请求缺少匹配的上游任务身份",
		)
	}
	return a.client.Do(
		ctx,
		http.MethodDelete,
		fmt.Sprintf(
			"openapi/%s/%s/%s",
			version,
			endpoint,
			url.PathEscape(request.Job.RemoteID),
		),
		nil,
		"",
		string(request.AttemptID),
		nil,
		false,
	)
}

type wireOptions struct {
	ResultIdentity      *resultIdentity                  `json:"result_identity,omitempty"`
	Endpoint            string                           `json:"endpoint"`
	APIVersion          string                           `json:"api_version"`
	TaskType            string                           `json:"task_type,omitempty"`
	ModelParameterValue string                           `json:"model_parameter_value,omitempty"`
	ModelParameter      string                           `json:"model_parameter,omitempty"`
	InputPaths          map[string][]string              `json:"input_paths,omitempty"`
	SourceResults       []sourceResultBinding            `json:"source_result_bindings,omitempty"`
	ResultAssets        []resultAssetProjection          `json:"result_assets,omitempty"`
	ResultReports       []resultReportProjection         `json:"result_reports,omitempty"`
	InlineModelInput    *inlineModelInput                `json:"inline_model_input,omitempty"`
	ModelInputProfile   *schema.ModelInputProfileOptions `json:"model_input_profile,omitempty"`
	// RefineAfterPreview 让 Text to 3D 在一次公开任务内先预览再精细化，
	// 交付带贴图的最高质量模型（见 refine.go）。精细化请求的覆盖值与
	// 预览任务引用参数都由 Schema 声明。
	RefineAfterPreview *refineDeclaration `json:"refine_after_preview,omitempty"`
}

// inlineModelInput 声明该 Offering 的 asset 型输入在资产网关不可用时可以
// 以 data URI 内联提交，上限由 Product Schema 拥有，不在代码中建立第二真源。
type inlineModelInput struct {
	MaxBytes  int64  `json:"max_bytes"`
	MediaType string `json:"media_type,omitempty"`
}

type resultAssetProjection struct {
	Path      string `json:"path"`
	Role      string `json:"role"`
	Mode      string `json:"mode"`
	Variant   string `json:"variant,omitempty"`
	MediaType string `json:"media_type,omitempty"`
}

// ValidateOffering 在进程启动时校验 Meshy wire Schema，避免任务已提交并扣费后
// 才发现结果资产投影缺失或无效。
func ValidateOffering(offering generation.Offering) error {
	if _, _, err := wireRoute(offering); err != nil {
		return err
	}
	var options wireOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return fmt.Errorf("provider_options 不是合法 JSON: %w", err)
	}
	if options.TaskType != "" && !safePathSegment(options.TaskType) {
		return errors.New("provider_options.task_type 无效")
	}
	for _, parameter := range offering.Parameters {
		if err := validateImageInputParameter(parameter); err != nil {
			return err
		}
	}
	if err := validateInputPaths(offering, options); err != nil {
		return err
	}
	if err := validateResultIdentity(offering, options); err != nil {
		return err
	}
	if err := validateSourceResults(offering, options); err != nil {
		return err
	}
	if len(options.ResultAssets) == 0 && len(options.ResultReports) == 0 {
		return errors.New("provider_options 缺少 result_assets")
	}
	if err := validateMultiImageOffering(offering, options); err != nil {
		return err
	}
	if err := validateResultReports(offering, options); err != nil {
		return err
	}
	seenPaths := make(map[string]struct{}, len(options.ResultAssets))
	for index, projection := range options.ResultAssets {
		projection.Path = strings.TrimSpace(projection.Path)
		projection.Role = strings.TrimSpace(projection.Role)
		projection.Mode = strings.TrimSpace(projection.Mode)
		if projection.Path == "" || projection.Role == "" || projection.Mode == "" {
			return fmt.Errorf(
				"result_assets[%d] 缺少 path、role 或 mode",
				index,
			)
		}
		switch projection.Mode {
		case "scalar", "map", "array", "array-map", "tree":
		default:
			return fmt.Errorf(
				"result_assets[%d] mode %q 不受支持",
				index,
				projection.Mode,
			)
		}
		if _, duplicated := seenPaths[projection.Path]; duplicated {
			return fmt.Errorf("result_assets 路径 %q 重复", projection.Path)
		}
		seenPaths[projection.Path] = struct{}{}
		if err := validateResultMediaType(offering, projection); err != nil {
			return fmt.Errorf("result_assets[%d]: %w", index, err)
		}
	}
	if options.InlineModelInput != nil && options.InlineModelInput.MaxBytes <= 0 {
		return errors.New("inline_model_input.max_bytes 必须为正数")
	}
	if options.InlineModelInput != nil && options.InlineModelInput.MediaType != "" && options.InlineModelInput.MediaType != "application/octet-stream" && options.InlineModelInput.MediaType != "model/gltf-binary" {
		return errors.New("inline_model_input.media_type 不受支持")
	}
	if _, _, err := profiledModelContract(offering); err != nil {
		return err
	}
	// 精细化声明不完整时不能静默退化成只交付预览网格：启动即拒绝。
	if declaration := options.RefineAfterPreview; declaration != nil &&
		(len(declaration.Set) > 0 || strings.TrimSpace(declaration.SourceParameter) != "") {
		resolved, ok := refineDeclarationOf(offering)
		if !ok {
			return errors.New("refine_after_preview 必须同时声明 set 与 source_parameter")
		}
		declared := false
		for _, parameter := range offering.Parameters {
			if parameter.Name == resolved.SourceParameter {
				declared = true
				break
			}
		}
		if !declared {
			return fmt.Errorf("refine_after_preview.source_parameter %q 不是该 Offering 的参数", resolved.SourceParameter)
		}
	}
	// 下游内网零互联网：上游输入一律内联，不依赖公网签名 URL。
	// asset-array 参考图固定内联；单个 asset 参数必须由 Schema 声明内联上限。
	if options.InlineModelInput == nil {
		for _, parameter := range offering.Parameters {
			if parameter.Type == "asset" {
				return fmt.Errorf("asset 参数 %q 需要 inline_model_input 声明", parameter.Name)
			}
		}
		return nil
	}
	// 超过内联上限的来源上游必然拒绝；max_bytes 不得高于内联上限，
	// 这样补齐格式、处理面板和 Agent 在绑定来源时就把它判为不兼容，而不是提交后失败。
	for _, parameter := range offering.Parameters {
		if parameter.Type != "asset" {
			continue
		}
		if parameter.MaxBytes <= 0 || parameter.MaxBytes > options.InlineModelInput.MaxBytes {
			return fmt.Errorf("asset 参数 %q 的 max_bytes 必须为正数且不超过 inline_model_input.max_bytes（%d）", parameter.Name, options.InlineModelInput.MaxBytes)
		}
	}
	return nil
}

// OfferingSupportsOfflineAssets 报告该 Offering 在没有公网资产网关时能否提交：
// 无 asset 型参数天然支持；有 asset 型参数时必须声明 inline_model_input。
func OfferingSupportsOfflineAssets(offering generation.Offering) bool {
	hasAssetParameter := false
	for _, parameter := range offering.Parameters {
		if parameter.Type == "asset" {
			hasAssetParameter = true
			break
		}
	}
	if !hasAssetParameter {
		return true
	}
	var options wireOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return false
	}
	return options.InlineModelInput != nil
}

// CapabilitySupportsOfflineAssets requires at least one offering for the capability
// and offline input support on every offering in the connection's scoped set.
func CapabilitySupportsOfflineAssets(offerings []generation.Offering, capability generation.Capability) bool {
	ready := false
	for _, offering := range offerings {
		if offering.Capability != capability {
			continue
		}
		if !OfferingSupportsOfflineAssets(offering) {
			return false
		}
		ready = true
	}
	return ready
}

func wireRoute(offering generation.Offering) (endpoint string, version string, err error) {
	var options wireOptions
	if unmarshalErr := json.Unmarshal(offering.ProviderOptions, &options); unmarshalErr != nil {
		return "", "", providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy Offering 的 provider_options 不是合法 JSON",
		)
	}
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	options.APIVersion = strings.TrimSpace(options.APIVersion)
	if !safeEndpointPath(options.Endpoint) || !safePathSegment(options.APIVersion) {
		return "", "", providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy Offering Schema 缺少安全的 endpoint 或 api_version",
		)
	}
	return options.Endpoint, options.APIVersion, nil
}

func safeEndpointPath(value string) bool {
	if value == "" {
		return false
	}
	for _, segment := range strings.Split(value, "/") {
		if !safePathSegment(segment) {
			return false
		}
	}
	return true
}

func safePathSegment(value string) bool {
	return value != "" &&
		value != "." &&
		value != ".." &&
		!strings.ContainsAny(value, `/\?#`) &&
		url.PathEscape(value) == value
}

// declaredReferenceMaxBytes 读取 reference_assets 参数声明的字节上限；
// 未声明的历史快照回退 20 MiB（与既有线上行为一致）。
func declaredReferenceMaxBytes(offering generation.Offering) int64 {
	const fallback = int64(20 << 20)
	for _, parameter := range offering.Parameters {
		if parameter.Name != "reference_assets" {
			continue
		}
		if parameter.MaxBytes > 0 {
			return parameter.MaxBytes
		}
		return fallback
	}
	return fallback
}

// addReferenceImages sends the single picture of Meshy's Image to 3D as
// image_url, the only image field that endpoint documents (#329); several
// views belong to the Multi-Image Offering. A request carrying more than one
// picture is refused before anything is sent, whatever its frozen Offering
// snapshot allowed.
func (a *Adapter) addReferenceImages(
	ctx context.Context,
	request generation.StartRequest,
	body map[string]any,
) error {
	dataURIs, err := a.referenceDataURIs(ctx, request)
	if err != nil {
		return err
	}
	if len(dataURIs) != 1 {
		return providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy 图生 3D 只接受一张参考图；多张图请改用多图生 3D",
		)
	}
	delete(body, "reference_assets")
	delete(body, "image_urls")
	body["image_url"] = dataURIs[0]
	return nil
}

// referenceDataURIs reads the resolved reference_assets pictures, in order,
// as data URIs within the declared byte bound.
func (a *Adapter) referenceDataURIs(
	ctx context.Context,
	request generation.StartRequest,
) ([]string, error) {
	assets := request.InputAssets["reference_assets"]
	if a.inputAssets == nil || len(assets) == 0 {
		return nil, providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy 图像能力至少需要一个已解析参考图",
		)
	}
	dataURIs := make([]string, 0, len(assets))
	maxBytes := declaredReferenceMaxBytes(request.Offering)
	for _, asset := range assets {
		if !strings.HasPrefix(asset.MediaType, "image/") {
			return nil, providerFailure(
				generation.FailureInvalidInput,
				false,
				"Meshy 图生 3D 的参考资产必须是图片",
			)
		}
		bodyReader, err := a.inputAssets.Open(ctx, asset)
		if err != nil {
			return nil, providerFailure(
				generation.FailureInvalidInput,
				false,
				"Meshy 参考图不可读取",
			)
		}
		content, readErr := io.ReadAll(io.LimitReader(bodyReader, maxBytes+1))
		closeErr := bodyReader.Close()
		if readErr != nil || closeErr != nil || int64(len(content)) > maxBytes {
			return nil, providerFailure(
				generation.FailureInvalidInput,
				false,
				fmt.Sprintf(
					"Meshy 参考图超过 %d 字节上限或不可读取",
					maxBytes,
				),
			)
		}
		dataURIs = append(
			dataURIs,
			"data:"+asset.MediaType+";base64,"+base64.StdEncoding.EncodeToString(content),
		)
	}
	return dataURIs, nil
}

// inlineImageArray reads an optional image-list parameter's resolved assets
// (owned by the requester, of a declared media type, within the declared
// count and byte bound) and returns them as data URIs in order.
func (a *Adapter) inlineImageArray(ctx context.Context, request generation.StartRequest, parameter schema.Parameter) ([]string, error) {
	assets := request.InputAssets[parameter.Name]
	if a.inputAssets == nil || len(assets) == 0 || (parameter.MinItems > 0 && len(assets) < parameter.MinItems) ||
		(parameter.MaxItems > 0 && len(assets) > parameter.MaxItems) || parameter.MaxBytes <= 0 {
		return nil, providerFailure(generation.FailureInvalidInput, false, fmt.Sprintf("Meshy 参数 %q 的图片数量或上限不符合 Offering Schema", parameter.Name))
	}
	uris := make([]string, 0, len(assets))
	for _, asset := range assets {
		allowed := false
		for _, mediaType := range parameter.MediaTypes {
			allowed = allowed || strings.EqualFold(strings.TrimSpace(mediaType), strings.TrimSpace(asset.MediaType))
		}
		if !allowed || (request.OwnerID != "" && asset.OwnerID != request.OwnerID) {
			return nil, providerFailure(generation.FailureInvalidInput, false, fmt.Sprintf("Meshy 参数 %q 的图片格式或归属不符合 Offering Schema", parameter.Name))
		}
		reader, err := a.inputAssets.Open(ctx, asset)
		if err != nil {
			return nil, providerFailure(generation.FailureInvalidInput, false, fmt.Sprintf("Meshy 参数 %q 的图片不可读取", parameter.Name))
		}
		content, readErr := io.ReadAll(io.LimitReader(reader, parameter.MaxBytes+1))
		closeErr := reader.Close()
		if readErr != nil || closeErr != nil || int64(len(content)) > parameter.MaxBytes {
			return nil, providerFailure(generation.FailureInvalidInput, false, fmt.Sprintf("Meshy 参数 %q 的图片超过 %d 字节上限或不可读取", parameter.Name, parameter.MaxBytes))
		}
		uris = append(uris, "data:"+asset.MediaType+";base64,"+base64.StdEncoding.EncodeToString(content))
	}
	return uris, nil
}

// projectInput 是 Meshy 入参投影的唯一接口。Offering 参数 Schema 决定
// 内部参数名、上游字段名及参数类型；这里不识别 capability 名称。
// omit_when 声明在投影前统一生效（如 ultra_mode 仅在 mode=preview 保留）。
func (a *Adapter) projectInput(
	ctx context.Context,
	request generation.StartRequest,
	body map[string]any,
) error {
	var options wireOptions
	if err := json.Unmarshal(request.Offering.ProviderOptions, &options); err != nil {
		return providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy Offering 的 provider_options 不是合法 JSON",
		)
	}
	if err := validateInputPaths(request.Offering, options); err != nil {
		return providerFailure(generation.FailureInvalidInput, false, err.Error())
	}
	if err := validateSourceResults(request.Offering, options); err != nil {
		return providerFailure(generation.FailureInvalidInput, false, err.Error())
	}
	options.Endpoint = strings.TrimSpace(options.Endpoint)
	for _, parameter := range request.Offering.Parameters {
		if generation.ParameterOmitted(parameter, body) {
			delete(body, parameter.Name)
		}
	}
	options.ModelParameter = strings.TrimSpace(options.ModelParameter)
	if options.ModelParameter != "" {
		_, exists := body[options.ModelParameter]
		if options.Endpoint == multiImageEndpoint || !exists {
			modelValue := strings.TrimSpace(options.ModelParameterValue)
			if modelValue == "" {
				modelValue = request.Offering.ModelID
			}
			body[options.ModelParameter] = modelValue
		}
	}
	delete(body, request.Offering.ExtraConsentParameter())
	for _, parameter := range request.Offering.Parameters {
		value, exists := body[parameter.Name]
		if !exists {
			continue
		}
		wireName := strings.TrimSpace(parameter.WireName)
		if wireName == "" {
			wireName = parameter.Name
		}
		switch parameter.Type {
		case "task":
			dependency, dependencyExists :=
				request.TaskDependencies[parameter.Name]
			if !dependencyExists {
				return providerFailure(
					generation.FailureInvalidInput,
					false,
					fmt.Sprintf(
						"Meshy 参数 %q 缺少已解析的任务依赖",
						parameter.Name,
					),
				)
			}
			body[wireName] = dependency.UpstreamJob.RemoteID
		case "asset-array":
			// reference_assets is mapped by addReferenceImages / addMultiImageInputs.
			// Any other image list (Multi-Image texture_image_urls, #329) is
			// inlined here as data URIs in the order chosen, never as asset IDs.
			if parameter.Name == "reference_assets" {
				body[wireName] = value
				break
			}
			uris, err := a.inlineImageArray(ctx, request, parameter)
			if err != nil {
				return err
			}
			body[wireName] = uris
		case "asset":
			assets := request.InputAssets[parameter.Name]
			if len(assets) != 1 {
				return providerFailure(
					generation.FailureInvalidInput,
					false,
					fmt.Sprintf(
						"Meshy 参数 %q 需要一个可发布资产",
						parameter.Name,
					),
				)
			}
			if parameter.ImageInput != nil {
				assetURL, err := a.imageInputWireValue(ctx, request, parameter)
				if err != nil {
					return err
				}
				body[wireName] = assetURL
				break
			}
			assetURL, profiled, err := a.profiledModelWireValue(ctx, request, parameter.Name)
			if err == nil && !profiled {
				assetURL, err = a.assetWireValue(ctx, options, assets[0], request.Offering.Timeout+10*time.Minute, parameter.Name)
			}
			if err != nil {
				return err
			}
			body[wireName] = assetURL
		default:
			if len(parameter.WireValues) > 0 {
				selected, stringValue := value.(string)
				mapped, declared := parameter.WireValues[selected]
				if !stringValue || !declared || json.Unmarshal(mapped, &value) != nil {
					return providerFailure(generation.FailureInvalidInput, false, "参数的上游值映射无效")
				}
				switch value.(type) {
				case string, float64, bool:
				default:
					return providerFailure(generation.FailureInvalidInput, false, "参数的上游值映射必须是标量")
				}
			}
			body[wireName] = value
		}
		if wireName != parameter.Name {
			delete(body, parameter.Name)
		}
	}
	if err := projectInputPaths(request.Offering, options, body); err != nil {
		return providerFailure(generation.FailureInvalidInput, false, err.Error())
	}
	return a.bindSourceResults(ctx, request, options, body)
}

// assetWireValue 把平台资产转换为上游可消费的取值。Offering 声明
// inline_model_input 时一律以 data URI 内联（下游内网零互联网，不依赖
// PUBLIC_BASE_URL 与签名公网 URL）；未声明时才退回公网签名 URL。
// 模型类资产沿用旧站实测可用的 application/octet-stream，图片保留自身类型。
func (a *Adapter) assetWireValue(
	ctx context.Context,
	options wireOptions,
	asset generation.InputAsset,
	validFor time.Duration,
	parameterName string,
) (string, error) {
	if a.inputPublisher != nil && options.InlineModelInput == nil {
		assetURL, err := a.inputPublisher.ExternalURL(ctx, asset, validFor)
		if err != nil {
			return "", providerFailure(
				generation.FailureInternal,
				false,
				fmt.Sprintf("Meshy 参数 %q 的资产无法发布", parameterName),
			)
		}
		return assetURL, nil
	}
	if options.InlineModelInput == nil {
		return "", providerFailure(
			generation.FailureInvalidInput,
			false,
			fmt.Sprintf("Meshy 参数 %q 需要一个可发布资产", parameterName),
		)
	}
	if a.inputAssets == nil {
		return "", providerFailure(
			generation.FailureInternal,
			false,
			fmt.Sprintf("Meshy 参数 %q 的资产不可读取", parameterName),
		)
	}
	maxBytes := options.InlineModelInput.MaxBytes
	reader, err := a.inputAssets.Open(ctx, asset)
	if err != nil {
		return "", providerFailure(
			generation.FailureInvalidInput,
			false,
			fmt.Sprintf("Meshy 参数 %q 的资产不可读取", parameterName),
		)
	}
	content, readErr := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		return "", providerFailure(
			generation.FailureInvalidInput,
			false,
			fmt.Sprintf("Meshy 参数 %q 的资产不可读取", parameterName),
		)
	}
	if int64(len(content)) > maxBytes {
		return "", providerFailure(
			generation.FailureInvalidInput,
			false,
			fmt.Sprintf(
				"Meshy 参数 %q 的资产超过内联上限 %d 字节",
				parameterName,
				maxBytes,
			),
		)
	}
	mediaType := asset.MediaType
	if !strings.HasPrefix(mediaType, "image/") {
		mediaType = "application/octet-stream"
	}
	if options.InlineModelInput.MediaType != "" {
		mediaType = options.InlineModelInput.MediaType
	}
	return "data:" + mediaType + ";base64," +
		base64.StdEncoding.EncodeToString(content), nil
}

type taskResponse struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Progress  int    `json:"progress"`
	TaskError *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Code    string `json:"code"`
	} `json:"task_error"`
	Raw     map[string]json.RawMessage `json:"-"`
	RawJSON json.RawMessage            `json:"-"`
}

func (response *taskResponse) UnmarshalJSON(data []byte) error {
	response.RawJSON = boundedReportWire(data)
	type wireResponse taskResponse
	if err := json.Unmarshal(data, (*wireResponse)(response)); err != nil {
		return err
	}
	return json.Unmarshal(data, &response.Raw)
}

// observation 按连接声明的 status_mapping 解析上游任务状态；FAILED 状态的
// 错误分类由 error_mapping 的 task_error 证据路径与词表驱动，未声明词汇
// 落入通用安全兜底（Rejected/默认说明）。
func (a *Adapter) observation(
	request generation.ObserveRequest,
	response taskResponse,
) (generation.Observation, error) {
	var options wireOptions
	if err := json.Unmarshal(request.Offering.ProviderOptions, &options); err != nil {
		return generation.Observation{}, providerFailure(generation.FailureInvalidInput, false, "Meshy Offering 的 provider_options 不是合法 JSON")
	}
	if options.TaskType != "" {
		var taskType string
		if !safePathSegment(options.TaskType) || response.ID != request.Job.RemoteID ||
			json.Unmarshal(response.Raw["type"], &taskType) != nil || taskType != options.TaskType {
			return generation.Observation{}, providerFailure(generation.FailureProtocol, false, "Meshy 返回的任务身份或类型不符")
		}
	}
	status := strings.ToLower(strings.TrimSpace(response.Status))
	observation := generation.Observation{
		Job:       &request.Job,
		Progress:  response.Progress,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: fmt.Sprintf(
			"%s:%s:%d",
			request.Job.RemoteID,
			strings.ToUpper(status),
			response.Progress,
		),
	}
	rule, declared := declaredStatusRule(a.statusMapping, status)
	if !declared {
		return generation.Observation{}, providerFailure(
			generation.FailureProtocol,
			false,
			"Meshy 返回未知任务状态",
		)
	}
	switch rule.Phase {
	case "accepted":
		observation.Phase = generation.UpstreamAccepted
	case "running":
		observation.Phase = generation.UpstreamRunning
	case "completed":
		observation.Phase = generation.UpstreamCompleted
		observation.Progress = 100
		claims, err := response.claims(
			request.AttemptID,
			request.Offering,
		)
		if err != nil {
			return generation.Observation{}, err
		}
		if err := captureResultIdentity(request, response, options, claims); err != nil {
			return generation.Observation{}, providerFailure(generation.FailureProtocol, false, "原型结果身份不完整或冲突")
		}
		observation.Artifacts = claims
	case "failed":
		observation.Phase = generation.UpstreamFailed
		payload, err := json.Marshal(response.Raw)
		if err != nil {
			return generation.Observation{}, providerFailure(
				generation.FailureInternal,
				false,
				"Meshy 观察响应无法编码",
			)
		}
		failure := httpjson.ClassifyPayloadWithMapping(
			a.errorMapping,
			payload,
			generation.FailureRejected,
			"Meshy 任务执行失败",
		)
		// Task error messages and codes are upstream-controlled and may echo
		// signed URLs or private task identifiers. SafeDetail stays the fixed
		// public text; the redacted, bounded task_error evidence goes only to
		// the internal forensic fields (persisted and logged, never projected).
		failure.SafeDetail = "Meshy 任务执行失败"
		failure.UpstreamCode, failure.UpstreamExcerpt = taskErrorEvidence(response, generation.InputEchoes(request.Offering, request.Input)...)
		observation.Failure = failure
	case "canceled":
		observation.Phase = generation.UpstreamCanceled
		observation.Failure = &generation.Failure{
			Class:      generation.FailureRejected,
			SafeDetail: "Meshy 任务已取消",
		}
	default:
		return generation.Observation{}, providerFailure(
			generation.FailureProtocol,
			false,
			"Meshy 返回未知任务状态",
		)
	}
	return observation, nil
}

// declaredStatusRule 按小写状态词查连接声明；未声明返回 ok=false。
func declaredStatusRule(
	mapping *generation.StatusMapping,
	status string,
) (generation.StatusRule, bool) {
	if mapping == nil {
		return generation.StatusRule{}, false
	}
	rule, ok := mapping.UpstreamStatus[status]
	return rule, ok
}

func (response taskResponse) claims(
	attemptID generation.AttemptID,
	offering generation.Offering,
) ([]generation.ArtifactClaim, error) {
	var options wireOptions
	if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
		return nil, providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy Offering 的 provider_options 不是合法 JSON",
		)
	}
	if len(options.ResultAssets) == 0 && len(options.ResultReports) == 0 {
		return nil, providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy Offering Schema 缺少 result_assets",
		)
	}
	claims, err := response.reportClaims(attemptID, offering, options)
	if err != nil {
		return nil, err
	}
	for _, projection := range options.ResultAssets {
		if err := validateResultMediaType(offering, projection); err != nil {
			return nil, providerFailure(generation.FailureInvalidInput, false, "Meshy 结果媒体类型声明无效")
		}
		projected, err := projectResultAssets(attemptID, response.Raw, projection)
		if err != nil {
			return nil, err
		}
		claims = append(claims, projected...)
	}
	return claims, nil
}

func projectResultAssets(
	attemptID generation.AttemptID,
	payload map[string]json.RawMessage,
	projection resultAssetProjection,
) ([]generation.ArtifactClaim, error) {
	projection.Path = strings.TrimSpace(projection.Path)
	projection.Role = strings.TrimSpace(projection.Role)
	projection.Mode = strings.TrimSpace(projection.Mode)
	projection.Variant = strings.TrimSpace(projection.Variant)
	if projection.Path == "" || projection.Role == "" || projection.Mode == "" {
		return nil, providerFailure(
			generation.FailureInvalidInput,
			false,
			"Meshy result_assets 项缺少 path、role 或 mode",
		)
	}
	value, present, err := resultValueAtPath(payload, projection.Path)
	if err != nil || !present {
		return nil, err
	}
	switch projection.Mode {
	case "scalar":
		var locator string
		if err := json.Unmarshal(value, &locator); err != nil {
			return nil, invalidResultProjection(projection, err)
		}
		if strings.TrimSpace(locator) == "" {
			return nil, nil
		}
		variant := projection.Variant
		if variant == "" {
			variant = path.Base(projection.Path)
		}
		media := projection.MediaType
		if media == "" {
			media = mediaType(locator, "")
		}
		return []generation.ArtifactClaim{claim(
			attemptID,
			projection.Role,
			variant,
			locator,
			media,
		)}, nil
	case "map":
		var values map[string]json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return nil, invalidResultProjection(projection, err)
		}
		return flattenResultClaims(attemptID, projection.Role, "", values)
	case "array":
		var values []json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return nil, invalidResultProjection(projection, err)
		}
		claims := make([]generation.ArtifactClaim, 0, len(values))
		for index, raw := range values {
			var locator string
			if err := json.Unmarshal(raw, &locator); err != nil {
				return nil, invalidResultProjection(projection, err)
			}
			if strings.TrimSpace(locator) != "" {
				variant := fmt.Sprintf("%d", index+1)
				claims = append(claims, claim(
					attemptID,
					projection.Role,
					variant,
					locator,
					mediaType(locator, ""),
				))
			}
		}
		return claims, nil
	case "array-map":
		var values []map[string]json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return nil, invalidResultProjection(projection, err)
		}
		claims := make([]generation.ArtifactClaim, 0)
		for index, nested := range values {
			prefix := fmt.Sprintf("%d", index+1)
			if projection.Variant != "" {
				prefix = projection.Variant + prefix
			}
			projected, err := flattenResultClaims(
				attemptID,
				projection.Role,
				prefix,
				nested,
			)
			if err != nil {
				return nil, err
			}
			claims = append(claims, projected...)
		}
		return claims, nil
	case "tree":
		var values map[string]json.RawMessage
		if err := json.Unmarshal(value, &values); err != nil {
			return nil, invalidResultProjection(projection, err)
		}
		return flattenResultClaims(attemptID, projection.Role, "", values)
	default:
		return nil, providerFailure(
			generation.FailureInvalidInput,
			false,
			fmt.Sprintf("Meshy result_assets mode %q 不受支持", projection.Mode),
		)
	}
}

func resultValueAtPath(
	payload map[string]json.RawMessage,
	fieldPath string,
) (json.RawMessage, bool, error) {
	segments := strings.Split(fieldPath, ".")
	current := payload
	for index, segment := range segments {
		value, present := current[segment]
		if !present {
			return nil, false, nil
		}
		if index == len(segments)-1 {
			return value, true, nil
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(value, &nested); err != nil {
			return nil, false, providerFailure(
				generation.FailureProtocol,
				false,
				fmt.Sprintf("Meshy 结果路径 %q 不是对象", fieldPath),
			)
		}
		current = nested
	}
	return nil, false, nil
}

func invalidResultProjection(
	projection resultAssetProjection,
	err error,
) error {
	return providerFailure(
		generation.FailureProtocol,
		false,
		fmt.Sprintf(
			"Meshy 结果路径 %q 不符合 mode %q：%v",
			projection.Path,
			projection.Mode,
			err,
		),
	)
}

func flattenResultClaims(
	attemptID generation.AttemptID,
	role string,
	prefix string,
	values map[string]json.RawMessage,
) ([]generation.ArtifactClaim, error) {
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	claims := make([]generation.ArtifactClaim, 0)
	for _, name := range names {
		if !validResultKey(name) {
			return nil, providerFailure(
				generation.FailureProtocol,
				false,
				"Meshy 结果包含无效资产字段名",
			)
		}
		variant := name
		if prefix != "" {
			variant = prefix + "." + name
		}
		var locator string
		if err := json.Unmarshal(values[name], &locator); err == nil {
			if strings.TrimSpace(locator) != "" {
				claims = append(claims, claim(
					attemptID,
					role,
					variant,
					locator,
					mediaType(locator, ""),
				))
			}
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(values[name], &nested); err != nil {
			return nil, providerFailure(
				generation.FailureProtocol,
				false,
				"Meshy 结果包含无法解析的资产字段",
			)
		}
		nestedClaims, err := flattenResultClaims(
			attemptID,
			role,
			variant,
			nested,
		)
		if err != nil {
			return nil, err
		}
		claims = append(claims, nestedClaims...)
	}
	return claims, nil
}

func validResultKey(name string) bool {
	if name == "" || len(name) > 96 {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') ||
			(char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

func claim(
	attemptID generation.AttemptID,
	role string,
	suffix string,
	locator string,
	media string,
) generation.ArtifactClaim {
	return generation.ArtifactClaim{
		ID:        fmt.Sprintf("%s:%s:%s", attemptID, role, suffix),
		Role:      role,
		Variant:   suffix,
		Locator:   locator,
		MediaType: media,
	}
}

func baseMediaType(value string) string {
	return strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
}

func mediaType(locator string, format string) string {
	extension := strings.ToLower(path.Ext(strings.Split(locator, "?")[0]))
	if extension == "" && format != "" {
		extension = "." + strings.ToLower(format)
	}
	switch extension {
	case ".glb":
		return "model/gltf-binary"
	case ".gltf":
		return "model/gltf+json"
	case ".fbx":
		return "application/octet-stream"
	case ".obj":
		return "text/plain"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	default:
		return mime.TypeByExtension(extension)
	}
}

func providerFailure(
	class generation.FailureClass,
	retryable bool,
	detail string,
) error {
	return &generation.ProviderError{Failure: &generation.Failure{
		Class:      class,
		Retryable:  retryable,
		SafeDetail: detail,
	}}
}

// expiredSourceFailure names a create refused with 404 while it referred to an
// earlier Meshy job (input_task_id, rig_task_id, preview_task_id): Meshy
// documents that 404 as the referenced task not being found among the key's
// tasks, which is what an API task past its retention (at most three days)
// answers (#325). The same input fails again, so it is a definite,
// non-retryable source_expired failure; the upstream evidence is kept.
func expiredSourceFailure(request generation.StartRequest, err error) error {
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil || provider.Failure.UpstreamStatus != http.StatusNotFound {
		return err
	}
	referenced := false
	for _, dependency := range request.TaskDependencies {
		referenced = referenced || strings.TrimSpace(dependency.UpstreamJob.RemoteID) != ""
	}
	if !referenced {
		return err
	}
	failure := *provider.Failure
	failure.Class, failure.Retryable, failure.Ambiguous = generation.FailureSourceExpired, false, false
	failure.RetryAfter = 0
	failure.SafeDetail = "Meshy 已不保留来源模型的原任务（上游返回 404 Task not found）"
	return &generation.ProviderError{Failure: &failure, Cause: provider.Cause}
}

func submissionUnknown(detail string) error {
	return &generation.ProviderError{Failure: &generation.Failure{
		Class:      generation.FailureSubmissionUnknown,
		Ambiguous:  true,
		SafeDetail: detail,
	}}
}

var _ generation.ProviderPort = (*Adapter)(nil)
