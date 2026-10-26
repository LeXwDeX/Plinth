package legacyassetproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestSourceFetchesOnceThenReadsLegacyCompatibleDiskCache(
	t *testing.T,
) {
	schema := sourceSchemaFixture()
	requests := 0
	source, err := NewSource(SourceConfig{
		Root:         t.TempDir(),
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Transport: roundTripFunc(func(request *http.Request) (
			*http.Response,
			error,
		) {
			requests++
			return sourceResponse(
				request,
				http.StatusOK,
				"image/webp",
				[]byte("webp-image"),
			), nil
		}),
		Clock: func() time.Time {
			return time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := assetdelivery.SourceRequest{
		URL: "https://assets.meshy.ai/images/a.webp?Signature=one",
	}

	first, err := source.Fetch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	firstBody := readSourceBody(t, first)
	if string(firstBody) != "webp-image" ||
		first.MediaType != "image/webp" ||
		first.Size != int64(len(firstBody)) ||
		first.CacheStatus != schema.Cache.MissValue {
		t.Fatalf("first fetch = %#v body=%q", first, firstBody)
	}

	second, err := source.Fetch(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	secondBody := readSourceBody(t, second)
	if string(secondBody) != "webp-image" ||
		second.CacheStatus != schema.Cache.HitValue ||
		requests != 1 {
		t.Fatalf(
			"second fetch = %#v body=%q requests=%d",
			second,
			secondBody,
			requests,
		)
	}

	hash := sourceHash(request.URL)
	metadataPath := filepath.Join(
		source.root,
		hash[:schema.Cache.ShardCharacters],
		hash+schema.Cache.MetadataSuffix,
	)
	metadataContent, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	var metadata map[string]any
	if err := json.Unmarshal(metadataContent, &metadata); err != nil {
		t.Fatal(err)
	}
	fields := schema.Cache.MetadataFields
	if metadata[fields.URL] != request.URL ||
		metadata[fields.ContentType] != "image/webp" ||
		int(metadata[fields.Size].(float64)) != len(firstBody) ||
		metadata[fields.CachedAt] != "2026-07-27T12:00:00Z" {
		t.Fatalf("legacy cache metadata = %#v", metadata)
	}
}

func TestSourceReadsCacheWrittenByOriginalRuntime(t *testing.T) {
	schema := sourceSchemaFixture()
	root := t.TempDir()
	targetURL := "https://assets.meshy.ai/images/original.webp"
	hash := sourceHash(targetURL)
	directory := filepath.Join(
		root,
		hash[:schema.Cache.ShardCharacters],
	)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, hash+".webp"),
		[]byte("legacy-cache"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	fields := schema.Cache.MetadataFields
	metadata, err := json.Marshal(map[string]any{
		fields.URL:         targetURL,
		fields.ContentType: "image/webp",
		fields.Size:        len("legacy-cache"),
		fields.CachedAt:    "2026-07-26T00:00:00.000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, hash+schema.Cache.MetadataSuffix),
		metadata,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(SourceConfig{
		Root:         root,
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Transport: roundTripFunc(func(*http.Request) (
			*http.Response,
			error,
		) {
			return nil, errors.New("network must not be used")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := source.Fetch(
		context.Background(),
		assetdelivery.SourceRequest{URL: targetURL},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := readSourceBody(t, content)
	if string(body) != "legacy-cache" ||
		content.MediaType != "image/webp" ||
		content.CacheStatus != schema.Cache.HitValue {
		t.Fatalf("legacy content = %#v body=%q", content, body)
	}
}

func TestSourceReadsOriginalPathHashedCacheAcrossSignedURLRefresh(
	t *testing.T,
) {
	schema := sourceSchemaFixture()
	writableRoot := t.TempDir()
	legacyRoot := t.TempDir()
	cachedURL := "https://assets.meshy.ai/images/original.webp?Signature=old"
	targetURL := "https://assets.meshy.ai/images/original.webp?Signature=current"
	hash := sourceHash("/images/original.webp")
	directory := filepath.Join(
		legacyRoot,
		hash[:schema.Cache.ShardCharacters],
	)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, hash+".webp"),
		[]byte("legacy-path-cache"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	fields := schema.Cache.MetadataFields
	metadata, err := json.Marshal(map[string]any{
		fields.URL:         cachedURL,
		fields.ContentType: "image/webp",
		fields.Size:        len("legacy-path-cache"),
		fields.CachedAt:    "2026-07-26T00:00:00.000Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(directory, hash+schema.Cache.MetadataSuffix),
		metadata,
		0o644,
	); err != nil {
		t.Fatal(err)
	}
	source, err := NewSource(SourceConfig{
		Root:          writableRoot,
		ReadOnlyRoots: []string{legacyRoot},
		Schema:        schema,
		AllowedHosts:  []string{"assets.meshy.ai"},
		Transport: roundTripFunc(func(*http.Request) (
			*http.Response,
			error,
		) {
			return nil, errors.New("network must not be used")
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := source.Fetch(
		context.Background(),
		assetdelivery.SourceRequest{URL: targetURL},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := readSourceBody(t, content)
	if string(body) != "legacy-path-cache" ||
		content.MediaType != "image/webp" ||
		content.CacheStatus != schema.Cache.HitValue {
		t.Fatalf("legacy path content = %#v body=%q", content, body)
	}
}

func TestSourceRefreshesExpiredSignedURLThroughInjectedPort(
	t *testing.T,
) {
	schema := sourceSchemaFixture()
	refresher := &refresherStub{
		url: "https://assets.meshy.ai/images/a.webp?Signature=fresh",
	}
	requests := make([]string, 0, 2)
	source, err := NewSource(SourceConfig{
		Root:         t.TempDir(),
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Refresher:    refresher,
		Transport: roundTripFunc(func(request *http.Request) (
			*http.Response,
			error,
		) {
			requests = append(requests, request.URL.String())
			if strings.Contains(request.URL.RawQuery, "expired") {
				return sourceResponse(
					request,
					http.StatusForbidden,
					"text/plain",
					[]byte("expired"),
				), nil
			}
			return sourceResponse(
				request,
				http.StatusOK,
				"image/webp",
				[]byte("fresh-image"),
			), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	content, err := source.Fetch(
		context.Background(),
		assetdelivery.SourceRequest{
			URL:    "https://assets.meshy.ai/images/a.webp?Signature=expired",
			TaskID: "legacy-task",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := readSourceBody(t, content)
	if string(body) != "fresh-image" ||
		content.CacheStatus != schema.Cache.RefreshedValue ||
		refresher.taskID != "legacy-task" ||
		len(requests) != 2 ||
		requests[1] != refresher.url {
		t.Fatalf(
			"refreshed content=%#v body=%q refresher=%#v requests=%v",
			content,
			body,
			refresher,
			requests,
		)
	}
}

func TestSourceRejectsRedirectOutsideProviderAssetHosts(t *testing.T) {
	schema := sourceSchemaFixture()
	source, err := NewSource(SourceConfig{
		Root:         t.TempDir(),
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Transport: roundTripFunc(func(request *http.Request) (
			*http.Response,
			error,
		) {
			if request.URL.Hostname() == "assets.meshy.ai" {
				response := sourceResponse(
					request,
					http.StatusFound,
					"text/plain",
					nil,
				)
				response.Header.Set(
					"Location",
					"https://internal.example/secrets",
				)
				return response, nil
			}
			return sourceResponse(
				request,
				http.StatusOK,
				"text/plain",
				[]byte("secret"),
			), nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = source.Fetch(
		context.Background(),
		assetdelivery.SourceRequest{
			URL: "https://assets.meshy.ai/redirect",
		},
	)
	if err == nil {
		t.Fatal("越权重定向被资产代理接受")
	}
}

func TestSourceClassifies404AndEnforcesSchemaByteLimit(t *testing.T) {
	schema := sourceSchemaFixture()
	schema.HTTP.ResponseMaxBytes = 4
	for name, testCase := range map[string]struct {
		response *http.Response
		want     error
	}{
		"not found": {
			response: sourceResponse(
				nil,
				http.StatusNotFound,
				"text/plain",
				nil,
			),
			want: assetdelivery.ErrSourceNotFound,
		},
		"too large": {
			response: sourceResponse(
				nil,
				http.StatusOK,
				"application/octet-stream",
				[]byte("12345"),
			),
			want: ErrSourceTooLarge,
		},
	} {
		t.Run(name, func(t *testing.T) {
			source, err := NewSource(SourceConfig{
				Root:         t.TempDir(),
				Schema:       schema,
				AllowedHosts: []string{"assets.meshy.ai"},
				Transport: roundTripFunc(func(request *http.Request) (
					*http.Response,
					error,
				) {
					testCase.response.Request = request
					return testCase.response, nil
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			_, fetchErr := source.Fetch(
				context.Background(),
				assetdelivery.SourceRequest{
					URL: "https://assets.meshy.ai/a.bin",
				},
			)
			if !errors.Is(fetchErr, testCase.want) {
				t.Fatalf(
					"Fetch error = %v, want %v",
					fetchErr,
					testCase.want,
				)
			}
		})
	}
}

func sourceSchemaFixture() catalog.AssetProxyRuntimeSchema {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		panic(err)
	}
	return taskCatalog.Application().Runtime.AssetDelivery.Proxy
}

func readSourceBody(
	t *testing.T,
	content assetdelivery.SourceContent,
) []byte {
	t.Helper()
	defer content.Body.Close()
	body, err := io.ReadAll(content.Body)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func sourceResponse(
	request *http.Request,
	status int,
	contentType string,
	body []byte,
) *http.Response {
	header := make(http.Header)
	if contentType != "" {
		header.Set("Content-Type", contentType)
	}
	return &http.Response{
		StatusCode:    status,
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       request,
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return function(request)
}

type refresherStub struct {
	url      string
	err      error
	taskID   string
	staleURL string
}

func (refresher *refresherStub) Refresh(
	_ context.Context,
	taskID string,
	staleURL string,
) (string, error) {
	refresher.taskID = taskID
	refresher.staleURL = staleURL
	return refresher.url, refresher.err
}
