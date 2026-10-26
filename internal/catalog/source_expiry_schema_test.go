package catalog_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #325: Meshy keeps API tasks at most three days (docs.meshy.ai/en/api/asset-retention).
// Every Meshy parameter that hands an earlier Meshy job to the upstream declares
// the age after which the platform treats that job as gone.
const meshyUpstreamJobMaxAge = 172800

func TestMeshyUpstreamJobReferencesDeclareTheirAge(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, offering := range c.List("") {
		if offering.ProviderID != "meshy" {
			continue
		}
		for _, parameter := range offering.Parameters {
			if parameter.Type != "task" || parameter.TaskSourceKind == schema.TaskSourceAssets {
				continue
			}
			found++
			if parameter.UpstreamJobMaxAgeSeconds != meshyUpstreamJobMaxAge {
				t.Errorf("%s %s: upstream_job_max_age_seconds = %d", offering.ID, parameter.Name, parameter.UpstreamJobMaxAgeSeconds)
			}
		}
	}
	if found == 0 {
		t.Fatal("no Meshy upstream job reference found")
	}
	// The processing services Meshy documents with a model_url alternative keep a saved-file route.
	for _, id := range []string{"meshy/processing/retexture", "meshy/processing/remesh", "meshy/processing/rigging"} {
		offering, ok := c.Get(schema.OfferingID(id))
		if !ok {
			t.Fatalf("%s missing", id)
		}
		hasFile := false
		for _, parameter := range offering.Parameters {
			if role, source := schema.SourceParameterRole(parameter); source && role == schema.SemanticSourceAsset && parameter.WireName == "model_url" {
				hasFile = true
			}
		}
		if !hasFile {
			t.Errorf("%s has no model_url source", id)
		}
	}
}

func TestSourceExpiredFailureAndToolTextAreDeclared(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Application struct {
			Content struct {
				Messages map[string]string `json:"messages"`
			} `json:"content"`
			Localization struct {
				Bundles map[string]struct {
					Messages map[string]string `json:"messages"`
				} `json:"bundles"`
			} `json:"localization"`
			Chat struct {
				Tools []struct {
					Name       string `json:"name"`
					Processing *struct {
						// Values are texts, except the nested #321 service texts.
						Messages map[string]any `json:"messages"`
					} `json:"processing"`
				} `json:"tools"`
			} `json:"chat"`
		} `json:"application"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"failure.source_expired", "ui.workspace.processing.plan_source_expired",
		"ui.workspace.processing.source_refused_expired", "ui.workspace.processing.asset_route_expired",
		// #326: an old failed plan judged again, and a prerequisite that cannot run on the model.
		"ui.workspace.processing.plan_retry_source_expired", "ui.workspace.processing.plan_retry_source_incompatible",
		"ui.workspace.processing.plan_retry_saved_file", "ui.workspace.processing.prerequisite_refused",
		// #326 P: a paused plan judged again, and a failed chat step judged for regenerate.
		"ui.workspace.processing.plan_resume_source_expired", "ui.workspace.processing.plan_resume_source_incompatible",
		"ui.workspace.processing.plan_resume_frozen_expired", "ui.workspace.processing.plan_resume_saved_file",
		"ui.workspace.processing.plan_waiting_alternative", "ui.studio.result.source_retry.saved_file",
		"ui.studio.result.source_retry.source_expired", "ui.studio.result.source_retry.source_incompatible"} {
		if strings.TrimSpace(document.Application.Content.Messages[key]) == "" {
			t.Errorf("message %s missing", key)
		}
		if strings.TrimSpace(document.Application.Localization.Bundles["en"].Messages[key]) == "" {
			t.Errorf("en message %s missing", key)
		}
	}
	if !strings.Contains(document.Application.Content.Messages["ui.workspace.processing.source_refused_expired"], "{service}") {
		t.Error("source_refused_expired names no {service}")
	}
	tools := 0
	for _, tool := range document.Application.Chat.Tools {
		if tool.Processing == nil {
			continue
		}
		tools++
		if text, _ := tool.Processing.Messages["source_expired"].(string); !strings.Contains(text, "{step}") {
			t.Errorf("%s: source_expired = %q; want a {step} text", tool.Name, text)
		}
	}
	if tools == 0 {
		t.Fatal("no processing tool found")
	}
}
