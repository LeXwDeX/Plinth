package etl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/legacygeneration"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assets/legacygenerated"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/etl/etltest"
)

func diagnosticTree(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, e := os.Readlink(path)
			result[path] = "link:" + target
			return e
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		sum := sha256.Sum256(data)
		result[path] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func diagnosticInput(t *testing.T, f *etltest.Fixture, id, path, digest string) {
	t.Helper()
	f.Exec(t, `INSERT INTO asset_registry(id,assetType,originalFilename,storedFilename,mimeType,fileSize,sourceSha256,storedSha256,storagePath,uploaderUserId) VALUES (?, 'image', 'PRIVATE_FILENAME', 'PRIVATE_FILENAME', 'image/png', 4, '', ?, ?, 'PRIVATE_OWNER')`, id, digest, path)
}
func diagnosticTask(t *testing.T, f *etltest.Fixture, id, reference string) {
	t.Helper()
	urls, _ := json.Marshal([]string{reference})
	f.Exec(t, `INSERT INTO usage_ledger(id,userId,taskType,taskId,providerId,estimatedCredits,taskStatus,params,resultUrls) VALUES (?, 'PRIVATE_OWNER', 'text-to-image', ?, 'openai', 3, 'SUCCEEDED', '{"prompt":"PRIVATE_PROMPT"}', ?)`, "ledger-"+id, id, string(urls))
}
func diagnosticArgs(f *etltest.Fixture, report string) []string {
	return []string{"diagnose-assets", "--legacy-database", f.DatabasePath(), "--storage-root", filepath.Join(f.Root, "storage"), "--report", report, "--generated-at-utc", "2026-10-15T00:00:00Z"}
}

func TestDiagnoseAssetsReadOnlyRedactedDeterministic(t *testing.T) {
	f := etltest.NewFixture(t)
	png := diagnosticPNG(t)
	digest := sha256.Sum256(png)
	if err := os.WriteFile(filepath.Join(f.Root, "storage/assets/PRIVATE_FILENAME.png"), png, 0600); err != nil {
		t.Fatal(err)
	}
	diagnosticInput(t, f, "PRIVATE_ASSET", "PRIVATE_FILENAME.png", hex.EncodeToString(digest[:]))
	diagnosticTask(t, f, "PRIVATE_TASK", "https://PRIVATE_HOST.invalid/PRIVATE_TOKEN.png?secret=PRIVATE_QUERY")
	before := diagnosticTree(t, f.Root)
	out := t.TempDir()
	report := filepath.Join(out, "report.json")
	var stdout, stderr bytes.Buffer
	if code := Main(diagnosticArgs(f, report), &stdout, &stderr); code != 0 {
		t.Fatalf("diagnosis failed code=%d out=%s err=%s", code, stdout.String(), stderr.String())
	}
	body, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"PRIVATE_ASSET", "PRIVATE_TASK", "PRIVATE_OWNER", "PRIVATE_FILENAME", "PRIVATE_PROMPT", "PRIVATE_TOKEN", "PRIVATE_QUERY", "PRIVATE_HOST", f.Root} {
		if bytes.Contains(append(append(body, stdout.Bytes()...), stderr.Bytes()...), []byte(secret)) {
			t.Fatalf("diagnosis leaked sentinel %q", secret)
		}
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, f.Root)) {
		t.Fatal("source tree was mutated")
	}
	info, _ := os.Stat(report)
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	var doc map[string]any
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["format"] != "asset-resolution-diagnostic-manifest-v1" || doc["snapshot_completeness"] != "unknown" {
		t.Fatalf("metadata=%s", body)
	}
	if !strings.Contains(string(body), "remote_blocked_offline") || !strings.Contains(string(body), "materialized") {
		t.Fatalf("missing reasoned inventory: %s", body)
	}
	second := filepath.Join(out, "second.json")
	stdout.Reset()
	stderr.Reset()
	if code := Main(diagnosticArgs(f, second), &stdout, &stderr); code != 0 {
		t.Fatalf("repeat failed %d %s", code, stderr.String())
	}
	repeated, _ := os.ReadFile(second)
	if !bytes.Equal(body, repeated) {
		t.Fatal("same input produced different report")
	}
}

