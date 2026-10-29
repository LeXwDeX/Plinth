package tripo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/uploadname"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

type Config struct {
	BaseURL        string
	APIKey         string
	AuthHeader     string
	AuthPrefix     string
	RequestTimeout time.Duration
	ErrorMapping   *generation.ErrorMapping
	StatusMapping  *generation.StatusMapping
	HTTPClient     *http.Client
	// NativeSourceTransport decorates the anonymous native-source client
	// after the secured client is built: pinned public IP dialing, TLS
	// verification, no proxy and no redirects stay in place.
	NativeSourceTransport func(http.RoundTripper) http.RoundTripper
	AllowHTTPAssets       bool
	InputAssets           generation.InputAssetResolver
	OfferingRoutes        []generation.Offering
	// ScratchDir holds the private copies of uploads (the checked GLB and the
	// spooled multipart body). Empty means os.TempDir(); production passes
	// the storage volume's tmp directory, because the container's /tmp is a
	// 64 MB tmpfs and a model may be 150 MB (#321 A).
	ScratchDir string
}

type Adapter struct {
	client             *httpjson.Client
	allowHTTPAssets    bool
	inputAssets        generation.InputAssetResolver
	offeringRoutes     map[generation.OfferingID]generation.Offering
	errorMapping       *generation.ErrorMapping
	statusMapping      *generation.StatusMapping
	nativeSourceClient *http.Client
	scratchDir         string
	// unknownPolls counts consecutive polls per remote task that returned a
	// status word with a declared max_consecutive bound (see observation).
	// It is process-local on purpose: the bound only has to be finite, and a
	// restart merely grants a fresh allowance.
	unknownMu    sync.Mutex
	unknownPolls map[string]int
}

type providerOptions struct {
	Protocol         string                     `json:"protocol,omitempty"`
	Model            string                     `json:"model"`
	Endpoint         string                     `json:"endpoint"`
	ObserveEndpoint  string                     `json:"observe_endpoint"`
	UploadEndpoint   string                     `json:"upload_endpoint"`
	ConvertEndpoint  string                     `json:"convert_endpoint"`
	ResponseAliases  []generation.ResponseAlias `json:"response_aliases,omitempty"`
	NativeConversion *nativeConversionOptions   `json:"native_conversion,omitempty"`
	SourceRig        *nativeSourceRig           `json:"source_rig,omitempty"`
	Transform        []float64                  `json:"transform,omitempty"`
}

func New(config Config) (*Adapter, error) {
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:        config.BaseURL,
		APIKey:         config.APIKey,
		AuthHeader:     config.AuthHeader,
		AuthPrefix:     config.AuthPrefix,
		RequestTimeout: config.RequestTimeout,
		HTTPClient:     config.HTTPClient,
		ClassifyError:  classifyErrorWithMapping(config.ErrorMapping),
		SpoolDir:       config.ScratchDir,
	})
	if err != nil {
		return nil, fmt.Errorf("tripo: %w", err)
	}
	routes := make(map[generation.OfferingID]generation.Offering)
	for _, offering := range config.OfferingRoutes {
		offering.ProviderOptions = append(json.RawMessage(nil), offering.ProviderOptions...)
		routes[offering.ID] = offering
	}
	nativeSourceClient := newNativeSourceClient(client.HTTPClient.Timeout)
	if config.NativeSourceTransport != nil {
		transport := config.NativeSourceTransport(nativeSourceClient.Transport)
		if transport == nil {
			return nil, errors.New("tripo: native source transport decorator returned nil")
		}
		nativeSourceClient.Transport = transport
	}
	return &Adapter{
		client:             client,
		allowHTTPAssets:    config.AllowHTTPAssets,
		inputAssets:        config.InputAssets,
		offeringRoutes:     routes,
		errorMapping:       config.ErrorMapping,
		statusMapping:      config.StatusMapping,
		nativeSourceClient: nativeSourceClient,
		scratchDir:         strings.TrimSpace(config.ScratchDir),
	}, nil
}

