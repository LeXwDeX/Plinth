package httpjson

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

const defaultMaxResponseBytes int64 = 4 << 20

// DefaultRequestTimeout 是未显式注入 HTTPClient 时的默认请求总超时。
// 30 分钟对齐前端产品约定 PROVIDER_REQUEST_TIMEOUT_MS /
// GPT_IMAGE_REQUEST_TIMEOUT_MS（src/lib/providers/provider-config.ts）：
// gpt-image 等同步出图经常超过 2 分钟，过短的客户端超时会把仍在执行的
// 提交掐断为传输层中断（ambiguous → submission_unknown，
// 用户看到「无法确认上游是否已接受任务」）。
const DefaultRequestTimeout = 30 * time.Minute

type Client struct {
	AuthHeader       string
	AuthPrefix       string
	BaseURL          *url.URL
	APIKey           string
	HTTPClient       *http.Client
	maxResponseBytes int64
	classifyError    HTTPErrorClassifier
	spoolDir         string
	// submitTimeout bounds a task-creation request (submitting=true) without
	// shortening the client timeout that also covers polls and downloads.
	// Zero leaves the caller's context as the only bound.
	submitTimeout time.Duration
	// largeSubmitTimeout applies to a creation request whose body exceeds
	// largeSubmitBytes.
	largeSubmitTimeout time.Duration
	largeSubmitBytes   int64
	// retrySubmitServerError is the connection-level declaration that a 5xx
	// answer to a creation request may be retried (#460 C25).
	retrySubmitServerError bool
	// pollTimeout bounds one non-creation request (a status poll) without
	// touching the client timeout that also covers downloads (#487).
	pollTimeout time.Duration
}

// largeSubmitBytes is the body size above which a creation request uses
// largeSubmitTimeout (Meshy: a model_url file over 100 MB).
const largeSubmitBytes int64 = 100 << 20

// Response is the metadata of a completed HTTP exchange. Adapters use it to
// read the upstream's Retry-After on a non-terminal poll.
type Response struct {
	StatusCode int
	Header     http.Header
}

// ScratchFilePrefix names every private scratch file a Provider Adapter
// writes. Storage maintenance removes old ones left by a crash.
const ScratchFilePrefix = "provider-"

type Config struct {
	BaseURL          string
	APIKey           string
	AuthHeader       string
	AuthPrefix       string
	RequestTimeout   time.Duration
	HTTPClient       *http.Client
	MaxResponseBytes int64
	// ErrorMapping 可选：连接级声明式错误词汇表；非空时构造声明驱动的
	// 通用分类器，未命中声明规则的错误回退到默认 HTTP 状态码分类。
	ErrorMapping *generation.ErrorMapping
	// ClassifyError 可选：对 HTTP 非 2xx 响应做 provider 专有的错误分类。
	// 返回非 nil error 时采用该分类；返回 nil 时回退到默认 HTTP 状态码分类。
	ClassifyError HTTPErrorClassifier
	// SpoolDir 是 multipart 提交体落盘的目录。留空时用 os.TempDir()；
	// 生产装配传入存储卷的 tmp 目录：容器的 /tmp 是 64 MB 的 tmpfs，
	// 装不下上百 MB 的模型上传（#321 A）。
	SpoolDir string
	// SubmitTimeout 限定创建请求（submitting=true）的单次时长，不影响
	// 轮询与产物下载使用的 HTTPClient 超时。零值表示只由调用方 context 约束。
	// 官方建议（Meshy，2026-11-22）：创建请求 60 秒，请求体超过 100 MB 时 120 秒。
	SubmitTimeout time.Duration
	// LargeSubmitTimeout 是请求体超过 LargeSubmitBytes 时创建请求的时长；
	// 零值时退回 SubmitTimeout。
	LargeSubmitTimeout time.Duration
	// LargeSubmitBytes 是创建请求使用 LargeSubmitTimeout 的请求体大小阈值；
	// 零值取 100 MiB（Meshy 官方：model_url 文件超过 100 MB）。
	LargeSubmitBytes int64
	// RetrySubmitServerError 是连接级声明：创建请求收到 5xx 时可以重试
	// （#460 C25）。上游可能已经受理创建，因此重试有重复扣费风险；
	// 只有声明允许的连接才这样做，未声明的连接保持「结果不明不重发」。
	RetrySubmitServerError bool
	// PollTimeout 限定非创建请求（任务状态轮询）的单次时长（#487）。连接级
	// 请求超时同时覆盖轮询与产物下载，官方对 GET 的建议是 30 秒
	// （Meshy，2026-11-23）；声明后轮询单独用这个值，下载不受影响。
	// 零值表示不额外限制。
	PollTimeout time.Duration
}

