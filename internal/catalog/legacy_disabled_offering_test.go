package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyMigrationCanReferenceOnlyMatchingDisabledOffering(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(source)); err != nil {
		t.Fatalf("historical mapping rejected: %v", err)
	}
	for _, tc := range []struct{ name, field, value string }{
		{"wrong provider", "legacy_provider_id", "tripo"},
		{"wrong capability", "capability", "image-to-3d"},
		{"unknown offering", "offering_id", "meshy/missing/text-to-3d"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal(source, &document); err != nil {
				t.Fatal(err)
			}
			application := document["application"].(map[string]any)
			runtime := application["runtime"].(map[string]any)
			migration := runtime["legacy_migration"].(map[string]any)
			backfill := migration["thumbnail_backfill"].(map[string]any)
			mappings := backfill["offering_mappings"].([]any)
			mappings[0].(map[string]any)[tc.field] = tc.value
			changed, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), "offering_mappings") {
				t.Fatalf("invalid mapping accepted or wrong error: %v", err)
			}
		})
	}
}