// Old task snapshots predate explicit routes. Only missing keys may use the
// current Product Schema; a pinned route (including an invalid empty one) wins.
func (a *Adapter) optionsFor(offering generation.Offering) (providerOptions, error) {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(offering.ProviderOptions, &values); err != nil {
		return providerOptions{}, err
	}
	if values == nil {
		values = make(map[string]json.RawMessage)
	}
	declared, exists := a.offeringRoutes[offering.ID]
	if exists && offering.AdapterKey == declared.AdapterKey &&
		offering.ProviderID == declared.ProviderID && offering.Capability == declared.Capability {
		var current map[string]json.RawMessage
		if err := json.Unmarshal(declared.ProviderOptions, &current); err != nil {
			return providerOptions{}, err
		}
		for _, key := range []string{"endpoint", "observe_endpoint", "upload_endpoint", "convert_endpoint", "response_aliases"} {
			if _, pinned := values[key]; !pinned {
				if value, present := current[key]; present {
					values[key] = value
				}
			}
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return providerOptions{}, err
	}
	var options providerOptions
	err = json.Unmarshal(encoded, &options)
	return options, err
}

func (a *Adapter) Start(
	ctx context.Context,
	request generation.StartRequest,
) (generation.Observation, error) {
	if smartSegmentProtocol(request.Offering) {
		return a.startSmartSegment(ctx, request)
	}
	if request.Offering.Capability == schema.ImageToSplat {
		return a.startSplat(ctx, request)
	}
	if multiviewCapability(request.Offering.Capability) {
		return a.startMultiview(ctx, request)
	}
	if nativeProcessingCapability(request.Offering.Capability) {
		return a.startNativeProcessing(ctx, request)
	}
	if request.Offering.Capability == schema.RigCheck {
		return a.startRigCheck(ctx, request)
	}
	if request.Offering.Capability == generation.FormatConversion && hasUploadedConversionSource(request.Offering) {
		return a.startUploadedConversion(ctx, request)
	}
	if request.Offering.Capability == generation.FormatConversion {
		// W5 格式转换：私有规划器 + W4 检查点发送（conversion.go）。
		return a.startConversion(ctx, request)
	}
	if request.Offering.Capability != generation.TextTo3D &&
		request.Offering.Capability != generation.ImageTo3D {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			"Tripo 适配器仅支持文生 3D、图生 3D 与格式转换",
		)
	}
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return generation.Observation{}, failure(
			generation.FailureInvalidInput,
			false,
			"规范化输入不是 JSON 对象",
		)
	}
	if request.Offering.Cost != nil || request.Offering.NativePricing != nil {
		if _, err := catalog.AuthorizeExtraCredits(request.Offering, request.Input); err != nil {
			return generation.Observation{}, failure(generation.FailureRejected, false, err.Error())
		}
	}
	if preCompletionFormats(request.Offering) {
		if _, err := validatePreCompletionInput(request.Offering, request.Input); err != nil {
			return generation.Observation{}, err
		}
	}
	options, err := a.optionsFor(request.Offering)
	if err != nil ||
		options.Model == "" || strings.TrimSpace(options.Endpoint) == "" {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo Offering 缺少 model 或 endpoint",
		)
	}
	body := make(map[string]any, len(input)+1)
	body["model"] = options.Model
	for name, value := range input {
		if name == "reference_assets" || name == request.Offering.ExtraConsentParameter() {
			continue
		}
		// Format selection and consent are private platform decisions. Their
		// authorized conversion jobs run only after the base model succeeds.
		if name == request.Offering.PriceSelectionParameter() {
			continue
		}
		body[name] = value
	}
	if request.Offering.Capability == generation.ImageTo3D {
		assets := request.InputAssets["reference_assets"]
		if a.inputAssets == nil || len(assets) != 1 {
			return generation.Observation{}, failure(
				generation.FailureInvalidInput,
				false,
				"Tripo 图生 3D 需要且只允许一个已解析参考图",
			)
		}
		token, err := a.upload(ctx, request, assets[0])
		if err != nil {
			return generation.Observation{}, err
		}
		// v3 统一用 input 字段承载图生 3D 的参考图 token
		body["input"] = token
	}
	// 非幂等付费创建提交前先持久化私有派发意图并取得发送许可（W4 §4.5）：
	// 未安装检查点回调时保持既有直连行为；ErrDispatchPending 等失败必须
	// 放弃发送（0 次上游调用）。intent 自含协调标识，不依赖进程内存。
	dispatchPermit, err := privateDispatchPermit(ctx, request.SubmissionKey, dispatchIntentStage{
		Stage:   "task.create",
		Details: map[string]any{"endpoint": options.Endpoint, "model": options.Model},
	})
	if err != nil {
		return generation.Observation{}, err
	}
	var response envelope[struct {
		TaskID string `json:"task_id"`
	}]
	if err := a.client.DoJSON(
		ctx,
		http.MethodPost,
		options.Endpoint,
		body,
		request.SubmissionKey,
		&response,
		true,
	); err != nil {
		return generation.Observation{}, err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return generation.Observation{}, err
	}
	if response.Data.TaskID == "" {
		return generation.Observation{}, submissionUnknown("Tripo 创建响应缺少 task_id")
	}
	job := &generation.UpstreamJob{
		ProviderID: request.Offering.ProviderID,
		RemoteID:   response.Data.TaskID,
	}
	return generation.Observation{
		Phase:     generation.UpstreamAccepted,
		Job:       job,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: privateDedupeKey(response.Data.TaskID, "accepted"),
		Dispatch:  dispatchPermit,
	}, nil
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
		return nil, failure(generation.FailureInternal, false, fmt.Sprintf("编码私有派发意图失败: %v", err))
	}
	return generation.PrivateDispatchPermit(ctx, generation.DispatchIntent{Bytes: encoded})
}

