package httpjson

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func OpenClaim(
	ctx context.Context,
	client *http.Client,
	claim generation.ArtifactClaim,
	allowedHosts []string,
	allowHTTP bool,
) (io.ReadCloser, generation.AssetMetadata, error) {
	if len(claim.Inline) > 0 {
		return io.NopCloser(bytes.NewReader(claim.Inline)), generation.AssetMetadata{
			MediaType: claim.MediaType,
			Size:      int64(len(claim.Inline)),
		}, nil
	}
	parsed, err := url.Parse(claim.Locator)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil {
		return nil, generation.AssetMetadata{}, providerError(
			generation.FailureProtocol,
			false,
			false,
			"Provider 产物 URL 无效",
			err,
		)
	}
	if parsed.Scheme != "https" && !(allowHTTP && parsed.Scheme == "http") {
		return nil, generation.AssetMetadata{}, providerError(
			generation.FailureProtocol,
			false,
			false,
			"Provider 产物 URL 必须使用 HTTPS",
			nil,
		)
	}
	if !hostAllowed(parsed.Hostname(), allowedHosts) {
		return nil, generation.AssetMetadata{}, providerError(
			generation.FailureIntegrity,
			false,
			false,
			"Provider 产物主机不在供给项白名单",
			nil,
		)
	}
	if client == nil {
		client = http.DefaultClient
	}
	clone := *client
	previousRedirect := clone.CheckRedirect
	clone.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("产物重定向次数过多")
		}
		if request.URL.Scheme != "https" && !(allowHTTP && request.URL.Scheme == "http") {
			return errors.New("产物重定向到不安全协议")
		}
		if !hostAllowed(request.URL.Hostname(), allowedHosts) {
			return errors.New("产物重定向到白名单外主机")
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		return nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, generation.AssetMetadata{}, err
	}
	response, err := clone.Do(request)
	if err != nil {
		return nil, generation.AssetMetadata{}, providerError(
			generation.FailureTransport,
			true,
			false,
			"下载 Provider 产物失败",
			err,
		)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		response.Body.Close()
		return nil, generation.AssetMetadata{}, providerError(
			generation.FailureUpstreamUnavailable,
			true,
			false,
			"Provider 产物暂不可下载",
			nil,
		)
	}
	return response.Body, generation.AssetMetadata{
		MediaType: response.Header.Get("Content-Type"),
		Size:      response.ContentLength,
	}, nil
}

func hostAllowed(host string, allowed []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.Trim(strings.TrimSpace(candidate), "."))
		if candidate != "" && (host == candidate || strings.HasSuffix(host, "."+candidate)) {
			return true
		}
	}
	return false
}
