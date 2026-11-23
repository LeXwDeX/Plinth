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
	"github.com/LeXwDeX/Plinth/internal/providers/upstreampool"
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
	// Clock is the wall clock the duration bounds read (a status word a
	// connection bounds by time, #460). Empty means time.Now; tests inject one
	// so a five-minute bound is a test step rather than five minutes of waiting.
	Clock func() time.Time
	// RetrySubmitServerError is the connection-level declaration that a 5xx
	// answer to a create may be retried (#460 C25).
	RetrySubmitServerError bool
	// Pool is the optional process-wide upstream concurrency pool (#468). Nil
	// disables queueing and keeps the previous behavior.
	Pool *upstreampool.Manager
	// PoolID names the pool an Offering queues through when its own
	// declaration names none (#487); empty disables queueing for those.
	PoolID string
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
	clock              func() time.Time
	pool               *upstreampool.Manager
	poolID             string
	// poolIDs is the upstream pool each routed Offering declared (#487),
	// resolved once at build time so the dispatch paths never parse provider
	// options to find their queue.
	poolIDs map[generation.OfferingID]string
	// unknownPolls counts consecutive polls per remote task that returned a
	// status word with a declared max_consecutive bound (see observation).
	// unknownSince is when the current run of a status word with a declared
	// max_duration_seconds bound began (see unknownStatusExpired). Both are
	// process-local on purpose: each bound only has to be finite, and a restart
	// merely grants a fresh allowance.
	unknownMu    sync.Mutex
	unknownPolls map[string]int
	unknownSince map[string]time.Time
}

type providerOptions struct {
	Protocol        string `json:"protocol,omitempty"`
	Model           string `json:"model"`
	Endpoint        string `json:"endpoint"`
	ObserveEndpoint string `json:"observe_endpoint"`
	UploadEndpoint  string `json:"upload_endpoint"`
	ConvertEndpoint string `json:"convert_endpoint"`
	// Pool names the upstream concurrency pool this Offering's dispatches draw
	// slots from (#487). Upstream pools are per task category, so one
	// connection registers several and the Offering says which one it belongs
	// to; an Offering that names none (or one the connection does not
	// register) is never queued.
	Pool string `json:"pool,omitempty"`
	// FormatConversion declares how an Offering's private format conversions
	// run: how many children may be in flight at once.
	FormatConversion *formatConversionOptions   `json:"format_conversion,omitempty"`
	ResponseAliases  []generation.ResponseAlias `json:"response_aliases,omitempty"`
	// ResponseMetadata names task output fields the Offering wants preserved as
	// asset metadata on the model claim rather than dropped: a model conversion
	// reports the origin move it made as output.normalize_scale and
	// output.normalize_translation, and without them a later step cannot tell
	// the delivered model's coordinates back to the source's. The names come
	// from this declaration; the Adapter hardcodes no upstream field.
	ResponseMetadata []string                 `json:"response_metadata,omitempty"`
	NativeConversion *nativeConversionOptions `json:"native_conversion,omitempty"`
	SourceRig        *nativeSourceRig         `json:"source_rig,omitempty"`
	SourceRigs       []nativeSourceRigRule    `json:"source_rigs,omitempty"`
	Transform        []float64                `json:"transform,omitempty"`
	// ReferenceWire selects a non-default reference image wire for
	// image-to-3d; "multiview_inputs" sends view-key `inputs` (multiview_model.go).
	ReferenceWire string `json:"reference_wire,omitempty"`
}