func diagnosticPNG(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func diagnosticWrite(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
}
func diagnosticRun(t *testing.T, f *etltest.Fixture, extra ...string) diagnosticReport {
	t.Helper()
	path := filepath.Join(t.TempDir(), "report.json")
	args := append(diagnosticArgs(f, path), extra...)
	var out, stderr bytes.Buffer
	if code := Main(args, &out, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var report diagnosticReport
	if err := json.Unmarshal(data, &report); err != nil {
		t.Fatal(err)
	}
	return report
}
func diagnosticFind(t *testing.T, report diagnosticReport, domain, id string) diagnosticEntry {
	t.Helper()
	ref := "sha256:" + diagnosticHash([]byte(id))
	for _, entry := range report.Entries {
		if entry.Domain == domain && entry.ID == ref {
			return entry
		}
	}
	t.Fatalf("entry %s/%s missing: %+v", domain, id, report.Entries)
	return diagnosticEntry{}
}

func TestDiagnoseAssetsInputReasonsAndSHARecovery(t *testing.T) {
	f := etltest.NewFixture(t)
	png := diagnosticPNG(t)
	digest := diagnosticHash(png)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/assets/good.png"), png)
	diagnosticInput(t, f, "sha", "good.png", strings.Repeat("0", 64))
	diagnosticInput(t, f, "missing", "PRIVATE_missing.png", "")
	diagnosticInput(t, f, "escape", "../PRIVATE_escape.png", "")
	diagnosticInput(t, f, "absolute", filepath.Join(t.TempDir(), "PRIVATE_path"), "")
	if err := os.Symlink("PRIVATE_absent.png", filepath.Join(f.Root, "storage/assets/dangling")); err != nil {
		t.Fatal(err)
	}
	diagnosticInput(t, f, "link", "dangling", "")
	outside := filepath.Join(t.TempDir(), "PRIVATE_outside")
	diagnosticWrite(t, outside, png)
	if err := os.Symlink(outside, filepath.Join(f.Root, "storage/assets/outside")); err != nil {
		t.Fatal(err)
	}
	diagnosticInput(t, f, "link-escape", "outside", "")
	diagnosticInput(t, f, "recover", "PRIVATE_old_location.png", digest)
	f.Exec(t, `UPDATE asset_registry SET fileSize=? WHERE id='recover'`, len(png))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/task-inputs/PRIVATE_copy.png"), png)
	before := diagnosticTree(t, f.Root)
	report := diagnosticRun(t, f)
	for id, want := range map[string]string{"sha": "sha256_mismatch", "missing": "missing_file", "escape": "illegal_path", "absolute": "illegal_path", "link": "symlink_unresolved", "link-escape": "illegal_path", "recover": "materialized"} {
		if got := diagnosticFind(t, report, "input_asset", id).Outcome; got != want {
			t.Errorf("%s got %s want %s", id, got, want)
		}
	}
	if got := diagnosticFind(t, report, "input_asset", "recover").Resolver; got != "input_sha256_recovery" {
		t.Fatalf("recovery route=%s", got)
	}
	if report.SourceCounts["asset_registry"] != 7 || report.LoadedCounts["asset_registry"] != 7 {
		t.Fatalf("input denominator=%+v %+v", report.SourceCounts, report.LoadedCounts)
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, f.Root)) {
		t.Fatal("input diagnosis changed bytes or links")
	}
}

func TestDiagnoseAssetsOutputCascadeAndRollup(t *testing.T) {
	f := etltest.NewFixture(t)
	pngBytes := diagnosticPNG(t)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	hosts := legacygeneration.CollectAssetHosts(product.List(""))
	if len(hosts) == 0 {
		t.Fatal("fixture needs declared asset host")
	}
	references := map[string]string{"generated": "/api/gpt-images/PRIVATE_generated.png", "token": "/api/meshy/background-removal/download/PRIVATE_token", "path-cache": "https://" + hosts[0] + "/PRIVATE_path.png?secret=PRIVATE_signature", "url-cache": "https://" + hosts[0] + "/PRIVATE_url.png?secret=PRIVATE_signature", "bad-sidecar": "https://" + hosts[0] + "/PRIVATE_sidecar.png?secret=PRIVATE_signature", "audit": "/api/gpt-images/PRIVATE_audit.png", "conflict": "/api/gpt-images/PRIVATE_duplicate.png", "remote": "https://PRIVATE_CDN.invalid/PRIVATE_remote.png?secret=PRIVATE_signature"}
	diagnosticWrite(t, filepath.Join(f.Root, "storage/generated/PRIVATE_generated.png"), pngBytes)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache/matting/PRIVATE_token.png"), pngBytes)
	for _, id := range []string{"path-cache", "url-cache", "bad-sidecar"} {
		source := references[id]
		parsed, _ := url.Parse(source)
		key := diagnosticHash([]byte(parsed.Path))
		if id == "url-cache" {
			key = diagnosticHash([]byte(source))
		}
		diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", key[:2], key+".png"), pngBytes)
		sidecar := []byte(`{"contentType":"image/png"}`)
		if id == "bad-sidecar" {
			sidecar = []byte(`PRIVATE_INVALID_METADATA`)
		}
		diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", key[:2], key+".meta.json"), sidecar)
	}
	diagnosticWrite(t, filepath.Join(f.Root, "storage/audit/audit_PRIVATE_audit.png"), pngBytes)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/audit/identical/audit_PRIVATE_audit.png"), pngBytes)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/audit/one/conflict_PRIVATE_duplicate.png"), pngBytes)
	image2 := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	image2.SetNRGBA(0, 0, color.NRGBA{R: 200, A: 255})
	var other bytes.Buffer
	if err := png.Encode(&other, image2); err != nil {
		t.Fatal(err)
	}
	diagnosticWrite(t, filepath.Join(f.Root, "storage/audit/two/conflict_PRIVATE_duplicate.png"), other.Bytes())
	for id, source := range references {
		diagnosticTask(t, f, id, source)
	}
	before := diagnosticTree(t, f.Root)
	report := diagnosticRun(t, f)
	for id, want := range map[string]string{"generated": "generated_route", "token": "legacy_download_route", "path-cache": "cache_path_hash", "url-cache": "cache_url_hash", "audit": "audit_filename_recovery", "remote": "remote_offline", "conflict": "audit_filename_recovery"} {
		entry := diagnosticFind(t, report, "task_output_required", id)
		if entry.Resolver != want {
			t.Errorf("%s route=%s outcome=%s want %s", id, entry.Resolver, entry.Outcome, want)
		}
	}
	for id, want := range map[string]string{"generated": "materialized", "token": "materialized", "path-cache": "materialized", "url-cache": "materialized", "audit": "materialized", "conflict": "duplicate_conflict", "bad-sidecar": "sidecar_missing_or_invalid", "remote": "remote_blocked_offline"} {
		entry := diagnosticFind(t, report, "task_output_required", id)
		if entry.Outcome != want {
			t.Errorf("%s outcome=%s want %s candidates=%+v", id, entry.Outcome, want, entry.CandidatePaths)
		}
	}
	if report.SourceCounts["usage_ledger"] != 8 || report.LoadedCounts["usage_ledger"] != 8 || report.SourceCredits != 24 || report.ProjectedOutputs != 8 || !report.SourceInvariantsVerified {
		t.Fatalf("invariants=%+v", report)
	}
	total := 0
	for _, bucket := range report.Rollup {
		total += bucket.Count
	}
	if total != len(report.Entries) {
		t.Fatalf("rollup lost references: %d vs %d", total, len(report.Entries))
	}
	data, _ := json.Marshal(report)
	for _, secret := range []string{"PRIVATE_", f.Root, "prompt"} {
		if bytes.Contains(data, []byte(secret)) {
			t.Fatalf("whole report leaks %s", secret)
		}
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, f.Root)) {
		t.Fatal("cascade diagnosis mutated source")
	}
}