func (a *Adapter) Observe(
	ctx context.Context,
	request generation.ObserveRequest,
) (generation.Observation, error) {
	if smartSegmentProtocol(request.Offering) {
		return a.observeSmartSegment(ctx, request)
	}
	if request.Offering.Capability == schema.ImageToSplat {
		return a.observeSplat(ctx, request)
	}
	if multiviewCapability(request.Offering.Capability) {
		return a.observeMultiview(ctx, request)
	}
	if nativeProcessingCapability(request.Offering.Capability) {
		return a.observeNativeProcessing(ctx, request)
	}
	if request.Offering.Capability == schema.RigCheck {
		return a.observeRigCheck(ctx, request)
	}
	if request.Offering.Capability == generation.FormatConversion && hasUploadedConversionSource(request.Offering) {
		return a.observeUploadedConversion(ctx, request)
	}
	if request.PostSuccess {
		// W6 成功后观察：推进已同意的私有格式转换（每轮至多一次付费
		// 派发；检查点栅栏仍然生效），父任务终态语义由内核约束。
		return a.observePostSuccessConversion(ctx, request)
	}
	if preCompletionFormats(request.Offering) {
		return a.observePreCompletionFormats(ctx, request)
	}
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.Count(options.ObserveEndpoint, "{task_id}") != 1 {
		return generation.Observation{}, failure(generation.FailureProtocol, false, "Tripo Offering 缺少 observe_endpoint")
	}
	task, err := a.observeTask(ctx, options, request.Job.RemoteID, string(request.AttemptID))
	if err != nil {
		return generation.Observation{}, err
	}
	return a.observation(request, task)
}

// observeTask 按声明的 observe_endpoint 查询单个上游任务（成功后观察
// 复用同一查询读取转换子作业状态）。
func (a *Adapter) observeTask(
	ctx context.Context,
	options providerOptions,
	remoteID string,
	cacheKey string,
) (task, error) {
	var response envelope[task]
	if err := a.client.DoJSON(
		ctx,
		http.MethodGet,
		strings.ReplaceAll(options.ObserveEndpoint, "{task_id}", url.PathEscape(remoteID)),
		nil,
		cacheKey,
		&response,
		false,
	); err != nil {
		return task{}, err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return task{}, err
	}
	return response.Data, nil
}

func (a *Adapter) OpenAsset(
	ctx context.Context,
	request generation.OpenAssetRequest,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if multiviewCapability(request.Offering.Capability) {
		return a.openMultiviewAsset(ctx, request)
	}
	conversion, err := nativeSourceMaterializationOptions(request.Offering, request.Claim)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	if conversion != nil {
		return a.openNativeMaterializationSource(ctx, request.Offering, request.Claim, conversion)
	}
	return httpjson.OpenClaim(
		ctx,
		a.client.HTTPClient,
		request.Claim,
		request.Offering.AssetHosts,
		a.allowHTTPAssets,
	)
}

func (a *Adapter) upload(
	ctx context.Context,
	request generation.StartRequest,
	asset generation.InputAsset,
) (string, error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.TrimSpace(options.UploadEndpoint) == "" {
		return "", failure(generation.FailureProtocol, false, "Tripo Offering 缺少 upload_endpoint")
	}
	filename, err := imageUploadFilename(asset.MediaType)
	if err != nil {
		return "", err
	}
	body, err := a.inputAssets.Open(ctx, asset)
	if err != nil {
		return "", failure(generation.FailureInvalidInput, false, "Tripo 参考图不可读取")
	}
	token, err := a.uploadReader(ctx, request, asset.MediaType, filename, body)
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", failure(generation.FailureProtocol, false, "Tripo 上传响应缺少 file_token")
	}
	return token, nil
}

