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
		// Model is the value of the request's "model" field, when the Offering
		// declares it here (the Tripo adapter sends it verbatim).
		Model string `json:"model"`
		// ModelParameter names the request field that carries the upstream
		// model (Meshy's ai_model); its value is ModelParameterValue, else the
		// Offering's model_id.
		ModelParameter      string `json:"model_parameter"`
		ModelParameterValue string `json:"model_parameter_value"`
		// RequestTemplate fields set to "$model" carry the Offering's model_id.
		RequestTemplate map[string]any `json:"request_template"`
		// CreatePath is the submit route of Offerings that declare their routes
		// as create_path/observe_path/download_path (the matting protocol).
		CreatePath string `json:"create_path"`
	} `json:"provider_options"`
	Parameters []struct {
		Name     string          `json:"name"`
		WireName string          `json:"wire_name"`
		Default  json.RawMessage `json:"default"`
		Options  []struct {
			Value any `json:"value"`
		} `json:"options"`
	} `json:"parameters"`
}

// coverageModelWire is what an Offering declares for the request fields that
// carry the upstream model: field name -> the model values it can send there.
// The field names come from the Catalog: the Provider's
// application.provider_deprecations[].model_fields, the Offering's
// provider_options.model_parameter and its request_template "$model" fields,
// and "model" for provider_options.model. The values are what the Offering
// declares for those fields: the model parameter's value, the template's
// model_id or literal, provider_options.model, and parameter defaults and
// options. Fields the Offering declares no value for are left out.
func coverageModelWire(o coverageOffering, providerModelFields []string) map[string][]string {
	fields := slices.Clone(providerModelFields)
	wire := map[string][]string{}
	add := func(field string, value any) {
		text, ok := value.(string)
		if !ok || strings.TrimSpace(field) == "" || strings.TrimSpace(text) == "" || slices.Contains(wire[field], text) {
			return
		}
		wire[field] = append(wire[field], text)
	}
	if o.Options.Model != "" {
		add("model", o.Options.Model)
	}
	if parameter := strings.TrimSpace(o.Options.ModelParameter); parameter != "" {
		fields = append(fields, parameter)
		value := strings.TrimSpace(o.Options.ModelParameterValue)
		if value == "" {
			value = o.ModelID
		}
		add(parameter, value)
	}
	for field, value := range o.Options.RequestTemplate {
		if value == "$model" {
			add(field, o.ModelID)
		} else if slices.Contains(fields, field) {
			if text, ok := value.(string); ok && !strings.HasPrefix(text, "$") {
				add(field, text)
			}
		}
	}
	for _, parameter := range o.Parameters {
		name := parameter.WireName
		if name == "" {
			name = parameter.Name
		}
		if !slices.Contains(fields, name) {
			continue
		}
		var value any
		if len(parameter.Default) > 0 && json.Unmarshal(parameter.Default, &value) == nil {
			add(name, value)
		}
		for _, option := range parameter.Options {
			add(name, option.Value)
		}
	}
	return wire
}

// recordCoverageModels adds the models one sampled JSON request body names in
// the given model fields, keeping per value the best evidence kind. A field
// that is absent, not a string or empty names no model.
func recordCoverageModels(requested map[string]map[string]string, rawBody json.RawMessage, fields []string, kind string) map[string]map[string]string {
	var body map[string]any
	if len(rawBody) == 0 || json.Unmarshal(rawBody, &body) != nil {
		return requested
	}
	for _, field := range fields {
		value, ok := body[field].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		if requested == nil {
			requested = map[string]map[string]string{}
		}
		if requested[field] == nil {
			requested[field] = map[string]string{}
		}
		if prior, seen := requested[field][value]; !seen || coverageEvidenceRank[kind] > coverageEvidenceRank[prior] {
			requested[field][value] = kind
		}
	}
	return requested
}

