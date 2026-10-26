package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestRecoveryPresentationIsOptionalCompleteAndCloned(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	a := c.Application()
	if a.TaskHistory.AssetRecovery == nil || a.TaskHistory.AssetRecovery.Action != "恢复已有产物" {
		t.Fatal("missing recovery presentation")
	}
	a.TaskHistory.AssetRecovery.Action = "changed"
	if c.Application().TaskHistory.AssetRecovery.Action == "changed" {
		t.Fatal("caller mutated Catalog presentation")
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	history := document["application"].(map[string]any)["task_history"].(map[string]any)
	labels := history["asset_recovery"].(map[string]any)
	for key, value := range labels {
		labels[key] = " "
		changed, _ := json.Marshal(document)
		if _, err = catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), "asset_recovery."+key) {
			t.Fatalf("missing label %s accepted: %v", key, err)
		}
		labels[key] = value
	}
	delete(history, "asset_recovery")
	// Dropping the section drops its translations, which would otherwise point at nothing.
	english := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
	for path := range english {
		if strings.HasPrefix(path, "task_history.asset_recovery") {
			delete(english, path)
		}
	}
	legacy, _ := json.Marshal(document)
	old, err := catalog.Load(bytes.NewReader(legacy))
	if err != nil || old.Application().TaskHistory.AssetRecovery != nil {
		t.Fatalf("legacy Schema without recovery UI rejected: %v", err)
	}
}
