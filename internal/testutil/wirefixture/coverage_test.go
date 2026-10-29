package wirefixture_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The coverage matrix is generated from the Catalog declarations and the
// committed wire samples. It is never edited by hand: regenerate with
//
//	UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage
//
// Units without a real sample are reported as "missing"; they are never filled
// with synthetic data and never fail the test.

const (
	coverageCatalogPath = "../../catalog/default_catalog.json"
	coverageOutputPath  = "testdata/wire-coverage.generated.json"
	coverageInternal    = "../.."

	evidenceReal      = "real"
	evidenceOfficial  = "official-example"
	evidenceDerived   = "derived"
	evidenceSynthetic = "synthetic-negative"
	evidenceMissing   = "missing"
	evidenceLocal     = "local-no-wire"
	evidenceDisabled  = "disabled-zero-egress"
	evidencePartial   = "partial"

	scenarioCapability = "(scenario)"
)

var coverageEvidenceRank = map[string]int{evidenceMissing: 0, evidenceSynthetic: 1, evidenceDerived: 2, evidenceOfficial: 3, evidenceReal: 4}

type coverageOffering struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	ModelID    string `json:"model_id"`
	AdapterKey string `json:"adapter_key"`
	Capability string `json:"capability"`
	Enabled    bool   `json:"enabled"`
	Options    struct {
		Endpoint string `json:"endpoint"`
		// CreatePath is the submit route of Offerings that declare their routes
		// as create_path/observe_path/download_path (the matting protocol).
		CreatePath string `json:"create_path"`
	} `json:"provider_options"`
}

type coverageOfferingRef struct {
	ID      string `json:"id"`
	ModelID string `json:"model_id"`
	Enabled bool   `json:"enabled"`
}

type coverageUnit struct {
	Adapter       string                `json:"adapter_key"`
	Capability    string                `json:"capability"`
	Endpoint      string                `json:"endpoint"`
	Hosts         []string              `json:"hosts,omitempty"`
	Protocols     []string              `json:"protocols,omitempty"`
	Evidence      string                `json:"evidence"`
	EvidenceFiles []string              `json:"evidence_files"`
	Offerings     []coverageOfferingRef `json:"offerings"`
}

type coverageOfferingRow struct {
	ID       string   `json:"id"`
	ModelID  string   `json:"model_id"`
	Adapter  string   `json:"adapter_key"`
	Enabled  bool     `json:"enabled"`
	Evidence string   `json:"evidence"`
	Units    []string `json:"units"`
}

type coverageMatrix struct {
	Version     int                       `json:"version"`
	Catalog     string                    `json:"catalog"`
	CatalogHash string                    `json:"catalog_sha256"`
	Samples     []string                  `json:"samples"`
	Counts      map[string]map[string]int `json:"counts"`
	Incomplete  []string                  `json:"incomplete"`
	Units       []coverageUnit            `json:"units"`
	Offerings   []coverageOfferingRow     `json:"offerings"`
}

type coverageSample struct {
	candidate string // repo-relative path
	offering  string // optional meta.offering
	exchanges []struct {
		Metadata struct {
			Connection string `json:"connection"`
			Protocol   string `json:"protocol"`
			Provenance string `json:"provenance"`
		} `json:"metadata"`
		Request struct {
			Method string `json:"method"`
			Host   string `json:"host"`
			Path   string `json:"path"`
		} `json:"request"`
	}
}

var (
	coverageIDSegment    = regexp.MustCompile(`^(id-\d+|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{16,}|\d+)$`)
	coverageAssetSegment = regexp.MustCompile(`^fixture-\d+(\.[A-Za-z0-9]+)?$`)
)

// normalizeCoveragePath replaces identifier segments with {id} and sanitized
// media placeholders with {asset}, keeping the media extension.
func normalizeCoveragePath(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case coverageAssetSegment.MatchString(segment):
			segments[i] = "{asset}" + filepath.Ext(segment)
		case coverageIDSegment.MatchString(segment):
			segments[i] = "{id}"
		}
	}
	return strings.Join(segments, "/")
}

