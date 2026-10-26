package assetdelivery

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProxyRejectsInvalidOrForbiddenSourceBeforeExternalFetch(
	t *testing.T,
) {
	schema := proxySchemaFixture()
	source := &proxySourceStub{}
	proxy, err := NewProxy(ProxyConfig{
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai", "tripo3d.ai"},
		Source:       source,
		Thumbnailer:  thumbnailerStub{},
		Stamper:      stamperStub{},
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, testCase := range map[string]struct {
		request Request
		want    error
	}{
		"missing": {
			request: Request{},
			want:    ErrMissingURL,
		},
		"malformed": {
			request: Request{URL: "not-a-url"},
			want:    ErrInvalidURL,
		},
		"scheme": {
			request: Request{
				URL: "file:///etc/passwd",
			},
			want: ErrInvalidURL,
		},
		"host": {
			request: Request{
				URL: "https://example.com/a.glb",
			},
			want: ErrForbiddenHost,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, proxyErr := proxy.Get(
				context.Background(),
				testCase.request,
			)
			if !errors.Is(proxyErr, testCase.want) {
				t.Fatalf(
					"Get error = %v, want %v",
					proxyErr,
					testCase.want,
				)
			}
		})
	}
	if source.calls != 0 {
		t.Fatalf("external fetch calls = %d, want 0", source.calls)
	}
}

func TestProxyServesAllowedProviderSubdomainAndStampsSupportedImage(
	t *testing.T,
) {
	schema := proxySchemaFixture()
	source := &proxySourceStub{
		content: SourceContent{
			Body:        io.NopCloser(bytes.NewReader([]byte("original"))),
			MediaType:   "image/png; charset=binary",
			Size:        8,
			CacheStatus: "HIT",
		},
	}
	stamper := &recordingStamper{result: []byte("stamped")}
	proxy, err := NewProxy(ProxyConfig{
		Schema:       schema,
		AllowedHosts: []string{"tripo3d.ai"},
		Source:       source,
		Thumbnailer:  thumbnailerStub{},
		Stamper:      stamper,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := proxy.Get(context.Background(), Request{
		URL:    "https://files.eu.tripo3d.ai/models/a.glb",
		TaskID: "task-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "stamped" ||
		result.MediaType != "image/png" ||
		result.Size != int64(len(body)) ||
		result.CacheStatus != "HIT" ||
		result.Thumbnail {
		t.Fatalf("proxy result = %#v body=%q", result, body)
	}
	if source.request.TaskID != "task-1" ||
		stamper.mediaType != "image/png" ||
		stamper.metadata.Artist != schema.Metadata.Artist {
		t.Fatalf(
			"source request = %#v, stamp = %#v",
			source.request,
			stamper,
		)
	}
}

func TestProxyThumbnailUsesSchemaPolicyAndDoesNotStamp(t *testing.T) {
	schema := proxySchemaFixture()
	source := &proxySourceStub{
		content: SourceContent{
			Body:        io.NopCloser(bytes.NewReader([]byte("original"))),
			MediaType:   "image/jpeg",
			Size:        8,
			CacheStatus: schema.Cache.HitValue,
		},
	}
	thumbnailer := &recordingThumbnailer{
		cachedErr: errors.New("not cached"),
		created: ThumbnailContent{
			Content:     []byte("thumbnail"),
			MediaType:   "image/png",
			CacheStatus: schema.Cache.MissValue,
		},
	}
	stamper := &recordingStamper{result: []byte("unexpected")}
	proxy, err := NewProxy(ProxyConfig{
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Source:       source,
		Thumbnailer:  thumbnailer,
		Stamper:      stamper,
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := proxy.Get(context.Background(), Request{
		URL:  "https://assets.meshy.ai/a.webp",
		Size: schema.Thumbnail.RequestValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "thumbnail" ||
		result.MediaType != schema.Thumbnail.OutputMediaType ||
		result.CacheStatus != schema.Cache.MissValue ||
		!result.Thumbnail ||
		thumbnailer.maximumDimension !=
			schema.Thumbnail.MaximumDimension ||
		thumbnailer.createCalls != 1 ||
		stamper.calls != 0 {
		t.Fatalf(
			"proxy result = %#v body=%q thumbnailer=%#v stamper=%#v",
			result,
			body,
			thumbnailer,
			stamper,
		)
	}
}

func TestProxyServesCachedThumbnailWithoutOpeningOriginalSource(
	t *testing.T,
) {
	schema := proxySchemaFixture()
	source := &proxySourceStub{
		err: errors.New("source must not be opened"),
	}
	thumbnailer := &recordingThumbnailer{
		cached: ThumbnailContent{
			Content:     []byte("cached-thumbnail"),
			MediaType:   schema.Thumbnail.OutputMediaType,
			CacheStatus: schema.Cache.HitValue,
		},
	}
	proxy, err := NewProxy(ProxyConfig{
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Source:       source,
		Thumbnailer:  thumbnailer,
		Stamper:      stamperStub{},
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := proxy.Get(context.Background(), Request{
		URL:  "https://assets.meshy.ai/a.webp",
		Size: schema.Thumbnail.RequestValue,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer result.Body.Close()
	body, err := io.ReadAll(result.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "cached-thumbnail" ||
		!result.Thumbnail ||
		result.CacheStatus != schema.Cache.HitValue ||
		source.calls != 0 ||
		thumbnailer.cachedCalls != 1 ||
		thumbnailer.createCalls != 0 {
		t.Fatalf(
			"cached thumbnail result=%#v body=%q source=%#v thumbnailer=%#v",
			result,
			body,
			source,
			thumbnailer,
		)
	}
}

func TestProxyRejectsInvalidThumbnailWithoutDecodingAgain(
	t *testing.T,
) {
	schema := proxySchemaFixture()
	source := &proxySourceStub{
		content: SourceContent{
			Body:        io.NopCloser(bytes.NewReader([]byte("original"))),
			MediaType:   "image/webp",
			Size:        8,
			CacheStatus: "HIT",
		},
	}
	stamper := &recordingStamper{result: []byte("fallback")}
	proxy, err := NewProxy(ProxyConfig{
		Schema:       schema,
		AllowedHosts: []string{"assets.meshy.ai"},
		Source:       source,
		Thumbnailer: &recordingThumbnailer{
			cachedErr: errors.New("not cached"),
			createErr: errors.New("unsupported input"),
		},
		Stamper: stamper,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = proxy.Get(context.Background(), Request{URL: "https://assets.meshy.ai/a.webp", Size: schema.Thumbnail.RequestValue})
	if !errors.Is(err, ErrUnavailable) || stamper.calls != 0 {
		t.Fatalf("thumbnail error=%v stamp calls=%d", err, stamper.calls)
	}

}

func TestProxyClassifiesMissingAndUnavailableExternalAsset(t *testing.T) {
	schema := proxySchemaFixture()
	for name, testCase := range map[string]struct {
		sourceErr error
		want      error
	}{
		"not found": {
			sourceErr: ErrSourceNotFound,
			want:      ErrNotFound,
		},
		"unavailable": {
			sourceErr: errors.New("upstream failed"),
			want:      ErrUnavailable,
		},
	} {
		t.Run(name, func(t *testing.T) {
			proxy, err := NewProxy(ProxyConfig{
				Schema:       schema,
				AllowedHosts: []string{"assets.meshy.ai"},
				Source: &proxySourceStub{
					err: testCase.sourceErr,
				},
				Thumbnailer: thumbnailerStub{},
				Stamper:     stamperStub{},
			})
			if err != nil {
				t.Fatal(err)
			}
			_, proxyErr := proxy.Get(context.Background(), Request{
				URL: "https://assets.meshy.ai/a.glb",
			})
			if !errors.Is(proxyErr, testCase.want) {
				t.Fatalf(
					"Get error = %v, want %v",
					proxyErr,
					testCase.want,
				)
			}
		})
	}
}

func proxySchemaFixture() catalog.AssetProxyRuntimeSchema {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		panic(err)
	}
	return taskCatalog.Application().Runtime.AssetDelivery.Proxy
}

type proxySourceStub struct {
	content SourceContent
	err     error
	calls   int
	request SourceRequest
}

func (source *proxySourceStub) Fetch(
	_ context.Context,
	request SourceRequest,
) (SourceContent, error) {
	source.calls++
	source.request = request
	return source.content, source.err
}

type thumbnailerStub struct{}

func (thumbnailerStub) Cached(
	context.Context,
	string,
) (ThumbnailContent, error) {
	return ThumbnailContent{}, errors.New("thumbnail unavailable")
}

func (thumbnailerStub) Create(
	context.Context,
	ThumbnailRequest,
) (ThumbnailContent, error) {
	return ThumbnailContent{}, errors.New("thumbnail unavailable")
}

type recordingThumbnailer struct {
	cached           ThumbnailContent
	cachedErr        error
	created          ThumbnailContent
	createErr        error
	cachedCalls      int
	createCalls      int
	maximumDimension int
}

func (thumbnailer *recordingThumbnailer) Cached(
	_ context.Context,
	_ string,
) (ThumbnailContent, error) {
	thumbnailer.cachedCalls++
	return thumbnailer.cached, thumbnailer.cachedErr
}

func (thumbnailer *recordingThumbnailer) Create(
	_ context.Context,
	request ThumbnailRequest,
) (ThumbnailContent, error) {
	thumbnailer.createCalls++
	thumbnailer.maximumDimension = request.MaximumDimension
	return thumbnailer.created, thumbnailer.createErr
}

type stamperStub struct{}

func (stamperStub) Stamp(
	content []byte,
	_ string,
	_ catalog.AssetProxyMetadataSchema,
) ([]byte, error) {
	return content, nil
}

type recordingStamper struct {
	result    []byte
	err       error
	calls     int
	mediaType string
	metadata  catalog.AssetProxyMetadataSchema
}

func (stamper *recordingStamper) Stamp(
	_ []byte,
	mediaType string,
	metadata catalog.AssetProxyMetadataSchema,
) ([]byte, error) {
	stamper.calls++
	stamper.mediaType = mediaType
	stamper.metadata = metadata
	return stamper.result, stamper.err
}
