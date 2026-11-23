// Package schemaimages executes the synchronous image protocol declared by an Offering.
package schemaimages

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/httpjson"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
)

type Config struct {
	BaseURL        string
	APIKey         string
	AuthHeader     string
	AuthPrefix     string
	RequestTimeout time.Duration
	ErrorMapping   *generation.ErrorMapping
	HTTPClient     *http.Client
	InputAssets    generation.InputAssetResolver
	// ScratchDir holds private upload copies (spooled multipart bodies).
	// Empty means os.TempDir(); production passes the storage volume's tmp
	// directory because the container's /tmp is a small tmpfs (#321 A).
	ScratchDir string
}
type Adapter struct {
	client  *httpjson.Client
	inputs  generation.InputAssetResolver
	mapping *generation.ErrorMapping
}

func New(config Config) (*Adapter, error) {
	classify := httpjson.ClassifyGatewayError
	if config.ErrorMapping != nil {
		// 连接级声明式错误词表；网关通用分类器保留为未声明时的安全兜底。
		classify = httpjson.ClassifyWithMapping(config.ErrorMapping)
	}
	client, err := httpjson.NewWithConfig(httpjson.Config{
		BaseURL:        config.BaseURL,
		APIKey:         config.APIKey,
		AuthHeader:     config.AuthHeader,
		AuthPrefix:     config.AuthPrefix,
		RequestTimeout: config.RequestTimeout,
		ErrorMapping:   config.ErrorMapping,
		HTTPClient:     config.HTTPClient,
		ClassifyError:  classify,
		SpoolDir:       config.ScratchDir,
	})
	if err != nil {
		return nil, err
	}
	return &Adapter{client: client, inputs: config.InputAssets, mapping: config.ErrorMapping}, nil
}
func (a *Adapter) Start(ctx context.Context, request generation.StartRequest) (generation.Observation, error) {
	var protocol imagewire.Protocol
	if err := json.Unmarshal(request.Offering.ProviderOptions, &protocol); err != nil || protocol.Endpoint == "" || protocol.Response.ImagesPath == "" {
		return generation.Observation{}, failure(generation.FailureProtocol, "图片协议配置无效")
	}
	var response map[string]any
	client := a.client.WithMaxResponseBytes(protocol.MaxResponseBytes)
	client.AuthHeader, client.AuthPrefix = protocol.AuthHeader, protocol.AuthPrefix
	if protocol.RequestEncoding == "multipart" {
		multipartBody, contentType, err := imagewire.EncodeMultipart(ctx, request, a.inputs)
		if err != nil {
			return generation.Observation{}, failure(generation.FailureInvalidInput, err.Error())
		}
		if err := client.Do(ctx, http.MethodPost, protocol.Endpoint, multipartBody, contentType, request.SubmissionKey, &response, true); err != nil {
			return generation.Observation{}, err
		}
	} else {
		body, err := imagewire.Build(ctx, request, a.inputs)
		if err != nil {
			return generation.Observation{}, failure(generation.FailureInvalidInput, err.Error())
		}
		if err := client.DoJSON(ctx, http.MethodPost, protocol.Endpoint, body, request.SubmissionKey, &response, true); err != nil {
			return generation.Observation{}, err
		}
	}
	var claims []generation.ArtifactClaim
	for _, raw := range imagewire.Select(response, protocol.Response.ImagesPath) {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		encoded, _ := item[protocol.Response.Base64Field].(string)
		locator, _ := item[protocol.Response.URLField].(string)
		mime, _ := item[protocol.Response.MediaTypeField].(string)
		if encoded == "" && locator == "" {
			continue
		}
		claim := generation.ArtifactClaim{Role: protocol.Response.Role, Locator: locator, MediaType: mime, ID: fmt.Sprintf("%s:image:%d", request.AttemptID, len(claims))}
		if encoded != "" {
			if strings.HasPrefix(encoded, "data:") {
				parts := strings.SplitN(encoded, ",", 2)
				if len(parts) != 2 {
					return generation.Observation{}, failure(generation.FailureProtocol, "上游图片编码无效")
				}
				encoded = parts[1]
			}
			var decodeErr error
			claim.Inline, decodeErr = base64.StdEncoding.DecodeString(encoded)
			if decodeErr != nil || len(claim.Inline) == 0 {
				return generation.Observation{}, failure(generation.FailureProtocol, "上游图片编码无效")
			}
			claim.MediaType = http.DetectContentType(claim.Inline)
			if !strings.HasPrefix(claim.MediaType, "image/") {
				return generation.Observation{}, failure(generation.FailureProtocol, "上游未返回有效图片")
			}
			claim.Locator = ""
		}
		if claim.MediaType == "" {
			claim.MediaType = protocol.Response.DefaultMediaType
		}
		claims = append(claims, claim)
	}
	if len(claims) == 0 {
		// 上游用 200 答复表达“没有出图”的原因（legacy finishReason、
		// promptFeedback.blockReason）时，按连接 error_mapping 的码表分类，
		// 而不是一律读成协议错误：内容阻断要落到 content_rejected，Agent 才
		// 会去改输入/换图，而不是重试同一条请求。
		if reason, rule, ok := a.blockedReason(response, protocol); ok {
			class := generation.FailureClass(rule.Class)
			return generation.Observation{}, failureWithRetry(class, noPictureMessage(class, reason), rule.Retryable)
		}
		return generation.Observation{}, failure(generation.FailureProtocol, "上游完成但未返回图片")
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: claims, DedupeKey: string(request.AttemptID) + ":completed"}, nil
}