func coverageProvenanceKind(provenance string) string {
	switch provenance {
	case "captured":
		return evidenceReal
	case "official-example":
		return evidenceOfficial
	case "derived":
		return evidenceDerived
	default:
		return evidenceSynthetic
	}
}

func readCoverageSamples(t *testing.T) []coverageSample {
	t.Helper()
	var samples []coverageSample
	err := filepath.WalkDir(coverageInternal, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".candidate.json") {
			return nil
		}
		sample := coverageSample{candidate: "internal/" + filepath.ToSlash(strings.TrimPrefix(path, coverageInternal+string(filepath.Separator)))}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &sample.exchanges); err != nil {
			return err
		}
		if metaRaw, err := os.ReadFile(strings.TrimSuffix(path, ".candidate.json") + ".meta.json"); err == nil {
			var meta struct {
				Offering string `json:"offering"`
			}
			if err := json.Unmarshal(metaRaw, &meta); err != nil {
				return err
			}
			sample.offering = meta.Offering
		}
		samples = append(samples, sample)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(samples, func(a, b coverageSample) int { return strings.Compare(a.candidate, b.candidate) })
	return samples
}

func generateWireCoverage(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(coverageCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Offerings []coverageOffering `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	samples := readCoverageSamples(t)

	byID := map[string]coverageOffering{}
	providerAdapter := map[string]string{}
	for _, o := range document.Offerings {
		byID[o.ID] = o
		providerAdapter[o.ProviderID] = o.AdapterKey
	}
	ref := func(o coverageOffering) coverageOfferingRef {
		return coverageOfferingRef{ID: o.ID, ModelID: o.ModelID, Enabled: o.Enabled}
	}

	units := map[string]*coverageUnit{}
	unitKey := func(adapter, capability, endpoint string) string { return adapter + "|" + capability + "|" + endpoint }
	ensure := func(adapter, capability, endpoint string) *coverageUnit {
		key := unitKey(adapter, capability, endpoint)
		if units[key] == nil {
			units[key] = &coverageUnit{Adapter: adapter, Capability: capability, Endpoint: endpoint, Evidence: evidenceMissing}
		}
		return units[key]
	}
	addUnique := func(list []string, value string) []string {
		if value == "" || slices.Contains(list, value) {
			return list
		}
		return append(list, value)
	}

	// Declarations: local and disabled Offerings never egress; enabled remote
	// Offerings declare an upstream endpoint (or leave it adapter-defined).
	declared := map[string]*coverageUnit{} // adapter|capability|declared endpoint
	for _, o := range document.Offerings {
		var unit *coverageUnit
		switch {
		case !o.Enabled:
			unit = ensure(o.AdapterKey, o.Capability, "(disabled)")
			unit.Evidence = evidenceDisabled
		case strings.HasPrefix(o.AdapterKey, "local.") || o.ProviderID == "local":
			unit = ensure(o.AdapterKey, o.Capability, "(local)")
			unit.Evidence = evidenceLocal
		default:
			endpoint := o.Options.Endpoint
			if endpoint == "" {
				endpoint = o.Options.CreatePath
			}
			if endpoint == "" {
				endpoint = "(adapter-defined)"
			}
			unit = ensure(o.AdapterKey, o.Capability, "declared:"+endpoint)
			declared[unitKey(o.AdapterKey, o.Capability, endpoint)] = unit
		}
		unit.Offerings = append(unit.Offerings, ref(o))
	}

	// Observed wire: attribute each distinct endpoint to an adapter and, when the
	// sample names its Offering, to that capability.
	for _, sample := range samples {
		capability, offeringAdapter := scenarioCapability, ""
		if o, ok := byID[sample.offering]; ok {
			capability, offeringAdapter = o.Capability, o.AdapterKey
		}
		for _, ex := range sample.exchanges {
			connection := ex.Metadata.Connection
			adapter := providerAdapter[connection]
			if adapter == "" {
				adapter = "connection:" + connection
			}
			unitCapability := capability
			if offeringAdapter != "" && adapter != offeringAdapter {
				unitCapability = scenarioCapability
			}
			path := normalizeCoveragePath(ex.Request.Path)
			endpoint := ex.Request.Method + " " + path
			unit := ensure(adapter, unitCapability, endpoint)
			kind := coverageProvenanceKind(ex.Metadata.Provenance)
			if coverageEvidenceRank[kind] > coverageEvidenceRank[unit.Evidence] {
				unit.Evidence = kind
			}
			unit.EvidenceFiles = addUnique(unit.EvidenceFiles, sample.candidate)
			unit.Hosts = addUnique(unit.Hosts, ex.Request.Host)
			unit.Protocols = addUnique(unit.Protocols, ex.Metadata.Protocol)

			// Offerings served by this unit: a declared endpoint it ends with, else
			// every enabled Offering of the same adapter and capability.
			trimmed := strings.TrimSuffix(path, "/{id}")
			matched := false
			for key, d := range declared {
				parts := strings.SplitN(key, "|", 3)
				if parts[0] == adapter && parts[1] == unitCapability && strings.HasSuffix(trimmed, "/"+parts[2]) {
					matched = true
					for _, o := range d.Offerings {
						if !slices.ContainsFunc(unit.Offerings, func(r coverageOfferingRef) bool { return r.ID == o.ID }) {
							unit.Offerings = append(unit.Offerings, o)
						}
					}
					delete(units, unitKey(d.Adapter, d.Capability, d.Endpoint))
				}
			}
			if !matched && len(unit.Offerings) == 0 {
				for _, o := range document.Offerings {
					if o.Enabled && o.AdapterKey == adapter && o.Capability == unitCapability {
						unit.Offerings = append(unit.Offerings, ref(o))
					}
				}
			}
		}
	}
	list := make([]*coverageUnit, 0, len(units))
	for _, u := range units {
		slices.Sort(u.Hosts)
		slices.Sort(u.Protocols)
		slices.Sort(u.EvidenceFiles)
		slices.SortFunc(u.Offerings, func(a, b coverageOfferingRef) int { return strings.Compare(a.ID, b.ID) })
		if u.EvidenceFiles == nil {
			u.EvidenceFiles = []string{}
		}
		if u.Offerings == nil {
			u.Offerings = []coverageOfferingRef{}
		}
		list = append(list, u)
	}
	slices.SortFunc(list, func(a, b *coverageUnit) int {
		return strings.Compare(unitKey(a.Adapter, a.Capability, a.Endpoint), unitKey(b.Adapter, b.Capability, b.Endpoint))
	})

	matrix := coverageMatrix{
		Version: 2, Catalog: "internal/catalog/default_catalog.json", CatalogHash: hex.EncodeToString(digest[:]),
		Samples: []string{}, Counts: map[string]map[string]int{"offerings": {}, "units": {}}, Incomplete: []string{},
	}
	for _, s := range samples {
		matrix.Samples = append(matrix.Samples, s.candidate)
	}
	offeringUnits := map[string][]string{}
	offeringEvidence := map[string][]string{}
	for _, u := range list {
		id := unitKey(u.Adapter, u.Capability, u.Endpoint)
		matrix.Units = append(matrix.Units, *u)
		matrix.Counts["units"][u.Evidence]++
		if u.Evidence == evidenceMissing {
			matrix.Incomplete = append(matrix.Incomplete, id)
		}
		for _, o := range u.Offerings {
			offeringUnits[o.ID] = append(offeringUnits[o.ID], id)
			offeringEvidence[o.ID] = append(offeringEvidence[o.ID], u.Evidence)
		}
	}
	matrix.Counts["units"]["total"] = len(list)
	for _, o := range document.Offerings {
		evidences := offeringEvidence[o.ID]
		class := evidenceMissing
		switch {
		case len(evidences) == 0:
		case slices.ContainsFunc(evidences, func(e string) bool { return e == evidenceDisabled || e == evidenceLocal }):
			class = evidences[0]
		default:
			best, worst := evidenceMissing, evidenceReal
			for _, e := range evidences {
				if coverageEvidenceRank[e] > coverageEvidenceRank[best] {
					best = e
				}
				if coverageEvidenceRank[e] < coverageEvidenceRank[worst] {
					worst = e
				}
			}
			switch {
			case worst == best:
				class = best
			case best == evidenceMissing:
				class = evidenceMissing
			default:
				class = evidencePartial
			}
		}
		matrix.Offerings = append(matrix.Offerings, coverageOfferingRow{
			ID: o.ID, ModelID: o.ModelID, Adapter: o.AdapterKey, Enabled: o.Enabled, Evidence: class, Units: offeringUnits[o.ID],
		})
		matrix.Counts["offerings"][class]++
		if o.Enabled {
			matrix.Counts["offerings"]["enabled"]++
		} else {
			matrix.Counts["offerings"]["disabled"]++
		}
	}
	matrix.Counts["offerings"]["total"] = len(document.Offerings)
	slices.SortFunc(matrix.Offerings, func(a, b coverageOfferingRow) int { return strings.Compare(a.ID, b.ID) })
	for i := range matrix.Offerings {
		if matrix.Offerings[i].Units == nil {
			matrix.Offerings[i].Units = []string{}
		}
	}
	out, err := json.MarshalIndent(matrix, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(out, '\n')
}

func TestWireCoverageMatrix(t *testing.T) {
	generated := generateWireCoverage(t)
	if os.Getenv("UPDATE_WIRE_COVERAGE") == "1" {
		if err := os.WriteFile(coverageOutputPath, generated, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	committed, err := os.ReadFile(coverageOutputPath)
	if err != nil {
		t.Fatalf("%v; run UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage", err)
	}
	if !bytes.Equal(committed, generated) {
		t.Fatal("wire coverage matrix is stale; regenerate with: UPDATE_WIRE_COVERAGE=1 go test ./internal/testutil/wirefixture -run Coverage")
	}
	var matrix coverageMatrix
	if err := json.Unmarshal(committed, &matrix); err != nil {
		t.Fatal(err)
	}
	t.Logf("wire coverage: units=%v offerings=%v incomplete=%d", matrix.Counts["units"], matrix.Counts["offerings"], len(matrix.Incomplete))
	if matrix.Counts["units"][evidenceReal] == 0 {
		t.Log("no real wire sample is committed; missing units are reported, never filled with synthetic data")
	}
}

func TestCoveragePathNormalization(t *testing.T) {
	for path, want := range map[string]string{
		"/openapi/v2/text-to-3d/id-12": "/openapi/v2/text-to-3d/{id}",
		"/fixture-9.fbx":               "/{asset}.fbx",
		"/api/v2/status":               "/api/v2/status",
		"/v1/tasks/123456":             "/v1/tasks/{id}",
	} {
		if got := normalizeCoveragePath(path); got != want {
			t.Errorf("normalize(%q) = %q, want %q", path, got, want)
		}
	}
}

// Capture metadata may only name a clean 40-hex revision; "unknown" is kept for
// historical captures taken from a dirty worktree.
func TestCaptureMetaSourceRevision(t *testing.T) {
	clean := regexp.MustCompile(`^[0-9a-f]{40}$`)
	found := 0
	err := filepath.WalkDir(coverageInternal, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".meta.json") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var meta map[string]any
		if err := json.Unmarshal(raw, &meta); err != nil {
			return err
		}
		for _, field := range []string{"source_revision", "source_sha"} {
			value, ok := meta[field].(string)
			if !ok {
				continue
			}
			found++
			if value != "unknown" && !clean.MatchString(value) {
				t.Errorf("%s: %s = %q, want a clean 40-hex revision or \"unknown\"", path, field, value)
			}
			if value != "unknown" {
				if state, _ := meta["source_state"].(string); strings.Contains(state, "dirty") {
					t.Errorf("%s: dirty capture must not carry a revision label", path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Fatal("no capture metadata found")
	}
}
