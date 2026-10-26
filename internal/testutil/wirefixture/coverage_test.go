package wirefixture_test

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

type coverageOffering struct {
	ID         string `json:"id"`
	ProviderID string `json:"provider_id"`
	AdapterKey string `json:"adapter_key"`
	Capability string `json:"capability"`
	Enabled    bool   `json:"enabled"`
	Options    struct {
		Endpoint string `json:"endpoint"`
	} `json:"provider_options"`
}

func TestWireCoverageMatrix(t *testing.T) {
	raw, err := os.ReadFile("../../catalog/default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Offerings []coverageOffering `json:"offerings"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	matrixRaw, err := os.ReadFile("testdata/wire-coverage.json")
	if err != nil {
		t.Fatal(err)
	}
	var matrix struct {
		Version   int `json:"version"`
		Offerings []struct {
			ID         string `json:"id"`
			ProviderID string `json:"provider_id"`
			Enabled    bool   `json:"enabled"`
			Evidence   string `json:"evidence"`
			Source     string `json:"source"`
		} `json:"offerings"`
	}
	if err := json.Unmarshal(matrixRaw, &matrix); err != nil {
		t.Fatal(err)
	}
	if matrix.Version != 1 {
		t.Fatalf("coverage matrix version = %d", matrix.Version)
	}

	byID := make(map[string]coverageOffering, len(document.Offerings))
	enabled := 0
	for _, offering := range document.Offerings {
		byID[offering.ID] = offering
		if offering.Enabled {
			enabled++
		}
	}
	if len(document.Offerings) != 76 || enabled != 69 || len(document.Offerings)-enabled != 7 {
		t.Fatalf("catalog coverage baseline = total %d enabled %d disabled %d, want 76/69/7; update the reviewed matrix with the Catalog change",
			len(document.Offerings), enabled, len(document.Offerings)-enabled)
	}
	if len(matrix.Offerings) != len(document.Offerings) {
		t.Fatalf("matrix rows = %d, catalog offerings = %d", len(matrix.Offerings), len(document.Offerings))
	}

	evidenceKinds := map[string]bool{
		"missing": true, "real": true, "official-example": true, "derived": true, "synthetic-negative": true,
	}
	seen := make(map[string]bool, len(matrix.Offerings))
	for _, row := range matrix.Offerings {
		offering, ok := byID[row.ID]
		if !ok || seen[row.ID] {
			t.Fatalf("matrix row is unknown or duplicated: %s", row.ID)
		}
		seen[row.ID] = true
		if row.ProviderID != offering.ProviderID || row.Enabled != offering.Enabled {
			t.Fatalf("matrix row drifted from Catalog: %s", row.ID)
		}
		if !evidenceKinds[row.Evidence] {
			t.Fatalf("matrix row has unknown evidence kind: %s %q", row.ID, row.Evidence)
		}
		if row.Evidence == "missing" && row.Source != "none" || row.Evidence != "missing" && row.Source == "none" {
			t.Fatalf("matrix row evidence/source mismatch: %s", row.ID)
		}
		if !offering.Enabled {
			continue
		}
		if offering.ID == "" || offering.ProviderID == "" || offering.AdapterKey == "" {
			t.Fatalf("enabled offering lacks a provider/adapter mapping: %s", row.ID)
		}
		if offering.ProviderID != "local" && offering.Capability == "" {
			t.Fatalf("remote offering lacks a capability: %s", row.ID)
		}
		if offering.Options.Endpoint != "" && offering.ProviderID == "" {
			t.Fatalf("dynamic endpoint has no provider: %s", row.ID)
		}
	}
	var missing []string
	for id, offering := range byID {
		if offering.Enabled && !seen[id] {
			missing = append(missing, id)
		}
	}
	slices.Sort(missing)
	if len(missing) > 0 {
		t.Fatalf("enabled offerings absent from the matrix: %v", missing)
	}

	counts := map[string]int{}
	for _, row := range matrix.Offerings {
		counts[row.Evidence]++
	}
	t.Logf("wire coverage: total=%d enabled=%d real=%d official-example=%d derived=%d synthetic-negative=%d missing=%d",
		len(document.Offerings), enabled, counts["real"], counts["official-example"], counts["derived"], counts["synthetic-negative"], counts["missing"])
	if counts["real"] == 0 {
		t.Log("#365 live evidence is still missing; matrix status is not a pass and cannot be replaced with synthetic data")
	}
}