func TestDiagnoseAssetsProtectedOutputAndFailuresNeverLeak(t *testing.T) {
	f := etltest.NewFixture(t)
	protected := t.TempDir()
	diagnosticWrite(t, filepath.Join(protected, "PRIVATE_target.db"), []byte("PRIVATE_EXISTING_DB"))
	cas := filepath.Join(protected, "PRIVATE_CAS")
	if err := os.Mkdir(cas, 0700); err != nil {
		t.Fatal(err)
	}
	before := diagnosticTree(t, protected)
	outside := t.TempDir()
	alias := filepath.Join(outside, "PRIVATE_alias")
	if err := os.Symlink(cas, alias); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(protected, "PRIVATE_target.db"), filepath.Join(cas, "new.json"), filepath.Join(alias, "new.json"), filepath.Join(f.Root, "storage/generated/new.json")} {
		var out, stderr bytes.Buffer
		args := append(diagnosticArgs(f, path), "--protected-root", protected)
		if code := Main(args, &out, &stderr); code == 0 {
			t.Fatalf("unsafe output accepted: %s", path)
		}
		if strings.Contains(out.String()+stderr.String(), "PRIVATE_") || strings.Contains(out.String()+stderr.String(), f.Root) {
			t.Fatal("failure leaked private path")
		}
	}
	for _, args := range [][]string{{"diagnose-assets", "--PRIVATE_FLAG=PRIVATE_SECRET"}, append(diagnosticArgs(f, filepath.Join(outside, "report.json")), "--catalog", filepath.Join(outside, "PRIVATE_missing_catalog")), append(diagnosticArgs(f, filepath.Join(outside, "report.json")), "--legacy-database", filepath.Join(f.Root, "PRIVATE_missing_database"))} {
		var out, stderr bytes.Buffer
		if Main(args, &out, &stderr) == 0 {
			t.Fatal("invalid invocation accepted")
		}
		if strings.Contains(out.String()+stderr.String(), "PRIVATE_") || strings.Contains(out.String()+stderr.String(), f.Root) {
			t.Fatal("CLI error leaked source data")
		}
	}
	if !reflect.DeepEqual(before, diagnosticTree(t, protected)) {
		t.Fatal("protected CAS/target tree changed")
	}
	entries, _ := os.ReadDir(cas)
	if len(entries) != 0 {
		t.Fatal("temporary report entered CAS")
	}
}

