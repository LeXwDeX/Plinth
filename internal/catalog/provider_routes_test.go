package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestProviderRoutesRejectMissingUnsafeAndIncompatibleDeclarations(t *testing.T) {
	for _, id := range []string{"jimeng/seedream-4-5/text-to-image", "tripo/tripo-v3.1/text-to-3d", "tripo/tripo-v3.1/image-to-3d"} {
		for _, endpoint := range []string{"", "https://other.invalid/submit", "//other.invalid/submit", "../submit", "safe/%2e%2e/submit", "submit?secret=x", "submit#fragment"} {
			t.Run(id+"/"+endpoint, func(t *testing.T) {
				payload := mutatedProviderRoute(t, id, func(offering map[string]any) {
					offering["provider_options"].(map[string]any)["endpoint"] = endpoint
				})
				if _, err := catalog.Load(bytes.NewReader(payload)); err == nil {
					t.Fatal("accepted missing or unsafe endpoint")
				}
			})
		}
		t.Run(id+"/wrong capability", func(t *testing.T) {
			payload := mutatedProviderRoute(t, id, func(offering map[string]any) { offering["capability"] = "remesh" })
			if _, err := catalog.Load(bytes.NewReader(payload)); err == nil {
				t.Fatal("accepted incompatible adapter capability")
			}
		})
	}
}

func TestTripoRoutesRequireObserveAndUploadContracts(t *testing.T) {
	for _, field := range []string{"observe_endpoint", "upload_endpoint"} {
		t.Run(field, func(t *testing.T) {
			payload := mutatedProviderRoute(t, "tripo/tripo-v3.1/image-to-3d", func(offering map[string]any) {
				delete(offering["provider_options"].(map[string]any), field)
			})
			if _, err := catalog.Load(bytes.NewReader(payload)); err == nil {
				t.Fatalf("accepted missing %s", field)
			}
		})
	}
}

func mutatedProviderRoute(t *testing.T, id string, update func(map[string]any)) []byte {
	t.Helper()
	payload, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, raw := range document["offerings"].([]any) {
		offering := raw.(map[string]any)
		if offering["id"] == id {
			update(offering)
			found = true
		}
	}
	if !found {
		t.Fatal("missing offering " + id)
	}
	payload, err = json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func TestProviderRouteCanBeCustomizedWithoutChangingAdapter(t *testing.T) {
	for _, id := range []string{"jimeng/seedream-4-5/text-to-image", "tripo/tripo-v3.1/text-to-3d"} {
		payload := mutatedProviderRoute(t, id, func(offering map[string]any) {
			offering["provider_options"].(map[string]any)["endpoint"] = "configured/submit"
		})
		if _, err := catalog.Load(bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
	}
}