// uploadReader uses the same documented multipart file wire for image/model inputs.
// The caller owns source authorization and any profile/byte verification.
func (a *Adapter) uploadReader(ctx context.Context, request generation.StartRequest, mediaType, filename string, body io.ReadCloser) (string, error) {
	options, err := a.optionsFor(request.Offering)
	if err != nil || strings.TrimSpace(options.UploadEndpoint) == "" {
		body.Close()
		return "", failure(generation.FailureProtocol, false, "Tripo Offering 缺少 upload_endpoint")
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	form := multipart.NewWriter(writer)
	contentType := form.FormDataContentType()
	go func() {
		header := make(textproto.MIMEHeader)
		header.Set(
			"Content-Disposition",
			`form-data; name="file"; filename="`+filename+`"`,
		)
		header.Set("Content-Type", mediaType)
		part, writeErr := form.CreatePart(header)
		if writeErr == nil {
			_, writeErr = io.Copy(part, body)
		}
		closeBodyErr := body.Close()
		closeFormErr := form.Close()
		if writeErr == nil {
			writeErr = closeBodyErr
		}
		if writeErr == nil {
			writeErr = closeFormErr
		}
		_ = writer.CloseWithError(writeErr)
	}()
	var response envelope[struct {
		FileToken string `json:"file_token"`
	}]
	if err := a.client.Do(
		ctx,
		http.MethodPost,
		options.UploadEndpoint,
		reader,
		contentType,
		request.SubmissionKey+":upload",
		&response,
		true,
	); err != nil {
		return "", err
	}
	if err := businessEnvelopeError(a.errorMapping, response.Code, response.Message); err != nil {
		return "", err
	}
	return response.Data.FileToken, nil
}

type envelope[T any] struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// classifyErrorWithMapping 按连接级 error_mapping 声明的错误码词表对
// HTTP 非 2xx 响应做精确分类；未声明映射（历史直连构造）或码不在词表
// 时返回 nil，回退到 httpjson 默认 HTTP 状态码分类。分类算法是 Tripo
// 信封解析，词表真源在 Product Schema 连接声明中。
func classifyErrorWithMapping(mapping *generation.ErrorMapping) httpjson.HTTPErrorClassifier {
	return func(response *http.Response, payload []byte) error {
		var body envelope[json.RawMessage]
		_ = json.Unmarshal(payload, &body)
		code := ""
		if body.Code != 0 {
			code = strconv.Itoa(body.Code)
		}
		class, retryable := tripoHTTPFailure(response.StatusCode)
		if mapping != nil {
			if rule, ok := mapping.UpstreamCodeMap[code]; code != "" && ok {
				class, retryable = generation.FailureClass(rule.Class), rule.Retryable
			}
		}
		retryHeader := "Retry-After"
		if mapping != nil && strings.TrimSpace(mapping.RetryAfterHeader) != "" {
			retryHeader = mapping.RetryAfterHeader
		}
		retryAfterValue := time.Duration(0)
		if retryable {
			retryAfterValue = retryAfter(response.Header.Get(retryHeader))
		}
		detail := fmt.Sprintf("Tripo 返回 HTTP %d", response.StatusCode)
		if code != "" {
			detail = "Tripo 返回错误 code=" + code
		}
		failure := &generation.Failure{
			Class:        class,
			Retryable:    retryable,
			SafeDetail:   detail,
			RetryAfter:   retryAfterValue,
			UpstreamCode: code,
		}
		httpjson.AttachUpstreamEvidence(failure, response.StatusCode, payload)
		return &generation.ProviderError{Failure: failure}
	}
}

func tripoHTTPFailure(status int) (generation.FailureClass, bool) {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return generation.FailureInvalidInput, false
	case http.StatusUnauthorized, http.StatusForbidden:
		return generation.FailureAuthentication, false
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return generation.FailureTimedOut, true
	case http.StatusTooManyRequests:
		return generation.FailureRateLimited, true
	default:
		if status >= 500 {
			return generation.FailureUpstreamUnavailable, true
		}
		return generation.FailureRejected, false
	}
}