func New(config Config) (*Adapter, error) {
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:                config.BaseURL,
		APIKey:                 config.APIKey,
		AuthHeader:             config.AuthHeader,
		AuthPrefix:             config.AuthPrefix,
		RequestTimeout:         config.RequestTimeout,
		HTTPClient:             config.HTTPClient,
		ClassifyError:          classifyErrorWithMapping(config.ErrorMapping),
		SpoolDir:               config.ScratchDir,
		RetrySubmitServerError: config.RetrySubmitServerError,
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
	adapter := &Adapter{
		client:             client,
		allowHTTPAssets:    config.AllowHTTPAssets,
		inputAssets:        config.InputAssets,
		offeringRoutes:     routes,
		errorMapping:       config.ErrorMapping,
		statusMapping:      config.StatusMapping,
		nativeSourceClient: nativeSourceClient,
		scratchDir:         strings.TrimSpace(config.ScratchDir),
		clock:              config.Clock,
		pool:               config.Pool,
		poolID:             strings.TrimSpace(config.PoolID),
		poolIDs:            map[generation.OfferingID]string{},
	}
	// Each Offering's own declaration names its upstream pool (#487). An
	// Offering whose declaration is unreadable keeps no pool: queueing is an
	// optimization, and refusing to build over it would take the whole
	// connection down for one bad route.
	for id, offering := range routes {
		options, err := adapter.optionsFor(offering)
		if err != nil {
			continue
		}
		if pool := strings.TrimSpace(options.Pool); pool != "" {
			adapter.poolIDs[id] = pool
		}
	}
	return adapter, nil
}