// coverageModelEvidence is the evidence a unit gives one Offering. A unit is
// shared by the Offerings of one endpoint, but each sample requested one model:
// when the unit's samples name a model in a field the Offering declares, only
// the samples that requested the Offering's own model count (#541, #575). An
// Offering that declares no model, or a unit whose samples name no model in its
// fields (field absent or empty), keeps the unit's evidence.
//
// requested maps field -> requested value -> best evidence of the samples that
// sent it; declared is coverageModelWire.
func coverageModelEvidence(unitEvidence string, requested map[string]map[string]string, declared map[string][]string) string {
	named, best := false, evidenceMissing
	for field, values := range declared {
		for value, kind := range requested[field] {
			named = true
			if slices.Contains(values, value) && coverageEvidenceRank[kind] > coverageEvidenceRank[best] {
				best = kind
			}
		}
	}
	if !named || coverageEvidenceRank[unitEvidence] <= coverageEvidenceRank[evidenceMissing] {
		return unitEvidence
	}
	return best
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
	// requested are the upstream models the unit's samples asked for, per model
	// field of a JSON request body (field -> value -> best evidence of the
	// samples that sent it), kept out of the matrix.
	requested map[string]map[string]string
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
			Body   struct {
				JSON json.RawMessage `json:"json"`
			} `json:"body"`
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
		Application struct {
			ProviderDeprecations []struct {
				ProviderID  string   `json:"provider_id"`
				ModelFields []string `json:"model_fields"`
			} `json:"provider_deprecations"`
		} `json:"application"`
		Offerings []coverageOffering `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	samples := readCoverageSamples(t)

	providerModelFields := map[string][]string{}
	for _, entry := range document.Application.ProviderDeprecations {
		providerModelFields[entry.ProviderID] = entry.ModelFields
	}
	byID := map[string]coverageOffering{}
	modelWire := map[string]map[string][]string{}
	var modelFields []string // every field any Offering declares a model in
	providerAdapter := map[string]string{}
	for _, o := range document.Offerings {
		byID[o.ID] = o
		providerAdapter[o.ProviderID] = o.AdapterKey
		modelWire[o.ID] = coverageModelWire(o, providerModelFields[o.ProviderID])
		for field := range modelWire[o.ID] {
			if !slices.Contains(modelFields, field) {
				modelFields = append(modelFields, field)
			}
		}
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
			unit.requested = recordCoverageModels(unit.requested, ex.Request.Body.JSON, modelFields, kind)

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
			// A wire unit is shared by the Offerings of one endpoint, but each of its
			// samples was requested with one model: an Offering gets only the
			// evidence of the samples that requested its own model, in the model
			// field its Provider declares (#541: the P2 generation endpoints are the
			// H endpoints with another model name; #575: Meshy's field is ai_model).
			evidence := coverageModelEvidence(u.Evidence, u.requested, modelWire[o.ID])
			offeringEvidence[o.ID] = append(offeringEvidence[o.ID], evidence)
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

// A wire unit is shared by every Offering of one endpoint, but its samples were
// requested with one model: an Offering that requests another model (Tripo P2 on the
// H generation endpoints, #541) must not read as "real" because of them.
func TestWireCoverageDoesNotCarryEvidenceAcrossModels(t *testing.T) {
	var matrix coverageMatrix
	raw, err := os.ReadFile(coverageOutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &matrix); err != nil {
		t.Fatal(err)
	}
	for _, row := range matrix.Offerings {
		switch row.ID {
		case "tripo/tripo-p2/text-to-3d", "tripo/tripo-p2/image-to-3d", "tripo/tripo-p2-multiview/image-to-3d":
			if row.Evidence == evidenceReal {
				t.Errorf("%s is %q: no real P2 request has been captured, the H samples are not its evidence", row.ID, row.Evidence)
			}
		case "meshy/meshy-t2.5/text-to-3d", "meshy/meshy-t2.5/image-to-3d":
			// Meshy names the model in ai_model (#575): the meshy-7.1 samples on
			// the same endpoints are not meshy-t2.5's evidence.
			if row.Evidence == evidenceReal {
				t.Errorf("%s is %q: no real meshy-t2.5 request is attributed to it, the meshy-7.1 samples are not its evidence", row.ID, row.Evidence)
			}
		case "tripo/tripo-v3.1-full/text-to-3d", "meshy/meshy-7.1/image-to-3d":
			if row.Evidence != evidenceReal {
				t.Errorf("%s is %q: its model is the one the samples were requested with", row.ID, row.Evidence)
			}
		}
	}
}

// The model fields an Offering is compared on come from the Catalog: the
// Provider's provider_deprecations model_fields, the Offering's model_parameter,
// its request_template "$model" fields and provider_options.model (#575).
func TestCoverageModelWireReadsCatalogDeclarations(t *testing.T) {
	raw, err := os.ReadFile(coverageCatalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Application struct {
			ProviderDeprecations []struct {
				ProviderID  string   `json:"provider_id"`
				ModelFields []string `json:"model_fields"`
			} `json:"provider_deprecations"`
		} `json:"application"`
		Offerings []coverageOffering `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	fields := map[string][]string{}
	for _, entry := range document.Application.ProviderDeprecations {
		fields[entry.ProviderID] = entry.ModelFields
	}
	want := map[string]map[string][]string{
		// model_parameter ai_model, value model_id.
		"meshy/meshy-7.1/text-to-3d":   {"ai_model": {"meshy-7.1"}},
		"meshy/meshy-t2.5/image-to-3d": {"ai_model": {"meshy-t2.5"}},
		// No model_parameter: the field is the Provider's model_fields, the
		// value the fixed ai_model parameter.
		"meshy/meshy-t2.5/text-to-3d": {"ai_model": {"meshy-t2.5"}},
		// model_parameter_value overrides model_id.
		"meshy/meshy-7.1-multiview/image-to-3d": {"ai_model": {"meshy-7.1"}},
		"meshy/processing/retexture":            {"ai_model": {"meshy-7"}},
		// request_template "$model".
		"openai/gpt-image-2/text-to-image":        {"model": {"gpt-image-2.6-sunburst"}},
		"gemini/gemini-3-pro-image/text-to-image": {"model": {"gemini-3-pro-image"}},
		// provider_options.model.
		"tripo/tripo-v3.1-full/text-to-3d": {"model": {"v3.1-20260211"}},
		"tripo/tripo-p2/text-to-3d":        {"model": {"P2-20260801"}},
		// No model declared: a wire field named "model" that carries a file
		// (Hyper3D Bang's source_asset) is not a model field.
		"hyper3d/rodin-gen-2.5/text-to-3d": {},
		"hyper3d/bang/local-model":         {},
		"meshy/processing/remesh":          {},
	}
	seen := 0
	for _, o := range document.Offerings {
		expected, ok := want[o.ID]
		if !ok {
			continue
		}
		seen++
		got := coverageModelWire(o, fields[o.ProviderID])
		if len(got) != len(expected) {
			t.Errorf("%s: model wire %v, want %v", o.ID, got, expected)
			continue
		}
		for field, values := range expected {
			if !slices.Equal(got[field], values) {
				t.Errorf("%s: model wire %v, want %v", o.ID, got, expected)
			}
		}
	}
	if seen != len(want) {
		t.Fatalf("found %d of %d Offerings in the Catalog", seen, len(want))
	}
}