func TestDiagnosticSnapshotProofDetectsLaterJournalReplacementAndRewrite(t *testing.T) {
	for _, change := range []string{"wal", "journal", "replace", "rewrite"} {
		t.Run(change, func(t *testing.T) {
			f := etltest.NewFixture(t)
			info, digest, err := diagnosticSnapshotIdentity(f.DatabasePath())
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "wal":
				diagnosticWrite(t, f.DatabasePath()+"-wal", []byte("PRIVATE_WAL"))
			case "journal":
				diagnosticWrite(t, f.DatabasePath()+"-journal", []byte("PRIVATE_JOURNAL"))
			case "replace":
				body, _ := os.ReadFile(f.DatabasePath())
				replacement := f.DatabasePath() + ".new"
				diagnosticWrite(t, replacement, body)
				if err := os.Rename(replacement, f.DatabasePath()); err != nil {
					t.Fatal(err)
				}
			case "rewrite":
				f.Exec(t, `INSERT INTO users(id,updatedAt) VALUES ('PRIVATE_USER',CURRENT_TIMESTAMP)`)
			}
			if diagnosticSourceUnchanged(f.DatabasePath(), info, digest) {
				t.Fatal("changed source was accepted as immutable proof")
			}
		})
	}
}

func TestDiagnoseAssetsOfflineAndFailedProjectionRedacted(t *testing.T) {
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	f := etltest.NewFixture(t)
	diagnosticTask(t, f, "PRIVATE_TASK", server.URL+"/PRIVATE_TOKEN.png?key=PRIVATE_SECRET")
	report := diagnosticRun(t, f)
	if got := diagnosticFind(t, report, "task_output_required", "PRIVATE_TASK").Outcome; got != "remote_blocked_offline" {
		t.Fatalf("remote outcome=%s", got)
	}
	if requests.Load() != 0 {
		t.Fatal("diagnostic contacted remote source")
	}
	f.Exec(t, `UPDATE usage_ledger SET params='PRIVATE_INVALID_JSON'`)
	path := filepath.Join(t.TempDir(), "report.json")
	var out, stderr bytes.Buffer
	if Main(diagnosticArgs(f, path), &out, &stderr) == 0 {
		t.Fatal("malformed private source accepted")
	}
	for _, secret := range []string{"PRIVATE_", f.Root, server.URL} {
		if strings.Contains(out.String()+stderr.String(), secret) {
			t.Fatalf("failed projection leaked %s", secret)
		}
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("failed projection published report")
	}
	if requests.Load() != 0 {
		t.Fatal("failed diagnostic contacted remote source")
	}
}

