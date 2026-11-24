package register_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/register"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
)

const (
	repoRoot         = "../../../.."
	markdownPath     = repoRoot + "/docs/fake-register.md"
	registerJSONPath = "register.json"
	wireCoveragePath = repoRoot + "/internal/testutil/wirefixture/testdata/wire-coverage.generated.json"
	updateEnv        = "UPDATE_FAKE_REGISTER"
)

var (
	datePattern     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	templateSegment = regexp.MustCompile(`\{[a-z_]+\}`)
)

// requiredCorrections are the Offerings the wire coverage matrix lists under a
// real unit only because they share its endpoint with a sampled model: no real
// request of their own model was ever captured. The register must record their
// behavior per model, as not real (#573, TestWireCoverageDoesNotCarryEvidenceAcrossModels).
var requiredCorrections = []string{
	"tripo/tripo-p2/text-to-3d",
	"tripo/tripo-p2/image-to-3d",
	"tripo/tripo-p2-multiview/image-to-3d",
}

func load(t *testing.T) register.Register {
	t.Helper()
	reg, err := register.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Entries) == 0 {
		t.Fatal("register has no entries")
	}
	return reg
}

// versions maps "provider/api" to the registered fake version.
func versions() map[string]fakeupstream.Version {
	out := map[string]fakeupstream.Version{}
	for _, version := range registry.Versions() {
		out[version.Provider+"/"+version.API] = version
	}
	return out
}

func validDate(value string) bool {
	if !datePattern.MatchString(value) {
		return false
	}
	_, err := time.Parse("2006-01-02", value)
	return err == nil
}

// Every entry names a registered version and existing routes (so a removed or
// renamed route makes its entries fail), a known category and status, and the
// fields its status requires: samples for real, a dated basis and the reason
// for constructed, a plan and issues for a gap.
func TestRegisterEntriesAreWellFormed(t *testing.T) {
	reg := load(t)
	if reg.Version != 1 {
		t.Errorf("register version %d, want 1", reg.Version)
	}
	known := versions()
	seen := map[string]bool{}
	for _, entry := range reg.Entries {
		id := entry.ID
		if id == "" || seen[id] {
			t.Errorf("entry id %q is empty or repeated", id)
		}
		seen[id] = true
		if !strings.HasPrefix(id, entry.Provider+"-"+entry.API+"/") {
			t.Errorf("%s: id must start with %q", id, entry.Provider+"-"+entry.API+"/")
		}
		version, ok := known[entry.Version()]
		if !ok {
			t.Errorf("%s: %s is not a registered fake version", id, entry.Version())
			continue
		}
		if len(entry.Endpoints) == 0 {
			t.Errorf("%s: no endpoint", id)
		}
		for _, endpoint := range entry.Endpoints {
			if !slices.ContainsFunc(version.Endpoints, func(e fakeupstream.Endpoint) bool { return e.Key() == endpoint }) {
				t.Errorf("%s: %s has no route %q (stale entry?)", id, entry.Version(), endpoint)
			}
		}
		if !slices.Contains(register.Categories, entry.Category) {
			t.Errorf("%s: unknown category %q", id, entry.Category)
		}
		if strings.TrimSpace(entry.Behavior) == "" {
			t.Errorf("%s: no behavior", id)
		}
		switch entry.Status {
		case register.StatusReal:
			if len(entry.Samples) == 0 {
				t.Errorf("%s: a real entry needs samples", id)
			}
			if entry.WhyNotReal != "" || entry.Plan != "" || len(entry.Issues) > 0 {
				t.Errorf("%s: a real entry carries no why_not_real, plan or issues", id)
			}
			if entry.Correction != nil {
				t.Errorf("%s: a wire coverage correction cannot be real", id)
			}
		case register.StatusConstructed:
			if len(entry.Samples) > 0 {
				t.Errorf("%s: a constructed entry cites no samples (it would be real)", id)
			}
			if len(entry.Basis) == 0 || !validDate(entry.CheckedAt) || strings.TrimSpace(entry.WhyNotReal) == "" {
				t.Errorf("%s: a constructed entry needs a basis, a checked_at date and why_not_real", id)
			}
			if entry.Plan != "" || len(entry.Issues) > 0 {
				t.Errorf("%s: plan and issues belong to a gap", id)
			}
		case register.StatusGap:
			if len(entry.Samples) > 0 {
				t.Errorf("%s: a gap cites no samples", id)
			}
			if strings.TrimSpace(entry.Plan) == "" || len(entry.Issues) == 0 {
				t.Errorf("%s: a gap needs a plan and issues", id)
			}
			if entry.CheckedAt != "" && !validDate(entry.CheckedAt) {
				t.Errorf("%s: checked_at %q", id, entry.CheckedAt)
			}
		default:
			t.Errorf("%s: unknown status %q", id, entry.Status)
		}
		for _, basis := range entry.Basis {
			if strings.TrimSpace(basis.Source) == "" {
				t.Errorf("%s: a basis without a source", id)
			}
			if strings.Contains(basis.Source, "://") && !strings.HasPrefix(basis.Source, "https://") {
				t.Errorf("%s: basis URL %q is not https", id, basis.Source)
			}
		}
		for _, issue := range entry.Issues {
			if issue <= 0 {
				t.Errorf("%s: issue %d", id, issue)
			}
		}
		if entry.Status != register.StatusReal {
			for _, word := range []string{"已对齐", "aligned"} {
				if strings.Contains(entry.Behavior, word) {
					t.Errorf("%s: a %s entry must not call its behavior %q", id, entry.Status, word)
				}
			}
		}
	}
}