// Samples requested with one model are not the evidence of an Offering that
// requests another model on the same endpoint, whichever field the Provider
// names the model in (#541, #575).
func TestCoverageModelEvidenceDoesNotCrossModels(t *testing.T) {
	sampled := func(field string, values map[string]string) map[string]map[string]string {
		return map[string]map[string]string{field: values}
	}
	for _, tc := range []struct {
		name      string
		unit      string
		requested map[string]map[string]string
		declared  map[string][]string
		want      string
	}{
		{"meshy ai_model: t2.5 does not inherit the 7.1 samples", evidenceReal,
			sampled("ai_model", map[string]string{"meshy-7.1": evidenceReal}), map[string][]string{"ai_model": {"meshy-t2.5"}}, evidenceMissing},
		{"meshy ai_model: 7.1 keeps its own samples", evidenceReal,
			sampled("ai_model", map[string]string{"meshy-7.1": evidenceReal}), map[string][]string{"ai_model": {"meshy-7.1"}}, evidenceReal},
		{"meshy ai_model: t2.5 gets its own derived sample, not the 7.1 real one", evidenceReal,
			sampled("ai_model", map[string]string{"meshy-7.1": evidenceReal, "meshy-t2.5": evidenceDerived}), map[string][]string{"ai_model": {"meshy-t2.5"}}, evidenceDerived},
		{"model field: P2 does not inherit the v3.1 samples", evidenceReal,
			sampled("model", map[string]string{"v3.1-20260211": evidenceReal}), map[string][]string{"model": {"P2-20260801"}}, evidenceMissing},
		{"model field: v3.1 keeps its own samples", evidenceReal,
			sampled("model", map[string]string{"v3.1-20260211": evidenceReal}), map[string][]string{"model": {"v3.1-20260211"}}, evidenceReal},
		{"model field: a gpt-image-2.5 sample is not gpt-image-2.6's evidence", evidenceReal,
			sampled("model", map[string]string{"gpt-image-2.5-sunburst": evidenceReal}), map[string][]string{"model": {"gpt-image-2.6-sunburst"}}, evidenceMissing},
		{"a model in another field is not compared", evidenceReal,
			sampled("model", map[string]string{"v3.1-20260211": evidenceReal}), map[string][]string{"ai_model": {"meshy-t2.5"}}, evidenceReal},
		{"samples that name no model keep the unit evidence", evidenceReal,
			nil, map[string][]string{"ai_model": {"meshy-t2.5"}}, evidenceReal},
		{"an Offering that declares no model keeps the unit evidence", evidenceReal,
			sampled("ai_model", map[string]string{"meshy-7.1": evidenceReal}), map[string][]string{}, evidenceReal},
		{"a missing unit stays missing", evidenceMissing,
			nil, map[string][]string{"ai_model": {"meshy-t2.5"}}, evidenceMissing},
		{"a disabled unit keeps its class", evidenceDisabled,
			nil, map[string][]string{"ai_model": {"meshy-7"}}, evidenceDisabled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := coverageModelEvidence(tc.unit, tc.requested, tc.declared); got != tc.want {
				t.Errorf("evidence = %q, want %q", got, tc.want)
			}
		})
	}
}

