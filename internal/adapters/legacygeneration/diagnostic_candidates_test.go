package legacygeneration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type diagnosticForbiddenImagePort struct{}

func (diagnosticForbiddenImagePort) Open(context.Context, string) (io.ReadCloser, int64, error) {
	panic("diagnostic opened asset port")
}

type diagnosticForbiddenTransport struct{}

func (diagnosticForbiddenTransport) RoundTrip(*http.Request) (*http.Response, error) {
	panic("diagnostic attempted network")
}

func TestDiagnosticCandidatesUseExistingRulesWithoutIO(t *testing.T) {
	reader := newTestLegacyReader(t, ReaderConfig{AssetDownloads: []catalog.LegacyAssetDownloadSchema{{Path: "/custom/remove/{job}", TokenParameter: "job", FilenameTemplate: "{job}.png", MediaType: "image/png", CacheSubdirectory: "configured-results", CacheFilenameTemplate: "result-{job}.png"}}})
	reader.native = administration.GenerationAssetReaderFunc(func(context.Context, administration.GenerationAssetRequest) (administration.GenerationAssetContent, error) {
		panic("diagnostic opened native port")
	})
	reader.generatedImages = diagnosticForbiddenImagePort{}
	reader.httpClient = &http.Client{Transport: diagnosticForbiddenTransport{}}
	token := reader.DiagnosticCandidates("/custom/remove/PRIVATE_TOKEN", t.TempDir())
	if len(token) != 1 || token[0].Resolver != "legacy_download_route" || filepath.Base(token[0].Path) != "result-PRIVATE_TOKEN.png" {
		t.Fatalf("token candidates=%+v", token)
	}
	source := "https://assets.test/PRIVATE_PATH.png?token=PRIVATE_SIGNATURE"
	reader.allowedHosts = map[string]struct{}{"assets.test": {}}
	cache := reader.DiagnosticCandidates(source, t.TempDir())
	if len(cache) != 2 || cache[0].Resolver != "cache_path_hash" || cache[1].Resolver != "cache_url_hash" || !strings.HasSuffix(cache[0].MetadataPath, reader.cacheMetadataSuffix) {
		t.Fatalf("cache=%+v", cache)
	}
	body, _ := json.Marshal(token)
	if strings.Contains(string(body), "PRIVATE_TOKEN") {
		t.Fatal("private trace paths were accidentally serializable")
	}
	reader.cachePrefixCharacters = 100
	if got := reader.DiagnosticCandidates(source, t.TempDir()); len(got) != 0 {
		t.Fatalf("impossible cache prefix should produce no candidate: %+v", got)
	}
	if got := reader.DiagnosticCandidates("", t.TempDir()); len(got) != 0 {
		t.Fatal("empty source touched a native route")
	}
}