func TestDiagnoseAssetsSelectedCacheRejectsUnsafeContent(t *testing.T) {
	f := etltest.NewFixture(t)
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	hosts := legacygeneration.CollectAssetHosts(product.List(""))
	source := "https://" + hosts[0] + "/PRIVATE_unsafe.png?secret=PRIVATE_SECRET"
	diagnosticTask(t, f, "PRIVATE_TASK", source)
	parsed, _ := url.Parse(source)
	key := diagnosticHash([]byte(parsed.Path))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", key[:2], key+".png"), []byte("PRIVATE_INVALID_IMAGE"))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", key[:2], key+".meta.json"), []byte(`{"contentType":"image/png"}`))
	// A later valid URL cache and audit copy must not hide rejected bytes in the selected cache.
	fallback := diagnosticHash([]byte(source))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", fallback[:2], fallback+".png"), diagnosticPNG(t))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", fallback[:2], fallback+".meta.json"), []byte(`{"contentType":"image/png"}`))
	diagnosticWrite(t, filepath.Join(f.Root, "storage/audit/PRIVATE_TASK_PRIVATE_unsafe.png"), diagnosticPNG(t))
	report := diagnosticRun(t, f)
	entry := diagnosticFind(t, report, "task_output_required", "PRIVATE_TASK")
	if entry.Outcome != "content_rejected" || entry.Resolver != "cache_path_hash" {
		t.Fatalf("unsafe selected cache hidden: %+v", entry)
	}
}