// A sampled request names a model only by a non-empty string in a model field:
// an absent, empty, blank or non-string field names none, and each value keeps
// the best evidence of the samples that sent it.
func TestRecordCoverageModels(t *testing.T) {
	fields := []string{"ai_model", "model"}
	var requested map[string]map[string]string
	for _, body := range []string{
		`{"prompt":"x"}`,
		`{"ai_model":""}`,
		`{"ai_model":"  "}`,
		`{"model":null}`,
		`{"model":{"name":"v3.1"}}`,
		`not json`,
		``,
	} {
		requested = recordCoverageModels(requested, json.RawMessage(body), fields, evidenceReal)
	}
	if len(requested) != 0 {
		t.Fatalf("bodies without a model name recorded %v", requested)
	}
	requested = recordCoverageModels(requested, json.RawMessage(`{"ai_model":"meshy-t2.5","model_type":"smart-topology"}`), fields, evidenceDerived)
	requested = recordCoverageModels(requested, json.RawMessage(`{"ai_model":"meshy-7.1"}`), fields, evidenceReal)
	requested = recordCoverageModels(requested, json.RawMessage(`{"ai_model":"meshy-7.1"}`), fields, evidenceDerived)
	requested = recordCoverageModels(requested, json.RawMessage(`{"model":"v3.1-20260211","ai_model":"meshy-t2.5"}`), fields, evidenceReal)
	want := map[string]map[string]string{
		"ai_model": {"meshy-t2.5": evidenceReal, "meshy-7.1": evidenceReal},
		"model":    {"v3.1-20260211": evidenceReal},
	}
	for field, values := range want {
		for value, kind := range values {
			if requested[field][value] != kind {
				t.Errorf("requested[%s][%s] = %q, want %q (all: %v)", field, value, requested[field][value], kind, requested)
			}
		}
		if len(requested[field]) != len(values) {
			t.Errorf("requested[%s] = %v, want %v", field, requested[field], values)
		}
	}
	if _, ok := requested["model_type"]; ok {
		t.Errorf("model_type is not a model field: %v", requested)
	}
}