// HTTPErrorClassifier 对 HTTP 非 2xx 响应做 provider 专有分类。
type HTTPErrorClassifier func(response *http.Response, payload []byte) error

// ClassifyGatewayError 是网宿 AI 网关（chat/completions、images/generations、
// images/edits）的专属分类器。依据官方文档（2026-09-17 检索）：
// 401 = 缺 Authorization/token 无效（鉴权问题）；403 = 网关拒绝服务，按
// upstream_denied 处理：文档称它表示请求体错误，但实际出现的是网关访问拒绝
// （eca_access_denied，IP 不在白名单），改写输入无济于事；500 = 网关/上游
// 内部错误（第三方）。
// 未覆盖的状态码返回 nil，回退到通用 HTTP 分类。
func ClassifyGatewayError(response *http.Response, payload []byte) error {
	var class generation.FailureClass
	switch response.StatusCode {
	case http.StatusUnauthorized:
		class = generation.FailureAuthentication
	case http.StatusForbidden:
		class = generation.FailureUpstreamDenied
	default:
		return nil
	}
	failure := &generation.Failure{
		Class:     class,
		Retryable: false,
	}
	withUpstreamEvidence(failure, response.StatusCode, payload)
	return &generation.ProviderError{Failure: failure}
}

func New(baseURL, apiKey string, client *http.Client) (*Client, error) {
	return NewWithConfig(Config{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: client,
	})
}

func NewWithConfig(config Config) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(config.BaseURL), "/") + "/")
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("Provider base URL 无效")
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("Provider base URL 仅支持 HTTP(S)")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		return nil, errors.New("Provider API key 不能为空")
	}
	if config.HTTPClient == nil {
		timeout := config.RequestTimeout
		if timeout <= 0 {
			timeout = DefaultRequestTimeout
		}
		config.HTTPClient = &http.Client{Timeout: timeout}
	}
	if config.MaxResponseBytes <= 0 {
		config.MaxResponseBytes = defaultMaxResponseBytes
	}
	if config.LargeSubmitBytes <= 0 {
		config.LargeSubmitBytes = largeSubmitBytes
	}
	classifyError := config.ClassifyError
	if classifyError == nil && config.ErrorMapping != nil {
		classifyError = ClassifyWithMapping(config.ErrorMapping)
	}
	return &Client{
		AuthHeader:             config.AuthHeader,
		AuthPrefix:             config.AuthPrefix,
		BaseURL:                parsed,
		APIKey:                 config.APIKey,
		HTTPClient:             config.HTTPClient,
		maxResponseBytes:       config.MaxResponseBytes,
		classifyError:          classifyError,
		spoolDir:               strings.TrimSpace(config.SpoolDir),
		submitTimeout:          config.SubmitTimeout,
		largeSubmitTimeout:     config.LargeSubmitTimeout,
		largeSubmitBytes:       config.LargeSubmitBytes,
		retrySubmitServerError: config.RetrySubmitServerError,
		pollTimeout:            config.PollTimeout,
	}, nil
}

func (c *Client) DoJSON(
	ctx context.Context,
	method string,
	path string,
	body any,
	requestID string,
	target any,
	submitting bool,
) error {
	_, err := c.DoJSONResponse(ctx, method, path, body, requestID, target, submitting)
	return err
}