// blockedReason reads the reason words a picture-less 200 answer carries, in
// the order the Offering's protocol declares them (a prompt-level block first:
// it means there is no candidate at all), and maps each through the
// connection's declared error code map. ok is false when the protocol declares
// no such field, or none of the words is mapped, so an unmapped reason stays
// the protocol failure it was.
func (a *Adapter) blockedReason(response map[string]any, protocol imagewire.Protocol) (string, generation.ErrorClassRule, bool) {
	for _, path := range []string{protocol.Response.PromptFeedbackPath, protocol.Response.FinishReasonPath} {
		if strings.TrimSpace(path) == "" {
			continue
		}
		for _, raw := range imagewire.Select(response, path) {
			word, ok := raw.(string)
			if !ok {
				continue
			}
			if rule, ok := httpjson.MappedCodeRule(a.mapping, word); ok {
				return strings.TrimSpace(word), rule, true
			}
		}
	}
	return "", generation.ErrorClassRule{}, false
}

// noPictureMessage is the task text of a 200 answer that reported no picture.
// It names the upstream's reason word for forensics but never repeats an
// upstream message: the word is from the Provider's documented vocabulary, an
// upstream message is not.
func noPictureMessage(class generation.FailureClass, reason string) string {
	switch class {
	case generation.FailureContentRejected:
		return "上游在 200 答复里报告内容审核拦截（" + reason + "），这一步没有生成结果"
	default:
		return "上游在 200 答复里报告没有返回图片（" + reason + "），这一步没有生成结果"
	}
}
func (a *Adapter) Observe(context.Context, generation.ObserveRequest) (generation.Observation, error) {
	return generation.Observation{}, failure(generation.FailureProtocol, "同步图片协议不支持轮询")
}
func (a *Adapter) OpenAsset(ctx context.Context, request generation.OpenAssetRequest) (io.ReadCloser, generation.AssetMetadata, error) {
	return httpjson.OpenClaim(ctx, a.client.HTTPClient, request.Claim, request.Offering.AssetHosts, false)
}
func failure(class generation.FailureClass, message string) error {
	return &generation.ProviderError{Failure: &generation.Failure{Class: class, SafeDetail: message}}
}

// failureWithRetry is a declared failure that may be retried: the reason word
// a connection maps to a retryable class (no_image) says so in the Schema.
func failureWithRetry(class generation.FailureClass, message string, retryable bool) error {
	return &generation.ProviderError{Failure: &generation.Failure{Class: class, SafeDetail: message, Retryable: retryable}}
}