// Every route of every registered fake version has at least one entry, so a
// route added to a fake without a register entry fails here. Routes are taken
// from the versions' endpoint tables, never from a hand-written list.
func TestRegisterCoversEveryFakeEndpoint(t *testing.T) {
	reg := load(t)
	covered := map[string]bool{}
	for _, entry := range reg.Entries {
		for _, endpoint := range entry.Endpoints {
			covered[entry.Version()+" "+endpoint] = true
		}
	}
	routes := 0
	for _, version := range registry.Versions() {
		if len(version.Endpoints) == 0 {
			t.Errorf("%s/%s has no endpoint table", version.Provider, version.API)
		}
		for _, endpoint := range version.Endpoints {
			routes++
			key := version.Provider + "/" + version.API + " " + endpoint.Key()
			if !covered[key] {
				t.Errorf("fake route %s has no register entry; add one to internal/testutil/fakeupstream/register/register.json", key)
			}
		}
	}
	if routes == 0 {
		t.Fatal("no fake routes found")
	}
}

// recordedPrefix is the path prefix the version's samples were recorded under
// ("/v3" for Tripo), read from an endpoint whose Sample key ends with its path.
func recordedPrefix(version fakeupstream.Version) (string, bool) {
	for _, endpoint := range version.Endpoints {
		if endpoint.Sample == "" {
			continue
		}
		method, path, ok := strings.Cut(endpoint.Sample, " ")
		template := templateSegment.ReplaceAllString(endpoint.Path, "{id}")
		if ok && method == endpoint.Method && strings.HasSuffix(path, template) {
			return strings.TrimSuffix(path, template), true
		}
	}
	return "", false
}

type sampleMeta struct {
	CapturedAt     string `json:"captured_at_utc"`
	Provenance     string `json:"provenance"`
	SourceRevision string `json:"source_revision"`
	SchemaRevision string `json:"schema_revision"`
	CatalogSHA256  string `json:"catalog_sha256"`
}

func (m sampleMeta) catalogRevision() string {
	switch {
	case m.SchemaRevision != "":
		return m.SchemaRevision
	case m.CatalogSHA256 != "":
		return "sha256:" + m.CatalogSHA256
	default:
		return "unknown"
	}
}

// A real entry names committed wirefixture candidates that the fake's own
// sample loader accepts (complete, captured or derived), whose .meta.json
// states the same capture date, provenance and revisions as the entry, and
// that hold an exchange of the entry's routes: every sample exercises one of
// the routes, and every route is exercised by one of the samples.
func TestRegisterRealEntriesCiteReadableSamples(t *testing.T) {
	reg := load(t)
	known := versions()
	loaded := map[string]*fakeupstream.Samples{}
	for _, entry := range reg.Entries {
		if entry.Status != register.StatusReal {
			continue
		}
		version, ok := known[entry.Version()]
		if !ok {
			continue // reported by TestRegisterEntriesAreWellFormed
		}
		prefix, ok := recordedPrefix(version)
		if !ok {
			t.Errorf("%s: %s has no sampled endpoint to read the recorded path prefix from", entry.ID, entry.Version())
			continue
		}
		exercised := map[string]bool{}
		for _, sample := range entry.Samples {
			if !strings.HasSuffix(sample.File, ".candidate.json") || filepath.IsAbs(sample.File) || strings.Contains(sample.File, "..") {
				t.Errorf("%s: sample %q must be a repository-relative *.candidate.json path", entry.ID, sample.File)
				continue
			}
			samples, ok := loaded[sample.File]
			if !ok {
				var err error
				samples, err = fakeupstream.LoadSamples(os.DirFS(repoRoot), sample.File)
				if err != nil {
					t.Errorf("%s: %v", entry.ID, err)
					continue
				}
				loaded[sample.File] = samples
			}
			raw, err := os.ReadFile(filepath.Join(repoRoot, strings.TrimSuffix(sample.File, ".candidate.json")+".meta.json"))
			if err != nil {
				t.Errorf("%s: sample meta: %v", entry.ID, err)
				continue
			}
			var meta sampleMeta
			if err := json.Unmarshal(raw, &meta); err != nil {
				t.Errorf("%s: sample meta %s: %v", entry.ID, sample.File, err)
				continue
			}
			revision := meta.SourceRevision
			if revision == "" {
				revision = "unknown"
			}
			if !validDate(sample.CapturedAt) || sample.CapturedAt != meta.CapturedAt {
				t.Errorf("%s: %s captured_at %q, meta says %q", entry.ID, sample.File, sample.CapturedAt, meta.CapturedAt)
			}
			if sample.Provenance != meta.Provenance {
				t.Errorf("%s: %s provenance %q, meta says %q", entry.ID, sample.File, sample.Provenance, meta.Provenance)
			}
			if sample.AppRevision != revision {
				t.Errorf("%s: %s app_revision %q, meta says %q", entry.ID, sample.File, sample.AppRevision, revision)
			}
			if sample.CatalogRevision != meta.catalogRevision() {
				t.Errorf("%s: %s catalog_revision %q, meta says %q", entry.ID, sample.File, sample.CatalogRevision, meta.catalogRevision())
			}
			matched := false
			for _, endpoint := range entry.Endpoints {
				key := templateSegment.ReplaceAllString(endpoint, "{id}")
				if len(samples.Find(key, prefix)) > 0 {
					exercised[endpoint] = true
					matched = true
				}
			}
			if !matched {
				t.Errorf("%s: %s holds no exchange of %v", entry.ID, sample.File, entry.Endpoints)
			}
		}
		for _, endpoint := range entry.Endpoints {
			if !exercised[endpoint] {
				t.Errorf("%s: no cited sample exercises %s; split the entry or mark that route constructed", entry.ID, endpoint)
			}
		}
	}
}