// DoJSONResponse is DoJSON plus the metadata of a completed exchange. A
// non-terminal poll uses the header to honour the upstream's Retry-After.
func (c *Client) DoJSONResponse(
	ctx context.Context,
	method string,
	path string,
	body any,
	requestID string,
	target any,
	submitting bool,
) (Response, error) {
	var encoded io.Reader
	knownSize := int64(-1)
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return Response{}, providerError(generation.FailureInternal, false, false, "编码 Provider 请求失败", err)
		}
		encoded = bytes.NewReader(data)
		knownSize = int64(len(data))
	}
	return c.do(
		ctx,
		method,
		path,
		encoded,
		"application/json",
		requestID,
		target,
		submitting,
		knownSize,
	)
}

func (c *Client) Do(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
	contentType string,
	requestID string,
	target any,
	submitting bool,
) error {
	_, err := c.do(ctx, method, path, body, contentType, requestID, target, submitting, -1)
	return err
}

func (c *Client) do(
	ctx context.Context,
	method string,
	path string,
	body io.Reader,
	contentType string,
	requestID string,
	target any,
	submitting bool,
	knownSize int64,
) (Response, error) {
	// Finish multipart construction before opening a connection. Local asset errors
	// then prove that no upstream task was created, without buffering images in RAM.
	spooledBytes := int64(-1)
	if submitting && body != nil && strings.HasPrefix(contentType, "multipart/") {
		if size, ok := reusableMultipartFile(body); ok {
			// The provider already spooled the finished body to a regular file
			// (Hyper3D submitMultipart): send it directly instead of copying it
			// into a second scratch file. The caller keeps ownership of the file
			// and closes/removes it after Do returns.
			spooledBytes = size
			// net/http would take over an io.ReadCloser body and close the
			// caller's file; the plain reader keeps that lifecycle caller-owned.
			body = readerOnly{body}
		} else {
			if closer, ok := body.(io.Closer); ok {
				closed := make(chan struct{})
				stop := context.AfterFunc(ctx, func() { _ = closer.Close(); close(closed) })
				defer func() {
					if !stop() {
						<-closed
					}
					_ = closer.Close()
				}()
			}
			file, err := os.CreateTemp(c.spoolDir, ScratchFilePrefix+"upload-*")
			if err != nil {
				return Response{}, providerError(generation.FailureTransport, true, false, "准备上传文件失败", err)
			}
			defer os.Remove(file.Name())
			defer file.Close()
			if _, err := io.Copy(file, requestBodyReader{ctx: ctx, Reader: body}); err != nil {
				return Response{}, providerError(generation.FailureTransport, true, false, "读取本地上传文件失败，尚未向上游提交", err)
			}
			if _, err := file.Seek(0, io.SeekStart); err != nil {
				return Response{}, providerError(generation.FailureTransport, true, false, "读取本地上传文件失败，尚未向上游提交", err)
			}
			if info, statErr := file.Stat(); statErr == nil {
				spooledBytes = info.Size()
			}
			body = file
		}
	}
	endpoint, err := c.resolve(path)
	if err != nil {
		return Response{}, providerError(generation.FailureProtocol, false, false, "Provider endpoint 无效", err)
	}
	// A creation request has the provider's own shorter guidance (Meshy:
	// 60 s, or 120 s when the body carries a file over 100 MB). Apply it to
	// this request only; the client timeout still bounds polls and downloads.
	if submitting && c.submitTimeout > 0 {
		timeout := c.submitTimeout
		size := knownSize
		if size < 0 {
			size = spooledBytes
		}
		if size > c.largeSubmitBytes && c.largeSubmitTimeout > 0 {
			timeout = c.largeSubmitTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	} else if !submitting && c.pollTimeout > 0 {
		// A status poll needs far less than the connection timeout that also
		// has to let a big artifact download finish (Meshy: GET 30 s, #487).
		// Downloads never come through here, so only polls are shortened.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.pollTimeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return Response{}, providerError(generation.FailureInternal, false, false, "创建 Provider 请求失败", err)
	}
	if submitting {
		// 非幂等提交关闭传输层自动重放（§4.3）：清除 GetBody 使
		// 307/308 重定向无法重发请求体；POST+body 本身不可重试
		// （isReplayable=false），重发必须重新取得检查点许可。
		request.GetBody = nil
	}
	// Spooled multipart bodies are seekable temp files; sending the known length
	// keeps the proven Content-Length form contract instead of chunked encoding.
	if spooledBytes >= 0 {
		request.ContentLength = spooledBytes
	}
	request.Header.Set("Accept", "application/json")
	if c.AuthHeader != "" {
		request.Header.Set(c.AuthHeader, c.AuthPrefix+c.APIKey)
	} else {
		request.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if body != nil && contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if requestID != "" {
		request.Header.Set("X-Request-ID", requestID)
	}

	var tlsFailed atomic.Bool
	var gotConnection atomic.Bool
	request = request.WithContext(httptrace.WithClientTrace(request.Context(), &httptrace.ClientTrace{
		TLSHandshakeDone: func(_ tls.ConnectionState, err error) { tlsFailed.Store(err != nil) },
		GotConn:          func(httptrace.GotConnInfo) { gotConnection.Store(true) },
	}))
	response, err := c.HTTPClient.Do(request)
	if err != nil {
		ambiguous := submitting && !(tlsFailed.Load() && !gotConnection.Load()) && !definitelyNotSent(err)
		return Response{}, providerError(
			generation.FailureTransport,
			!ambiguous,
			ambiguous,
			"无法确认 Provider 请求结果",
			err,
		)
	}
	defer response.Body.Close()
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseBytes+1))
	if readErr != nil {
		return Response{}, providerError(
			generation.FailureTransport,
			true,
			submitting,
			"读取 Provider 响应失败",
			readErr,
		)
	}
	if int64(len(payload)) > c.maxResponseBytes {
		return Response{}, providerError(
			generation.FailureProtocol,
			false,
			submitting,
			"Provider 响应超过安全上限",
			nil,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		// A gateway/server failure cannot prove that a create request was rejected.
		// Apply this before provider-specific wording so classifiers cannot turn an
		// uncertain acceptance into an automatic second submission.
		if submitting && (response.StatusCode == http.StatusRequestTimeout || response.StatusCode >= 500) {
			// A connection that declares it may retry a 5xx creation: the
			// upstream might have accepted the work before failing, so the
			// retry risks a second charge. The connection owns that trade-off
			// (#460 C25); the default stays "unknown, never resend".
			if c.retrySubmitServerError && response.StatusCode >= 500 {
				return Response{}, &generation.ProviderError{Failure: withUpstreamEvidence(
					&generation.Failure{
						Class:      generation.FailureUpstreamUnavailable,
						Retryable:  true,
						SafeDetail: "上游返回错误，按连接声明重试提交",
						RetryAfter: retryAfter(response.Header.Get("Retry-After")),
					},
					response.StatusCode,
					payload,
				)}
			}
			return Response{}, &generation.ProviderError{Failure: withUpstreamEvidence(
				&generation.Failure{
					Class:      generation.FailureSubmissionUnknown,
					Retryable:  false,
					Ambiguous:  true,
					SafeDetail: "上游返回错误，无法确认是否已接受提交，已停止自动重发",
				},
				response.StatusCode,
				payload,
			)}
		}
		if c.classifyError != nil {
			if err := c.classifyError(response, payload); err != nil {
				return Response{}, err
			}
		}
		return Response{}, classifyHTTP(response, payload)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return Response{}, nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return Response{}, &generation.ProviderError{Failure: withUpstreamEvidence(
			&generation.Failure{
				Class:      generation.FailureProtocol,
				Retryable:  false,
				Ambiguous:  submitting,
				SafeDetail: "Provider 返回了无效 JSON",
			},
			response.StatusCode,
			payload,
		), Cause: err}
	}
	return Response{StatusCode: response.StatusCode, Header: response.Header.Clone()}, nil
}

func (c *Client) resolve(path string) (*url.URL, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("空 endpoint")
	}
	relative, err := url.Parse(strings.TrimLeft(path, "/"))
	if err != nil || relative.IsAbs() || relative.Host != "" {
		return nil, errors.New("endpoint 必须是相对路径")
	}
	return c.BaseURL.ResolveReference(relative), nil
}