// retryAfter 解析 Retry-After 响应头（秒数或 HTTP 时间）。
func retryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsed, err := http.ParseTime(value); err == nil {
		if delay := time.Until(parsed); delay > 0 {
			return delay
		}
	}
	return 0
}

// businessEnvelopeError 按连接级 error_mapping 词表分类业务信封错误；
// code 不在声明词表中时保守回退 Rejected（v3 已用非 2xx 承载全部错误，
// 此路径仅作防御性回退）。
func businessEnvelopeError(
	mapping *generation.ErrorMapping,
	code int,
	_ string,
) error {
	if code == 0 {
		return nil
	}
	class := generation.FailureRejected
	retryable := false
	if mapping != nil {
		if rule, ok := mapping.UpstreamCodeMap[strconv.Itoa(code)]; ok {
			class = generation.FailureClass(rule.Class)
			retryable = rule.Retryable
		}
	}
	// The upstream message may echo signed asset URLs, file tokens, or task IDs.
	// It is never safe to put that text in a public task failure.
	return failure(class, retryable, fmt.Sprintf("Tripo 返回业务错误 code=%d", code))
}

type task struct {
	Type     string         `json:"type,omitempty"`
	TaskID   string         `json:"task_id"`
	Status   string         `json:"status"`
	Progress int            `json:"progress"`
	Output   map[string]any `json:"output"`
	// ErrorCode / ErrorMessage describe a failed task. The official Tripo
	// task object carries error_code (an integer) and error_message (the
	// official Python SDK reads the same pair as error_code / error_msg).
	// Both are upstream-controlled: they only feed the internal forensic
	// fields and the Schema code map, never SafeDetail.
	ErrorCode    json.RawMessage `json:"error_code,omitempty"`
	ErrorMessage string          `json:"error_message,omitempty"`
	ErrorMsg     string          `json:"error_msg,omitempty"`
	// CreditsConsumed 保留上游 v3 任务响应的 credits_consumed 原始
	// 数值（官方 Go SDK types.go：十进制最多两位，VIP 折扣可为小数），
	// 作为转换任务的计费证据写入 Provider 私有 Opaque，不截断。
	CreditsConsumed json.RawMessage `json:"credits_consumed,omitempty"`
}

// observation 按连接级 status_mapping 词表解析上游任务状态；未声明的
// 状态词按未知状态协议错误处理（与历史行为一致）。
func (a *Adapter) observation(
	request generation.ObserveRequest,
	value task,
) (generation.Observation, error) {
	status := strings.ToLower(strings.TrimSpace(value.Status))
	observation := generation.Observation{
		Job:       &request.Job,
		Progress:  value.Progress,
		PollAfter: request.Offering.PollInterval,
		DedupeKey: privateDedupeKey(request.Job.RemoteID, status, strconv.Itoa(value.Progress)),
	}
	rule, declared := declaredStatusRule(a.statusMapping, status)
	if !declared {
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo 返回未知任务状态",
		)
	}
	// A status word with a declared max_consecutive (Tripo's "unknown") keeps
	// the task in its declared phase, but only for that many polls in a row;
	// the next poll ends the attempt with the declared failure class.
	if a.exceedsConsecutiveBound(request.Job.RemoteID, rule) {
		observation.Phase = generation.UpstreamFailed
		// The coordinator drops an observation whose key equals the last
		// recorded one, so the terminal transition needs a key of its own.
		observation.DedupeKey = privateDedupeKey(request.Job.RemoteID, status, "poll-limit-exceeded")
		observation.Failure = &generation.Failure{
			Class:      generation.FailureClass(rule.Class),
			Retryable:  rule.Retryable,
			SafeDetail: "Tripo 任务状态为 " + status + "，超过轮询上限",
		}
		return observation, nil
	}
	switch rule.Phase {
	case "accepted":
		observation.Phase = generation.UpstreamAccepted
	case "running":
		observation.Phase = generation.UpstreamRunning
	case "completed":
		observation.Phase = generation.UpstreamCompleted
		observation.Progress = 100
		options, err := a.optionsFor(request.Offering)
		if err != nil {
			return generation.Observation{}, failure(
				generation.FailureProtocol,
				false,
				"Tripo Offering 的响应别名声明无效",
			)
		}
		observation.Artifacts = claims(value, request.AttemptID, options.ResponseAliases)
	case "canceled":
		observation.Phase = generation.UpstreamCanceled
		observation.Failure = &generation.Failure{
			Class:      generation.FailureRejected,
			SafeDetail: "Tripo 任务已取消",
		}
	case "failed":
		observation.Phase = generation.UpstreamFailed
		class := generation.FailureRejected
		if rule.Class != "" {
			class = generation.FailureClass(rule.Class)
		}
		code := taskErrorCode(value.ErrorCode)
		// The task's own error_code refines the class through the
		// connection's upstream_code_map, unless the status word itself
		// declares one (banned, expired). Retryability stays with the status
		// rule: a failed task is not retried because its code looks transient.
		if mapped, known := a.mappedTaskErrorClass(code); known && rule.Class == "" {
			class = mapped
		}
		observation.Failure = &generation.Failure{
			Class:      class,
			Retryable:  rule.Retryable,
			SafeDetail: "Tripo 任务状态为 " + status,
		}
		observation.Failure.UpstreamCode = code
		observation.Failure.UpstreamExcerpt = taskErrorExcerpt(request, code, value)
	default:
		return generation.Observation{}, failure(
			generation.FailureProtocol,
			false,
			"Tripo 返回未知任务状态",
		)
	}
	// 转换任务在所有相位持续携带合并后的私有证据（每次观察都会整体
	// 覆盖 attempt.Opaque，必须从上一份证据滚动合并，避免丢失 Start
	// 阶段写入的源任务/格式信息）；生成能力行为不变（Opaque 保持空）。
	if request.Offering.Capability == generation.FormatConversion {
		observation.Opaque = mergeConversionEvidence(request.Opaque, value)
	}
	return observation, nil
}

