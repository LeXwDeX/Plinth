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
	client *httpjson.Client
	inputs generation.InputAssetResolver
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
	return &Adapter{client: client, inputs: config.InputAssets}, nil
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
		return generation.Observation{}, failure(generation.FailureProtocol, "上游完成但未返回图片")
	}
	return generation.Observation{Phase: generation.UpstreamCompleted, Progress: 100, Artifacts: claims, DedupeKey: string(request.AttemptID) + ":completed"}, nil
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