func classifyHTTP(response *http.Response, payload []byte) error {
	class := generation.FailureRejected
	retryable := false
	switch response.StatusCode {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		class = generation.FailureInvalidInput
	case http.StatusUnauthorized, http.StatusForbidden:
		class = generation.FailureAuthentication
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		class = generation.FailureTimedOut
		retryable = true
	case http.StatusTooManyRequests:
		class = generation.FailureRateLimited
		retryable = true
	default:
		if response.StatusCode >= 500 {
			class = generation.FailureUpstreamUnavailable
			retryable = true
		}
	}
	failure := &generation.Failure{
		Class:      class,
		Retryable:  retryable,
		SafeDetail: publicHTTPErrorMessage(response.StatusCode),
		RetryAfter: retryAfter(response.Header.Get("Retry-After")),
	}
	withUpstreamEvidence(failure, response.StatusCode, payload)
	return &generation.ProviderError{Failure: failure}
}

// Upstream error bodies may contain private URLs, request IDs or account data.
// Keep their evidence in the private failure fields, never in public task text.
func publicHTTPErrorMessage(status int) string {
	if status == http.StatusForbidden {
		// 403 is the upstream refusing service, not a bad input (upstream_denied).
		return "上游服务返回 HTTP 403，拒绝了这次请求"
	}
	return fmt.Sprintf("上游服务返回 HTTP %d，请检查输入或稍后重试", status)
}