// exceedsConsecutiveBound counts consecutive polls of a status word whose rule
// declares max_consecutive and reports whether the bound is now exceeded.
// Any other status word resets the count for that remote task.
func (a *Adapter) exceedsConsecutiveBound(remoteID string, rule generation.StatusRule) bool {
	a.unknownMu.Lock()
	defer a.unknownMu.Unlock()
	if rule.MaxConsecutive <= 0 {
		delete(a.unknownPolls, remoteID)
		return false
	}
	if a.unknownPolls == nil {
		a.unknownPolls = map[string]int{}
	}
	a.unknownPolls[remoteID]++
	if a.unknownPolls[remoteID] > rule.MaxConsecutive {
		delete(a.unknownPolls, remoteID)
		return true
	}
	return false
}

// taskErrorCode returns a failed task's error_code as a plain decimal string
// (the field is an integer upstream; a quoted number is tolerated). Anything
// else is dropped rather than persisted.
func taskErrorCode(raw json.RawMessage) string {
	text := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if text == "" || len(text) > 8 {
		return ""
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return text
}

func (a *Adapter) mappedTaskErrorClass(code string) (generation.FailureClass, bool) {
	if code == "" || a.errorMapping == nil {
		return "", false
	}
	rule, ok := a.errorMapping.UpstreamCodeMap[code]
	if !ok || rule.Class == "" {
		return "", false
	}
	return generation.FailureClass(rule.Class), true
}

// taskErrorExcerpt builds the internal-only evidence for a failed task. The
// message is upstream-controlled (it can echo the prompt, signed URLs or keys),
// so it goes through generation.UpstreamExcerpt with the frozen input's
// free-text values as echoes.
func taskErrorExcerpt(request generation.ObserveRequest, code string, value task) string {
	message := strings.TrimSpace(value.ErrorMessage)
	if message == "" {
		message = strings.TrimSpace(value.ErrorMsg)
	}
	parts := make([]string, 0, 2)
	if code != "" {
		parts = append(parts, "code="+code)
	}
	if message != "" {
		parts = append(parts, "message="+message)
	}
	return generation.UpstreamExcerpt(
		strings.Join(parts, " "),
		generation.UpstreamExcerptRunes,
		generation.InputEchoes(request.Offering, request.Input)...,
	)
}

func privateDedupeKey(parts ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(digest[:8])
}

// mergeConversionEvidence 合并既有证据与本次响应的 credits_consumed
// （Start 阶段写入源任务/格式，观察阶段只补计费证据；JSON 解析失败时
// 保留既有证据）。
func mergeConversionEvidence(
	previous json.RawMessage,
	value task,
) json.RawMessage {
	evidence := conversionEvidence{}
	if len(previous) > 0 {
		_ = json.Unmarshal(previous, &evidence)
	}
	if len(value.CreditsConsumed) > 0 {
		evidence.ConsumedCredit = append(json.RawMessage(nil), value.CreditsConsumed...)
	}
	if evidence.SourceRemoteID == "" && evidence.Format == "" && evidence.ConsumedCredit == nil {
		return append(json.RawMessage(nil), previous...)
	}
	encoded, err := json.Marshal(evidence)
	if err != nil {
		return append(json.RawMessage(nil), previous...)
	}
	return encoded
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

// defaultResponseAliases 是 v2（model/pbr_model/base_model）与 v3
// （model_url/...）两种响应字段名的协议兼容回退；Offering 声明
// response_aliases 时以声明为准（冻结历史路由优先）。
func defaultResponseAliases() []generation.ResponseAlias {
	return []generation.ResponseAlias{
		{Key: "model_url", ClaimID: "model:model_url", Role: "model", Fields: []string{"model_url", "model"}},
		{Key: "pbr_model_url", ClaimID: "model:pbr_model_url", Role: "model", Fields: []string{"pbr_model_url", "pbr_model"}},
		{Key: "base_model_url", ClaimID: "model:base_model_url", Role: "model", Fields: []string{"base_model_url", "base_model"}},
		{Key: "rendered_image_url", ClaimID: "thumbnail", Role: "thumbnail", Fields: []string{"rendered_image_url", "rendered_image"}},
	}
}

func claims(
	value task,
	attemptID generation.AttemptID,
	aliases []generation.ResponseAlias,
) []generation.ArtifactClaim {
	if len(aliases) == 0 {
		aliases = defaultResponseAliases()
	}
	claims := make([]generation.ArtifactClaim, 0, len(aliases))
	seen := map[string]struct{}{}
	for _, alias := range aliases {
		locator := firstHTTPOutput(value.Output, alias.Fields...)
		if locator == "" {
			continue
		}
		if _, duplicate := seen[locator]; duplicate {
			continue
		}
		seen[locator] = struct{}{}
		claimID := alias.ClaimID
		if claimID == "" {
			claimID = alias.Key
		}
		claim := generation.ArtifactClaim{
			ID:      fmt.Sprintf("%s:%s", attemptID, claimID),
			Locator: locator,
			Variant: alias.Variant,
		}
		if alias.Role == "model" {
			claim.Role = "model"
			claim.MediaType = modelMediaType(locator)
		} else {
			claim.Role = alias.Role
			claim.MediaType = imageMediaType(locator)
		}
		claims = append(claims, claim)
	}
	return claims
}

// firstHTTPOutput 按顺序返回第一个 HTTP URL 值。
func firstHTTPOutput(output map[string]any, names ...string) string {
	for _, name := range names {
		if locator, _ := output[name].(string); isHTTP(locator) {
			return locator
		}
	}
	return ""
}

// tripoUploadImageExtensions are the picture formats Tripo's file upload takes.
var tripoUploadImageExtensions = []string{".png", ".jpg", ".jpeg", ".webp"}

// imageUploadFilename names an uploaded picture from its verified media type.
// Callers derive it before any dispatch permit, so an unsupported type is
// refused with its reason instead of travelling under a guessed extension.
func imageUploadFilename(mediaType string) (string, error) {
	extension, err := uploadname.Extension(mediaType, tripoUploadImageExtensions)
	if err != nil {
		return "", failure(generation.FailureInvalidInput, false, "Tripo 上传图片格式无效："+err.Error())
	}
	return "input" + extension, nil
}

func modelMediaType(locator string) string {
	switch strings.ToLower(path.Ext(strings.Split(locator, "?")[0])) {
	case ".glb":
		return "model/gltf-binary"
	case ".gltf":
		return "model/gltf+json"
	default:
		return "application/octet-stream"
	}
}

func imageMediaType(locator string) string {
	mediaType := mime.TypeByExtension(path.Ext(strings.Split(locator, "?")[0]))
	if mediaType == "" {
		return "image/webp"
	}
	return mediaType
}

func isHTTP(value string) bool {
	return strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://")
}

func failure(
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

func submissionUnknown(detail string) error {
	return &generation.ProviderError{Failure: &generation.Failure{
		Class:      generation.FailureSubmissionUnknown,
		Ambiguous:  true,
		SafeDetail: detail,
	}}
}

var _ generation.ProviderPort = (*Adapter)(nil)