// now is the Adapter's wall clock.
func (a *Adapter) now() time.Time {
	if a.clock != nil {
		return a.clock()
	}
	return time.Now()
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
		for _, key := range []string{"endpoint", "observe_endpoint", "upload_endpoint", "convert_endpoint", "response_aliases", "response_metadata"} {
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
	// The export options are checked now, before the paid generation: a
	// declaration that cannot be written would only fail after the model exists.
	if _, err := generationConversion(request.Offering, options); err != nil {
		return generation.Observation{}, err
	}
	multiviewWire := request.Offering.Capability == generation.ImageTo3D &&
		options.ReferenceWire == referenceWireMultiviewInputs
	var views []string
	if multiviewWire {
		// 视角校验是纯本地检查，必须先于任何上传与付费派发。
		if a.inputAssets == nil {
			return generation.Observation{}, failure(
				generation.FailureInvalidInput,
				false,
				"Tripo 多视图需要已解析参考图",
			)
		}
		if views, err = multiviewViews(request.Offering, input, len(request.InputAssets["reference_assets"])); err != nil {
			return generation.Observation{}, err
		}
	}
	body := make(map[string]any, len(input)+1)
	body["model"] = options.Model
	for name, value := range input {
		if name == "reference_assets" || name == request.Offering.ExtraConsentParameter() {
			continue
		}
		// reference_views only maps images to wire keys; it is not an upstream field.
		if multiviewWire && name == multiviewViewsParameter {
			continue
		}
		// Format selection and consent are private platform decisions. Their
		// authorized conversion jobs run only after the base model succeeds.
		if name == request.Offering.PriceSelectionParameter() {
			continue
		}
		// Export options (texture size, pivot, FBX preset) belong to the
		// conversion jobs, not to the generation request.
		if conversionBoundParameter(options, name) {
			continue
		}
		body[name] = value
	}
	if multiviewWire {
		assets := request.InputAssets["reference_assets"]
		tokens := make([]string, 0, len(assets))
		for _, asset := range assets {
			token, err := a.upload(ctx, request, asset)
			if err != nil {
				return generation.Observation{}, err
			}
			tokens = append(tokens, token)
		}
		body["inputs"] = multiviewInputsBody(views, tokens)
	} else if request.Offering.Capability == generation.ImageTo3D {
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
) (observation generation.Observation, err error) {
	// Public jobs in the shared pool are counted, never blocked (C3): Start
	// registered them, every Observe refreshes them, and a terminal observation
	// releases them. The private conversion paths below do their own Acquire.
	if a.poolEnabled(request.Offering) && poolCountsPublicJob(request.Offering) {
		// Readopt (not just Touch) so a restart re-registers the still-running
		// public job; it also refreshes last-seen.
		a.readoptPool(request.Offering, request.AttemptID)
		defer func() {
			if err == nil && terminalPhase(observation.Phase) {
				a.releasePool(request.Offering, request.AttemptID)
			}
		}()
	}
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
	// A free upload creates no upstream task: it is not a paid submission, so a
	// lost or failed upload is a retryable transport error rather than an
	// ambiguous outcome (Tripo's files page: an upload holds no credits). The
	// caller resends the whole multipart body and gets a fresh token; the token
	// of a lost response is simply never used.
	if err := a.client.Do(
		ctx,
		http.MethodPost,
		options.UploadEndpoint,
		reader,
		contentType,
		request.SubmissionKey+":upload",
		&response,
		false,
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
		resetHeader := ""
		if mapping != nil {
			resetHeader = strings.TrimSpace(mapping.RateLimitResetHeader)
		}
		retryAfterValue := time.Duration(0)
		if retryable {
			// Retry-After wins; the reset header is the documented fallback for
			// a refusal that carries only the window's end (rate-limits page:
			// "prefer Retry-After / X-RateLimit-Reset").
			retryAfterValue = retryAfter(response.Header.Get(retryHeader))
			if retryAfterValue <= 0 && resetHeader != "" {
				retryAfterValue = rateLimitReset(response.Header.Get(resetHeader))
			}
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

// rateLimitReset 解析限流窗口的重置头（Unix 秒，rate-limits 页的
// X-RateLimit-Reset）为还要等待多久。窗口已经过去或取值不可解析时返回 0，
// 让调用方退回自己的退避。
func rateLimitReset(value string) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 {
		return 0
	}
	delay := time.Until(time.Unix(seconds, 0))
	if delay <= 0 {
		return 0
	}
	return delay
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
	// A status word with a declared bound (Tripo's "unknown": the task-query
	// page says it is not a success or failure and to keep polling with a
	// timeout) keeps the task in its declared phase, but only for that long.
	// The poll after the bound ends the attempt as an ambiguous failure: the
	// upstream may still be working, so the job is never sent again and the
	// failure is not offered as a retryable one.
	if a.unknownStatusExpired(request.Job.RemoteID, status, rule) {
		observation.Phase = generation.UpstreamFailed
		// The coordinator drops an observation whose key equals the last
		// recorded one, so the terminal transition needs a key of its own.
		observation.DedupeKey = privateDedupeKey(request.Job.RemoteID, status, "poll-limit-exceeded")
		observation.Failure = &generation.Failure{
			Class:      generation.FailureSubmissionUnknown,
			Retryable:  false,
			Ambiguous:  true,
			SafeDetail: "Tripo 任务状态为 " + status + "，超过轮询时限，结果不明",
		}
		return observation, nil
	}
	switch rule.Phase {
	case "accepted":
		observation.Phase = generation.UpstreamAccepted
		// The Tripo task object reports no queue position, so a queued
		// status word yields a queue without a count.
		observation.Queue = httpjson.UpstreamQueue(a.statusMapping, rule, nil)
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
		attachResponseMetadata(observation.Artifacts, value.Output, options.ResponseMetadata)
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

// unknownStatusExpired reports whether a status word the connection bounds has
// now been reported for too long, and records this poll. The bound may be a
// number of consecutive polls (max_consecutive) or an amount of wall-clock
// time (max_duration_seconds, #460); whichever passes first ends the run. Any
// other status word, or a word with no declared bound, closes both windows.
func (a *Adapter) unknownStatusExpired(remoteID, status string, rule generation.StatusRule) bool {
	if rule.MaxConsecutive <= 0 && rule.MaxDurationSeconds <= 0 {
		a.clearUnknownStatus(remoteID)
		return false
	}
	if rule.MaxConsecutive > 0 {
		a.unknownMu.Lock()
		if a.unknownPolls == nil {
			a.unknownPolls = map[string]int{}
		}
		a.unknownPolls[remoteID]++
		exceeded := a.unknownPolls[remoteID] > rule.MaxConsecutive
		if exceeded {
			delete(a.unknownPolls, remoteID)
		}
		a.unknownMu.Unlock()
		if exceeded {
			a.clearUnknownStatus(remoteID)
			return true
		}
	}
	return a.unknownDurationExpired(remoteID, rule)
}

// unknownDurationExpired reports whether the wall-clock window of a status word
// declared with max_duration_seconds has passed (Tripo's "unknown": the
// task-query page says to continue polling with a timeout). The window opens
// with the first poll of the run and closes with any other status word; a
// restart grants a fresh allowance, like the poll count does.
func (a *Adapter) unknownDurationExpired(remoteID string, rule generation.StatusRule) bool {
	if rule.MaxDurationSeconds <= 0 {
		return false
	}
	limit := time.Duration(rule.MaxDurationSeconds) * time.Second
	now := a.now()
	a.unknownMu.Lock()
	defer a.unknownMu.Unlock()
	since, seen := a.unknownSince[remoteID]
	if !seen {
		if a.unknownSince == nil {
			a.unknownSince = map[string]time.Time{}
		}
		a.unknownSince[remoteID] = now
		return false
	}
	if now.Sub(since) < limit {
		return false
	}
	delete(a.unknownSince, remoteID)
	return true
}

// clearUnknownStatus closes the poll-count and duration windows of one remote
// task.
func (a *Adapter) clearUnknownStatus(remoteID string) {
	a.unknownMu.Lock()
	defer a.unknownMu.Unlock()
	delete(a.unknownPolls, remoteID)
	delete(a.unknownSince, remoteID)
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

// mappedTaskErrorClass 按连接级的任务级错误码词表（error_mapping.
// task_error_code_map）分类一个失败任务的 error_code。请求级顶层 code 与
// 任务级 error_code 是两套语义（官方 error-handling 页：同一个数字可能
// 含义不同），所以任务级码只查任务级表：表里没有的一律不分类，只作为证据
// 保留在 UpstreamCode/UpstreamExcerpt 中。
func (a *Adapter) mappedTaskErrorClass(code string) (generation.FailureClass, bool) {
	if code == "" || a.errorMapping == nil {
		return "", false
	}
	rule, ok := a.errorMapping.TaskErrorCodeMap[code]
	if !ok || rule.Class == "" {
		return "", false
	}
	return generation.FailureClass(rule.Class), true
}

// taskErrorCodeRetryable 报告一个失败任务的 error_code 是否被任务级词表声明
// 为可重试（瞬时类）。未声明、声明为不可重试或没有码的任务都不是瞬时失败：
// 转换子作业只对瞬时失败重提，确定性失败（1004 参数无效、2008 内容策略、
// 2018 模型过于复杂等）重发不会成功。
func (a *Adapter) taskErrorCodeRetryable(code string) bool {
	if code == "" || a.errorMapping == nil {
		return false
	}
	rule, ok := a.errorMapping.TaskErrorCodeMap[code]
	return ok && rule.Retryable
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

// responseMetadata builds the asset metadata for the output fields an Offering
// declares under response_metadata (a model conversion reports the origin move it
// made as output.normalize_scale / output.normalize_translation). The field names
// come from the Offering declaration, never from the Adapter. A field the output
// does not carry is skipped; no declaration, or none present, yields no metadata.
func responseMetadata(output map[string]any, fields []string) json.RawMessage {
	if len(fields) == 0 || len(output) == 0 {
		return nil
	}
	metadata := make(map[string]json.RawMessage, len(fields))
	for _, name := range fields {
		value, present := output[name]
		if !present {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			continue
		}
		metadata[name] = encoded
	}
	if len(metadata) == 0 {
		return nil
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil
	}
	return encoded
}

// attachResponseMetadata copies the declared output fields onto the model claim's
// metadata (asset metadata), so a conversion's reported transform survives the
// Adapter. A claim that already carries metadata keeps it.
func attachResponseMetadata(claims []generation.ArtifactClaim, output map[string]any, fields []string) {
	metadata := responseMetadata(output, fields)
	if metadata == nil {
		return
	}
	for i := range claims {
		if claims[i].Role == "model" && len(claims[i].Metadata) == 0 {
			claims[i].Metadata = metadata
		}
	}
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