func safeMessage(payload []byte) string {
	message, _ := errorEvidence(payload)
	return message
}

// errorCode is an upstream code field that may be encoded as a JSON string or
// as a JSON number. Google's Interactions error object answers a snake_case
// string at the top level ({code, message}) while its legacy generateContent
// envelope answers a number under error ({error:{code,message,status}}): a
// json.Number field made the whole body unreadable for the first form and a
// string field for the second, leaving UpstreamCode, UpstreamMessage and the
// excerpt empty, so a declared code mapping never ran. A value that is neither
// (null, an object) reads as no code rather than failing the whole body.
type errorCode string

func (code *errorCode) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	switch {
	case trimmed == "" || trimmed == "null":
		*code = ""
	case trimmed[0] == '"':
		var text string
		if err := json.Unmarshal(data, &text); err != nil {
			return nil
		}
		*code = errorCode(strings.TrimSpace(text))
	case trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9':
		var number json.Number
		if err := json.Unmarshal(data, &number); err != nil {
			return nil
		}
		*code = errorCode(number.String())
	default:
		*code = ""
	}
	return nil
}

// errorEvidence 从上游错误体提取可读消息与机器错误码。
// 覆盖五类已核实契约：网宿网关 {message}、OpenAI/Ark 信封 {error:{code,message,type}}、
// Tripo {code,message,suggestion}、Meshy {message}（含 task_error 透传场景）、
// Google 官方两种信封（Interactions 顶层 {code,message} 与 legacy
// generateContent 的 {error:{code:number,message,status}}）。
func errorEvidence(payload []byte) (string, string) {
	var body struct {
		Code    errorCode `json:"code"`
		Message string    `json:"message"`
		Detail  string    `json:"detail"`
		Error   struct {
			Code    errorCode `json:"code"`
			Type    string    `json:"type"`
			Message string    `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(payload, &body) != nil {
		return "", ""
	}
	message := body.Error.Message
	if message == "" {
		message = body.Message
	}
	if message == "" {
		message = body.Detail
	}
	code := strings.TrimSpace(string(body.Error.Code))
	if code == "" {
		code = strings.TrimSpace(body.Error.Type)
	}
	if code == "" && body.Code != "" && body.Code != "0" {
		code = string(body.Code)
	}
	const maximumRunes = 500
	runes := []rune(strings.TrimSpace(message))
	if len(runes) > maximumRunes {
		runes = runes[:maximumRunes]
	}
	return string(runes), code
}

// boundedExcerpt 生成去敏的错误体摘录，供落库与管理侧诊断使用；不会出现在
// 面向用户的文案里。去敏规则只有 generation.UpstreamExcerpt 一份（URL、签名
// query、令牌、邮箱、不可打印字符、按 rune 截断）；内核在落库前还会按冻结输入
// 再剔除提示词回显。
func boundedExcerpt(payload []byte) string {
	return generation.UpstreamExcerpt(string(payload), generation.UpstreamExcerptRunes)
}

func withUpstreamEvidence(
	failure *generation.Failure,
	status int,
	payload []byte,
) *generation.Failure {
	message, code := errorEvidence(payload)
	failure.UpstreamStatus = status
	failure.UpstreamCode = code
	failure.UpstreamExcerpt = boundedExcerpt(payload)
	if failure.SafeDetail == "" && message != "" {
		failure.SafeDetail = message
	}
	return failure
}

// AttachUpstreamEvidence 供 provider 专属分类器复用：把上游状态码、
// 机器错误码与去敏响应摘录挂到 Failure 上（不覆盖已有 SafeDetail）。
func AttachUpstreamEvidence(
	failure *generation.Failure,
	status int,
	payload []byte,
) *generation.Failure {
	if failure == nil {
		return nil
	}
	return withUpstreamEvidence(failure, status, payload)
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if parsed, err := http.ParseTime(value); err == nil {
		delay := time.Until(parsed)
		if delay > 0 {
			return delay
		}
	}
	return 0
}

// PollAfter is the non-terminal poll delay an upstream asks for: the
// Retry-After header when it carries one, else fallback. It is the shared rule
// (Meshy documents it as the Retry-After header, or 5 seconds when absent), so
// a Provider does not invent its own parsing; zero and malformed values leave
// fallback unchanged.
func PollAfter(header http.Header, fallback time.Duration) time.Duration {
	if header == nil {
		return fallback
	}
	if delay := retryAfter(header.Get("Retry-After")); delay > 0 {
		return delay
	}
	return fallback
}

func providerError(
	class generation.FailureClass,
	retryable bool,
	ambiguous bool,
	detail string,
	cause error,
) error {
	return &generation.ProviderError{
		Failure: &generation.Failure{
			Class:      class,
			Retryable:  retryable,
			Ambiguous:  ambiguous,
			SafeDetail: detail,
		},
		Cause: cause,
	}
}

func definitelyNotSent(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return true
	}
	var urlErr *url.Error
	return errors.As(err, &urlErr) && strings.Contains(strings.ToLower(urlErr.Error()), "unsupported protocol scheme")
}

// reusableMultipartFile reports body as an already-spooled seekable regular
// file and rewinds it. A provider that preflights the complete multipart body on
// disk can hand that file over instead of paying for a second spool copy.
func reusableMultipartFile(body io.Reader) (int64, bool) {
	file, ok := body.(*os.File)
	if !ok {
		return 0, false
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return 0, false
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return 0, false
	}
	return info.Size(), true
}

// readerOnly hides an io.ReadCloser so net/http wraps it in io.NopCloser and
// leaves the caller's file open after the request.
type readerOnly struct {
	io.Reader
}

// Real multipart bodies are pipes; the cancellation hook above closes the pipe
// to interrupt a blocked Read, while this wrapper checks context between chunks.
type requestBodyReader struct {
	ctx context.Context
	io.Reader
}

func (r requestBodyReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.Reader.Read(p)
	if ctxErr := r.ctx.Err(); ctxErr != nil {
		return 0, ctxErr
	}
	return n, err
}

// WithMaxResponseBytes returns a request-scoped copy using the schema response budget.
func (c *Client) WithMaxResponseBytes(limit int64) *Client {
	copy := *c
	if limit > 0 {
		copy.maxResponseBytes = limit
	}
	return &copy
}