func TestDiagnoseAssetsUsesEachResolverLinkBoundary(t *testing.T) {
	f := etltest.NewFixture(t)
	pngBytes := diagnosticPNG(t)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache/PRIVATE_copy.png"), pngBytes)
	if err := os.Symlink("../cache/PRIVATE_copy.png", filepath.Join(f.Root, "storage/assets/PRIVATE_cross_link")); err != nil {
		t.Fatal(err)
	}
	diagnosticInput(t, f, "cross", "PRIVATE_cross_link", "")
	if body, _, err := openLegacyStorageFile(filepath.Join(f.Root, "storage/assets"), "PRIVATE_cross_link"); err == nil {
		body.Close()
		t.Fatal("fixture did not cross the actual input boundary")
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	hosts := legacygeneration.CollectAssetHosts(product.List(""))
	source := "https://" + hosts[0] + "/PRIVATE_cached.png"
	parsed, _ := url.Parse(source)
	key := diagnosticHash([]byte(parsed.Path))
	paths := map[string]string{"generated": "generated/PRIVATE_link.png", "token": "cache/matting/PRIVATE_token.png", "cache": "cache/" + key[:2] + "/" + key + ".png"}
	refs := map[string]string{"generated": "/api/gpt-images/PRIVATE_link.png", "token": "/api/meshy/background-removal/download/PRIVATE_token", "cache": source}
	for id, path := range paths {
		path = filepath.Join(f.Root, "storage", path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(f.Root, "storage/cache/PRIVATE_copy.png"), path); err != nil {
			t.Fatal(err)
		}
		diagnosticTask(t, f, id, refs[id])
	}
	diagnosticWrite(t, filepath.Join(f.Root, "storage/cache", key[:2], key+".meta.json"), []byte(`{"contentType":"image/png"}`))
	report := diagnosticRun(t, f)
	if entry := diagnosticFind(t, report, "input_asset", "cross"); entry.Outcome != "illegal_path" {
		t.Errorf("input link escaped assets boundary: %+v", entry)
	}
	for id := range paths {
		if entry := diagnosticFind(t, report, "task_output_required", id); entry.Outcome == "materialized" {
			t.Errorf("%s reader would reject linked main file: %+v", id, entry)
		}
	}
}

func TestDiagnosticOutputOversizePreservesRouteFallback(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := product.Application()
	assets := app.Administration.Generations.Assets
	f := etltest.NewFixture(t)
	f.Root, err = filepath.EvalSymlinks(f.Root)
	if err != nil {
		t.Fatal(err)
	}
	small := diagnosticPNG(t)
	limit := int64(len(small) + 1)
	large := append(append([]byte{}, small...), []byte("PRIVATE_OVERSIZED_PADDING")...)
	generated, err := legacygenerated.New(filepath.Join(f.Root, "storage/generated"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := legacygeneration.NewReader(legacygeneration.ReaderConfig{
		Native: administration.GenerationAssetReaderFunc(func(context.Context, administration.GenerationAssetRequest) (administration.GenerationAssetContent, error) {
			panic("unexpected native port")
		}),
		GeneratedImages:           generated,
		GeneratedImageRoute:       app.Runtime.HTTP.LegacyRoutes.GeneratedImage,
		CacheRoot:                 filepath.Join(f.Root, "storage/cache"),
		CachePrefixCharacters:     assets.LegacyCachePrefixCharacters,
		CacheMetadataSuffix:       assets.LegacyCacheMetadataSuffix,
		CacheContentTypeField:     assets.LegacyCacheContentTypeField,
		AllowedHosts:              legacygeneration.CollectAssetHosts(product.List("")),
		DefaultMediaType:          assets.DefaultMediaType,
		UnknownExtension:          assets.UnknownExtension,
		MaximumBytes:              limit,
		RemoteTimeoutMilliseconds: assets.RemoteDownloadTimeoutMilliseconds,
		MaximumRedirects:          assets.MaximumRemoteRedirects,
		MediaTypeExtensions:       assets.MediaTypeExtensions,
		AssetDownloads:            legacygeneration.CollectAssetDownloads(app.Capabilities),
	})
	if err != nil {
		t.Fatal(err)
	}
	hosts := legacygeneration.CollectAssetHosts(product.List(""))
	refs := map[string]string{"generated": "/api/gpt-images/PRIVATE_g.png", "token": "/api/meshy/background-removal/download/PRIVATE_t", "cache": "https://" + hosts[0] + "/PRIVATE_c.png"}
	paths := map[string]string{"generated": "generated/PRIVATE_g.png", "token": "cache/matting/PRIVATE_t.png"}
	parsed, _ := url.Parse(refs["cache"])
	key := diagnosticHash([]byte(parsed.Path))
	paths["cache"] = "cache/" + key[:2] + "/" + key + ".png"
	for id, path := range paths {
		diagnosticWrite(t, filepath.Join(f.Root, "storage", path), large)
		name := map[string]string{"generated": "PRIVATE_g.png", "token": "PRIVATE_t", "cache": "PRIVATE_c.png"}[id]
		diagnosticWrite(t, filepath.Join(f.Root, "storage/audit", id+"_"+name), small)
	}
	audit, err := newLegacyAuditAssetRecovery(filepath.Join(f.Root, "storage"), app.Runtime.LegacyMigration.AuditAssetRecovery, assets)
	if err != nil {
		t.Fatal(err)
	}
	scanner := newDiagnosticScanner(filepath.Join(f.Root, "storage"), limit)
	for id, source := range refs {
		entry := newDiagnosticEntry("task_output_required", id, source)
		scanner.output(&entry, id, source, reader, audit, administration.GenerationAsset{Role: "image", MediaType: "image/png"})
		if id == "cache" {
			if entry.Outcome != "materialized" || entry.Resolver != "audit_filename_recovery" {
				t.Errorf("cache miss lost permitted audit fallback: %+v", entry)
			}
		} else if entry.Outcome != "too_large" || entry.Resolver != map[string]string{"generated": "generated_route", "token": "legacy_download_route"}[id] {
			t.Errorf("%s oversized direct route incorrectly fell back: %+v", id, entry)
		}
	}
	// The output package limit is not an input-file limit in strict migration.
	if outcome, _, _ := scanner.probe(filepath.Join(f.Root, "storage", paths["generated"]), false, nil); outcome != "materialized" {
		t.Errorf("input-style streaming hash incorrectly applies output limit: %s", outcome)
	}
}

func TestDiagnoseAssetsRequiredRoleUsesSuccessfulOutput(t *testing.T) {
	f := etltest.NewFixture(t)
	diagnosticTask(t, f, "PRIVATE_TASK", "/api/gpt-images/PRIVATE_missing.png")
	f.Exec(t, `UPDATE usage_ledger SET resultUrls='["/api/gpt-images/PRIVATE_missing.png","/api/gpt-images/PRIVATE_good.png"]'`)
	diagnosticWrite(t, filepath.Join(f.Root, "storage/generated/PRIVATE_good.png"), diagnosticPNG(t))
	report := diagnosticRun(t, f)
	found := 0
	for _, entry := range report.Entries {
		if entry.ID != "sha256:"+diagnosticHash([]byte("PRIVATE_TASK")) {
			continue
		}
		found++
		want := "task_output_optional"
		if entry.Outcome == "materialized" {
			want = "task_output_required"
		}
		if entry.Domain != want {
			t.Errorf("role assigned by declaration order instead of successful output: %+v", entry)
		}
	}
	if found != 2 || report.ProjectedOutputs != 2 || report.SourceCounts["usage_ledger"] != 1 {
		t.Fatalf("wrong independent denominators: found=%d report=%+v", found, report)
	}
}
