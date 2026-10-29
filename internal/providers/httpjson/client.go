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
}

// HTTPErrorClassifier 对 HTTP 非 2xx 响应做 provider 专有分类。
type HTTPErrorClassifier func(response *http.Response, payload []byte) error

// ClassifyGatewayError 是网宿 AI 网关（chat/completions、images/generations、
// images/edits）的专属分类器。依据官方文档（2026-09-17 检索）：
// 401 = 缺 Authorization/token 无效（鉴权问题）；403 = 请求主体 JSON 格式
// 错误或缺必填参数（我方请求构造问题）；500 = 网关/上游内部错误（第三方）。
// 未覆盖的状态码返回 nil，回退到通用 HTTP 分类。
func ClassifyGatewayError(response *http.Response, payload []byte) error {
	var class generation.FailureClass
	switch response.StatusCode {
	case http.StatusUnauthorized:
		class = generation.FailureAuthentication
	case http.StatusForbidden:
		class = generation.FailureInvalidInput
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
	classifyError := config.ClassifyError
	if classifyError == nil && config.ErrorMapping != nil {
		classifyError = ClassifyWithMapping(config.ErrorMapping)
	}
	return &Client{
		AuthHeader:       config.AuthHeader,
		AuthPrefix:       config.AuthPrefix,
		BaseURL:          parsed,
		APIKey:           config.APIKey,
		HTTPClient:       config.HTTPClient,
		maxResponseBytes: config.MaxResponseBytes,
		classifyError:    classifyError,
		spoolDir:         strings.TrimSpace(config.SpoolDir),
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
	var encoded io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return providerError(generation.FailureInternal, false, false, "编码 Provider 请求失败", err)
		}
		encoded = bytes.NewReader(data)
	}
	return c.Do(
		ctx,
		method,
		path,
		encoded,
		"application/json",
		requestID,
		target,
		submitting,
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
	// Finish multipart construction before opening a connection. Local asset errors
	// then prove that no upstream task was created, without buffering images in RAM.
	spooledBytes := int64(-1)
	if submitting && body != nil && strings.HasPrefix(contentType, "multipart/") {
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
			return providerError(generation.FailureTransport, true, false, "准备上传文件失败", err)
		}
		defer os.Remove(file.Name())
		defer file.Close()
		if _, err := io.Copy(file, requestBodyReader{ctx: ctx, Reader: body}); err != nil {
			return providerError(generation.FailureTransport, true, false, "读取本地上传文件失败，尚未向上游提交", err)
		}
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			return providerError(generation.FailureTransport, true, false, "读取本地上传文件失败，尚未向上游提交", err)
		}
		if info, statErr := file.Stat(); statErr == nil {
			spooledBytes = info.Size()
		}
		body = file
	}
	endpoint, err := c.resolve(path)
	if err != nil {
		return providerError(generation.FailureProtocol, false, false, "Provider endpoint 无效", err)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return providerError(generation.FailureInternal, false, false, "创建 Provider 请求失败", err)
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
		return providerError(
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
		return providerError(
			generation.FailureTransport,
			true,
			submitting,
			"读取 Provider 响应失败",
			readErr,
		)
	}
	if int64(len(payload)) > c.maxResponseBytes {
		return providerError(
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
			return &generation.ProviderError{Failure: withUpstreamEvidence(
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
				return err
			}
		}
		return classifyHTTP(response, payload)
	}
	if target == nil || response.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return &generation.ProviderError{Failure: withUpstreamEvidence(
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
	return nil
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
	return fmt.Sprintf("上游服务返回 HTTP %d，请检查输入或稍后重试", status)
}

func safeMessage(payload []byte) string {
	message, _ := errorEvidence(payload)
	return message
}

// errorEvidence 从上游错误体提取可读消息与机器错误码。
// 覆盖四类已核实契约：网宿网关 {message}、OpenAI/Ark 信封 {error:{code,message,type}}、
// Tripo {code,message,suggestion}、Meshy {message}（含 task_error 透传场景）。
func errorEvidence(payload []byte) (string, string) {
	var body struct {
		Code    json.Number `json:"code"`
		Message string      `json:"message"`
		Detail  string      `json:"detail"`
		Error   struct {
			Code    string `json:"code"`
			Type    string `json:"type"`
			Message string `json:"message"`
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
	code := strings.TrimSpace(body.Error.Code)
	if code == "" {
		code = strings.TrimSpace(body.Error.Type)
	}
	if code == "" && body.Code.String() != "" && body.Code.String() != "0" {
		code = body.Code.String()
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
