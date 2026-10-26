package catalog

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
)

func TestRigCheckOfferingHasNativeSourceAndRequiredBoundedReport(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	o, ok := c.Get("tripo/processing/rig-check")
	if !ok {
		t.Fatal("missing independent Rig Check offering")
	}
	if !o.Enabled || o.Capability != "rig-check" || o.ModelID != "tripo-rig-check" || o.EstimatedCredits != 0 || !o.CreditsMetered || o.Retry.MaxAttempts != 1 || len(o.Retry.Retryable) != 0 || o.IdempotentSubmission {
		t.Fatalf("unsafe offering contract: %+v", o)
	}
	if len(o.Parameters) != 1 {
		t.Fatal("offering must expose only native task input")
	}
	p := o.Parameters[0]
	if p.Name != "source_task" || p.Type != "task" || p.Semantic != schema.SemanticSourceTask || !p.Required || !p.SameProvider || p.TaskSourceKind != schema.TaskSourceUpstreamJob || p.WireName != "input" || !slices.Equal(p.TaskCapabilities, []schema.Capability{schema.TextTo3D, schema.ImageTo3D}) || !slices.Equal(p.TaskOfferings, []schema.OfferingID{"tripo/tripo-v3.1-full/text-to-3d", "tripo/tripo-v3.1-full/image-to-3d"}) {
		t.Fatalf("wrong native source: %+v", p)
	}
	if len(o.Outputs) != 1 {
		t.Fatal("one report output required")
	}
	out := o.Outputs[0]
	if out.Role != "other" || out.Variant != "report" || out.Delivery != "required" || out.Minimum == nil || *out.Minimum != 1 || out.MaxBytes != 32768 || !slices.Equal(out.MediaTypes, []string{"application/vnd.art-ai.rig-check-report+json"}) {
		t.Fatalf("wrong report slot: %+v", out)
	}
	var wire map[string]any
	if err = json.Unmarshal(o.ProviderOptions, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 2 || wire["endpoint"] != "animations/rig-check" || wire["observe_endpoint"] != "tasks/{task_id}" {
		t.Fatalf("wire must not imply upload/model/URL aliases: %+v", wire)
	}
	app := c.Application()
	if app.Content.Messages["ui.studio.stage.action.process_hint.rig-check"] == "" {
		t.Fatal("missing Schema hint for the declared processing capability")
	}
	found := false
	for _, op := range app.Chat.Harness.Operations {
		if op.ID == "rig-check" {
			found = true
			if op.UnavailableReason != "" || op.UploadedSourceUnavailableReason == "" || op.Capability != "rig-check" || op.ToolName != "process_model" {
				t.Fatalf("operation must be enabled for native sources and explicitly unavailable for uploads: %+v", op)
			}
		}
	}
	if !found {
		t.Fatal("missing Harness operation")
	}
	legacy := 0
	for _, entry := range app.ProviderDiscovery.LegacyModels {
		if entry.ModelID == "tripo-rig-check" {
			legacy++
			if entry.ProviderKey != "tripo-rig-check" {
				t.Fatalf("wrong legacy provider key: %+v", entry)
			}
		}
	}
	if legacy != 1 {
		t.Fatalf("enabled Rig Check legacy model count = %d, want 1", legacy)
	}
}

func TestRigCheckReportPresentationIsPublicWithoutPrintProtocolReuse(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(c.Application().Public())
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	assets := v["task_history"].(map[string]any)["assets"].(map[string]any)
	r, ok := assets["rig_check_report"].(map[string]any)
	if !ok {
		t.Fatal("missing public Rig Check presentation")
	}
	if r["format"] != "art-ai.rig-check-report.v1" || r["media_type"] != "application/vnd.art-ai.rig-check-report+json" || r["max_bytes"] != float64(32768) {
		t.Fatalf("wrong protocol: %+v", r)
	}
	for _, k := range []string{"title", "loading", "load_failed", "retry", "unsupported", "limitation"} {
		if r[k] == nil || r[k] == "" {
			t.Fatalf("missing Schema copy %s", k)
		}
	}
	labels, ok := r["boolean_labels"].(map[string]any)
	if !ok || labels["false"] == nil || labels["true"] == nil {
		t.Fatal("false must have an explicit label")
	}
}

func TestRigCheckReportPresentationRejectsAmbiguousProtocolAndMissingLabels(t *testing.T) {
	cases := map[string]func(*RigCheckReportPresentationSchema){
		"wrong format":       func(r *RigCheckReportPresentationSchema) { r.Format = "art-ai.print-report.v1" },
		"generic MIME":       func(r *RigCheckReportPresentationSchema) { r.MediaType = "application/json" },
		"print MIME":         func(r *RigCheckReportPresentationSchema) { r.MediaType = "application/vnd.art-ai.print-report+json" },
		"zero budget":        func(r *RigCheckReportPresentationSchema) { r.MaxBytes = 0 },
		"oversized budget":   func(r *RigCheckReportPresentationSchema) { r.MaxBytes = 32769 },
		"missing false":      func(r *RigCheckReportPresentationSchema) { delete(r.BooleanLabels, "false") },
		"unknown species":    func(r *RigCheckReportPresentationSchema) { r.SpeciesLabels["humanoid"] = "Other" },
		"missing species":    func(r *RigCheckReportPresentationSchema) { delete(r.SpeciesLabels, "avian") },
		"missing limitation": func(r *RigCheckReportPresentationSchema) { r.Limitation = "" },
		"empty field":        func(r *RigCheckReportPresentationSchema) { r.FieldLabels["riggable"] = " " },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c, err := LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			mutate(&app.TaskHistory.Assets.RigCheckReport)
			if validateTaskHistory(app.TaskHistory) == nil {
				t.Fatal("accepted unsafe report presentation")
			}
		})
	}
}
func TestRigCheckReportPresentationMapsAreIndependentOfCatalog(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.TaskHistory.Assets.RigCheckReport.FieldLabels["riggable"] = "changed"
	app.TaskHistory.Assets.RigCheckReport.BooleanLabels["false"] = "changed"
	app.TaskHistory.Assets.RigCheckReport.SpeciesLabels["avian"] = "changed"
	current := c.Application().TaskHistory.Assets.RigCheckReport
	if current.FieldLabels["riggable"] == "changed" || current.BooleanLabels["false"] == "changed" || current.SpeciesLabels["avian"] == "changed" {
		t.Fatal("caller mutated Catalog presentation")
	}
}

func TestRigCheckPresentationRejectsObsoleteUnknownValueField(t *testing.T) {
	raw, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err = json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	app := doc["application"].(map[string]any)
	report := app["task_history"].(map[string]any)["assets"].(map[string]any)["rig_check_report"].(map[string]any)
	report["unknown_value"] = "must not conceal a missing required species"
	changed, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Load(bytes.NewReader(changed)); err == nil {
		t.Fatal("accepted obsolete Rig Check unknown-value fallback")
	}
}
