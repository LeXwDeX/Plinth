package meshy

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestNativeConsentIsNotMeshyWireParameter(t *testing.T) {
	a := &Adapter{}
	body := map[string]any{"prompt": "test", "server_consent": true}
	request := generation.StartRequest{Offering: generation.Offering{NativePricing: &schema.NativePricing{ConsentParameter: "server_consent"}, ProviderOptions: json.RawMessage(`{}`), Parameters: []generation.Parameter{{Name: "prompt", Type: "text"}, {Name: "server_consent", Type: "boolean"}}}}
	if e := a.projectInput(context.Background(), request, body); e != nil {
		t.Fatal(e)
	}
	if body["prompt"] != "test" || body["server_consent"] != nil {
		t.Fatalf("platform consent leaked %v", body)
	}
}
