package catalog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestTaskAssetSourceParameterContract(t *testing.T) {
	base := `{"name":"source_task","type":"task","task_source_kind":"assets","task_offerings":["local/forward"],"media_types":["model/gltf+json"],"extensions":[".gltf"],"max_bytes":1024}`
	for _, name := range []string{"valid", "default upstream", "explicit upstream", "unknown kind", "asset type", "empty media", "wildcard media", "empty extension", "zero bytes", "empty offering", "duplicate offering"} {
		t.Run(name, func(t *testing.T) {
			var values map[string]any
			_ = json.Unmarshal([]byte(base), &values)
			switch name {
			case "default upstream":
				delete(values, "task_source_kind")
				delete(values, "extensions")
				delete(values, "media_types")
				delete(values, "max_bytes")
			case "explicit upstream":
				values["task_source_kind"] = "upstream_job"
				delete(values, "extensions")
				delete(values, "media_types")
				delete(values, "max_bytes")
			case "unknown kind":
				values["task_source_kind"] = "url"
				delete(values, "extensions")
			case "asset type":
				values["type"] = "asset"
			case "empty media":
				delete(values, "media_types")
			case "wildcard media":
				values["media_types"] = []string{"model/*"}
			case "empty extension":
				delete(values, "extensions")
			case "zero bytes":
				values["max_bytes"] = 0
			case "empty offering":
				values["task_offerings"] = []string{" "}
			case "duplicate offering":
				values["task_offerings"] = []string{"local/forward", "local/forward"}
			}
			raw, _ := json.Marshal(values)
			var parameter schema.Parameter
			_ = json.Unmarshal(raw, &parameter)
			_, err := validateValueDefinition(parameter)
			valid := name == "valid" || name == "default upstream" || name == "explicit upstream"
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
		})
	}
}

func TestTaskSourceOfferingReferencesAreValidatedDuringLoad(t *testing.T) {
	for _, name := range []string{"valid", "unknown", "wrong capability", "wrong provider"} {
		t.Run(name, func(t *testing.T) {
			var doc map[string]any
			if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
				t.Fatal(err)
			}
			for _, entry := range doc["offerings"].([]any) {
				o := entry.(map[string]any)
				if o["id"] != "meshy/processing/remesh" {
					continue
				}
				for _, item := range o["parameters"].([]any) {
					p := item.(map[string]any)
					if p["name"] != "source_task" {
						continue
					}
					p["task_offerings"] = []string{"meshy/meshy-6/text-to-3d"}
					// Use an existing source ID from the same current Catalog.
					for _, candidate := range doc["offerings"].([]any) {
						source := candidate.(map[string]any)
						if source["provider_id"] == "meshy" && source["capability"] == "text-to-3d" {
							p["task_offerings"] = []string{source["id"].(string)}
							break
						}
					}
					switch name {
					case "unknown":
						p["task_offerings"] = []string{"missing/source"}
					case "wrong capability":
						p["task_capabilities"] = []string{"rigging"}
					case "wrong provider":
						p["task_offerings"] = []string{"tripo/tripo-v3.1/text-to-3d"}
					}
				}
			}
			raw, _ := json.Marshal(doc)
			_, err := Load(bytes.NewReader(raw))
			if name == "valid" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "task_offerings") {
				t.Fatalf("invalid source declaration accepted or wrong error: %v", err)
			}
		})
	}
}

func TestTaskSourceDiscoveryRequiresCompleteSourceContract(t *testing.T) {
	var doc document
	if err := json.Unmarshal(defaultCatalog, &doc); err != nil {
		t.Fatal(err)
	}
	offerings := generationMap{"local/convert": {Parameters: []schema.Parameter{{Type: "task", TaskSourceKind: schema.TaskSourceAssets}}}}
	for _, missing := range []string{"", "task_source_kind", "task_offerings", "task_capabilities", "same_provider"} {
		t.Run("missing_"+missing, func(t *testing.T) {
			discovery := doc.Application.ProviderDiscovery
			switch missing {
			case "task_source_kind":
				discovery.API.InputFields.TaskSourceKind = ""
			case "task_offerings":
				discovery.API.InputFields.TaskOfferings = ""
			case "task_capabilities":
				discovery.API.InputFields.TaskCapabilities = ""
			case "same_provider":
				discovery.API.InputFields.SameProvider = ""
			}
			err := validateTaskSourceDiscovery(discovery, offerings)
			if missing == "" && err != nil {
				t.Fatal(err)
			}
			if missing != "" && (err == nil || !strings.Contains(err.Error(), missing)) {
				t.Fatalf("missing source wire accepted or wrong error: %v", err)
			}
		})
	}
	legacy := generationMap{"provider/convert": {Parameters: []schema.Parameter{{Type: "task"}}}}
	if err := validateTaskSourceDiscovery(ProviderDiscoverySchema{}, legacy); err != nil {
		t.Fatalf("legacy provider dependency changed: %v", err)
	}
}

// #325: the age is a positive number of seconds on an upstream_job task reference only.
func TestUpstreamJobMaxAgeContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		raw   string
		valid bool
	}{
		{"upstream task", `{"name":"source_task","type":"task","upstream_job_max_age_seconds":172800}`, true},
		{"explicit upstream", `{"name":"source_task","type":"task","task_source_kind":"upstream_job","upstream_job_max_age_seconds":60}`, true},
		{"negative", `{"name":"source_task","type":"task","upstream_job_max_age_seconds":-1}`, false},
		{"asset parameter", `{"name":"source_asset","type":"asset","upstream_job_max_age_seconds":60}`, false},
		{"stored bytes", `{"name":"source_task","type":"task","task_source_kind":"assets","task_offerings":["local/forward"],"media_types":["model/gltf-binary"],"extensions":[".glb"],"max_bytes":1024,"upstream_job_max_age_seconds":60}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var parameter schema.Parameter
			if err := json.Unmarshal([]byte(tc.raw), &parameter); err != nil {
				t.Fatal(err)
			}
			_, err := validateValueDefinition(parameter)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v err=%v", tc.valid, err)
			}
		})
	}
}
