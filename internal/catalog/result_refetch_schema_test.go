package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The shipped Schema declares which provider connections can have an accepted
// result fetched again (#295): the job-based 3D and matting protocols, with
// Meshy's documented 72-hour API retention; never the one-shot image protocols.
func TestShippedResultRefetchDeclarations(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for key, hours := range map[string]int{"meshy.v2": 72, "tripo.v3": 0, "hyper3d.rodin.v1": 0, "matting.v1": 0} {
		policy, ok := c.ResultRefetchPolicy(key)
		if !ok || policy.Mode != "observe" || policy.RetentionHours != hours {
			t.Errorf("%s policy = %#v, %v", key, policy, ok)
		}
	}
	for _, key := range []string{"openai-images.v1", "gemini-images.v1", "jimeng-images.v1", "local.model", "local.recovery"} {
		if _, ok := c.ResultRefetchPolicy(key); ok {
			t.Errorf("%s declares result_refetch", key)
		}
	}
	app := c.Application()
	messages := app.Runtime.GenerationMessages
	if messages.RefetchSourceUnavailable == "" || messages.RefetchResultUnavailable == "" {
		t.Fatal("refetch failure texts missing")
	}
	if app.Runtime.HTTP.Routes.TaskRefetch != "/api/v1/tasks/{taskID}/refetch" {
		t.Fatalf("route = %q", app.Runtime.HTTP.Routes.TaskRefetch)
	}
	for _, key := range []string{"ui.task.refetch.action", "ui.task.refetch.description", "ui.task.refetch.unavailable.not_supported", "ui.task.refetch.unavailable.expired", "ui.task.refetch.unavailable.provider_unavailable", "ui.task.refetch.unavailable.repeated_failure"} {
		if app.Content.Messages[key] == "" || app.Localization.Bundles["en"].Messages[key] == "" {
			t.Errorf("message %s missing in a locale", key)
		}
	}
}

func mutatedCatalog(t *testing.T, mutate func(application map[string]any)) error {
	t.Helper()
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	mutate(document["application"].(map[string]any))
	raw, _ = json.Marshal(document)
	_, err = catalog.Load(bytes.NewReader(raw))
	return err
}

func connection(application map[string]any, id string) map[string]any {
	for _, item := range application["runtime"].(map[string]any)["provider_connections"].([]any) {
		entry := item.(map[string]any)
		if entry["id"] == id {
			return entry
		}
	}
	return nil
}

func TestResultRefetchDeclarationsAreValidated(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"one-shot image protocol": func(a map[string]any) {
			connection(a, "openai-images")["result_refetch"] = map[string]any{"mode": "observe"}
		},
		"unknown mode": func(a map[string]any) {
			connection(a, "tripo")["result_refetch"] = map[string]any{"mode": "download"}
		},
		"negative retention": func(a map[string]any) {
			connection(a, "meshy")["result_refetch"] = map[string]any{"mode": "observe", "retention_hours": -1}
		},
		"failure texts missing": func(a map[string]any) {
			delete(a["runtime"].(map[string]any)["generation_messages"].(map[string]any), "refetch_result_unavailable")
		},
		"agent texts missing": func(a map[string]any) {
			delete(a["chat"].(map[string]any)["agent"].(map[string]any)["refetch_unavailable_content"].(map[string]any), "expired")
		},
		"repeated failure text missing": func(a map[string]any) {
			delete(a["chat"].(map[string]any)["agent"].(map[string]any)["refetch_unavailable_content"].(map[string]any), "repeated_failure")
		},
		"repeated failure text without its cause": func(a map[string]any) {
			a["chat"].(map[string]any)["agent"].(map[string]any)["refetch_unavailable_content"].(map[string]any)["repeated_failure"] = "cannot be fetched again"
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := mutatedCatalog(t, mutate); err == nil {
				t.Fatal("invalid result_refetch declaration loaded")
			}
		})
	}
}
