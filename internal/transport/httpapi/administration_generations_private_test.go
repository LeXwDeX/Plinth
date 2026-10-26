package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestAdministrationGenerationCacheWireKeepsUpstreamStatePrivate(t *testing.T) {
	t.Parallel()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	fields := taskCatalog.Application().Administration.Generations.API.Fields
	wire := administrationGenerationCacheWire(fields, administration.GenerationCache{
		TaskID: "task-1",
		Status: "SUCCEEDED",
		Data:   json.RawMessage(`{"remote_id":"provider-job","model_url":"https://provider.example/signed?token=secret"}`),
	})
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "provider.example") ||
		strings.Contains(string(encoded), "provider-job") ||
		strings.Contains(string(encoded), "secret") {
		t.Fatalf("upstream state exposed: %s", encoded)
	}
	if wire[fields.CacheData] == nil || wire[fields.Status] != "SUCCEEDED" {
		t.Fatalf("safe cache metadata missing: %#v", wire)
	}
}