type coverageMatrix struct {
	Units []struct {
		Adapter    string `json:"adapter_key"`
		Capability string `json:"capability"`
		Endpoint   string `json:"endpoint"`
		Evidence   string `json:"evidence"`
		Offerings  []struct {
			ID string `json:"id"`
		} `json:"offerings"`
	} `json:"units"`
}

// The register corrects the wire coverage matrix per behavior: a correction
// names a matrix unit that exists, carries real (or derived) evidence and lists
// the Offering, and the entry itself is not real. The Offerings the matrix
// credits only through a shared endpoint (requiredCorrections) must have one.
// The matrix generation is not changed by the register.
func TestRegisterWireCoverageCorrections(t *testing.T) {
	reg := load(t)
	raw, err := os.ReadFile(wireCoveragePath)
	if err != nil {
		t.Fatal(err)
	}
	var matrix coverageMatrix
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	corrected := map[string]bool{}
	for _, entry := range reg.Entries {
		correction := entry.Correction
		if correction == nil {
			continue
		}
		if entry.Status == register.StatusReal {
			t.Errorf("%s: a correction entry cannot be real", entry.ID)
		}
		if strings.TrimSpace(correction.Reason) == "" {
			t.Errorf("%s: a correction needs a reason", entry.ID)
		}
		found := false
		for _, unit := range matrix.Units {
			if unit.Adapter+"|"+unit.Capability+"|"+unit.Endpoint != correction.Unit {
				continue
			}
			found = true
			if unit.Evidence != "real" && unit.Evidence != "derived" {
				t.Errorf("%s: unit %s is %q, nothing to correct", entry.ID, correction.Unit, unit.Evidence)
			}
			if !slices.ContainsFunc(unit.Offerings, func(o struct {
				ID string `json:"id"`
			}) bool {
				return o.ID == correction.Offering
			}) {
				t.Errorf("%s: unit %s does not list %s", entry.ID, correction.Unit, correction.Offering)
			}
		}
		if !found {
			t.Errorf("%s: wire coverage has no unit %q", entry.ID, correction.Unit)
		}
		corrected[correction.Offering] = true
	}
	for _, offering := range requiredCorrections {
		if !corrected[offering] {
			t.Errorf("%s shares a sampled endpoint without a real sample of its own; the register must correct it per behavior", offering)
		}
	}
}

// docs/fake-register.md is generated from register.json and committed.
func TestFakeRegisterMarkdown(t *testing.T) {
	reg := load(t)
	want := reg.Markdown()
	if os.Getenv(updateEnv) == "1" {
		if err := os.WriteFile(markdownPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("%v; generate it with %s=1 go test ./internal/testutil/fakeupstream/register -run Markdown", err, updateEnv)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("docs/fake-register.md is stale; regenerate with %s=1 go test ./internal/testutil/fakeupstream/register -run Markdown", updateEnv)
	}
}

// The register is public: it names no host address, private path or
// credential. (The wire capture privacy scan does not apply: it refuses every
// unregistered URL host, and a basis names official documentation pages.)
func TestRegisterHasNoPrivateData(t *testing.T) {
	data, err := os.ReadFile(registerJSONPath)
	if err != nil {
		t.Fatal(err)
	}
	markdown := load(t).Markdown()
	forbidden := []*regexp.Regexp{
		regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`),
		regexp.MustCompile(`(?i)/(users|home|data|root)/`),
		regexp.MustCompile(`(?i)\b(bearer|token|password|secret)[=:]\s*\S`),
		regexp.MustCompile(`\bsk-[A-Za-z0-9]{8,}`),
		regexp.MustCompile(`(?i)localhost:\d+`),
	}
	for name, content := range map[string][]byte{registerJSONPath: data, "docs/fake-register.md": markdown} {
		for _, pattern := range forbidden {
			if match := pattern.Find(content); match != nil {
				t.Errorf("%s contains %q (%s)", name, match, pattern)
			}
		}
	}
}
