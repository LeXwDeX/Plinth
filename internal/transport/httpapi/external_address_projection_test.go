package httpapi

import "testing"

func TestLegacyProjectionDropsRawAndEncodedProviderAddresses(t *testing.T) {
	t.Parallel()
	response := map[string]any{
		"model_urls":     []any{"https://cdn.provider.example/model.glb?token=secret"},
		"source":         map[string]any{"url": "https%3A%2F%2Fcdn.provider.example%2Fmodel.glb"},
		"double_encoded": "https%253A%252F%252Fcdn.provider.example%252Fmodel.glb",
		"asset_url":      "/api/v1/tasks/task-1/assets/asset-1",
	}
	redactExternalAddresses(response)
	if response["model_urls"].([]any)[0] != "" ||
		response["source"].(map[string]any)["url"] != "" ||
		response["double_encoded"] != "" ||
		response["asset_url"] != "/api/v1/tasks/task-1/assets/asset-1" {
		t.Fatalf("unsafe projection = %#v", response)
	}
}
