package catalog

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/providers/meshy"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #189: every template is enabled after one isolated public Prepare/Grant/Task
// per stage on the dev-test host (first batch: fridge magnet, figure, vinyl
// figure, brick figure, collapsible fidget; second batch: keychain, lampshade,
// pixel fidget, keycap). Direct upstream success alone never qualified one.
// #100: Plinth is a game-art production platform, so every Creative Lab product is defined but not
// offered to users or the Agent (enabled=false). Setting a product back to true restores it.
var creativeLabEnabledProducts = map[string]bool{"keychain": false, "fridge-magnet": false, "figure": false, "vinyl-figure": false, "brick-figure": false, "lamp": false, "keycap": false, "fidget-pixel": false, "fidget-collapsible": false}

func TestCreativeLabCandidatesDeclareNineProductsAndSeventeenBoundStages(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	products := map[string][]int{"keychain": {6, 30}, "fridge-magnet": {6, 30}, "figure": {6, 30}, "vinyl-figure": {6, 30}, "brick-figure": {6, 30}, "lamp": {30, 6}, "keycap": {12, 50}, "fidget-pixel": {6, 30}, "fidget-collapsible": {6}}
	count, total := 0, 0
	for product, prices := range products {
		stages := []string{"prototype", "build"}
		if len(prices) == 1 {
			stages = []string{"generate"}
		}
		for i, stage := range stages {
			id := schema.OfferingID("meshy/creative-lab/" + product + "/" + stage)
			o, ok := c.Get(id)
			if !ok {
				t.Fatalf("missing %s", id)
			}
			count++
			total += o.EstimatedCredits
			if o.Enabled != creativeLabEnabledProducts[product] || o.Default || o.IdempotentSubmission || o.Retry.MaxAttempts != 1 || len(o.Retry.Retryable) != 0 || o.EstimatedCredits != prices[i] || !o.CreditsMetered {
				t.Fatalf("unsafe candidate contract %s", id)
			}
			if _, _, err := c.Normalize(o.Capability, id, json.RawMessage(`{}`)); err == nil {
				t.Fatalf("candidate without required input selectable: %s", id)
			}
			if err := meshy.ValidateOffering(o); err != nil {
				t.Fatalf("%s: %v", id, err)
			}
			var wire struct {
				Endpoint       string              `json:"endpoint"`
				APIVersion     string              `json:"api_version"`
				TaskType       string              `json:"task_type"`
				ModelParameter string              `json:"model_parameter"`
				SourceResults  []map[string]string `json:"source_result_bindings"`
			}
			if err := json.Unmarshal(o.ProviderOptions, &wire); err != nil {
				t.Fatal(err)
			}
			endpoint := product + "/v1/" + stage
			taskType := "creative-lab-" + product + "-" + stage
			if stage == "generate" {
				endpoint = product + "/v1"
				taskType = "creative-lab-" + product
			}
			if wire.Endpoint != endpoint || wire.APIVersion != "creative-lab" || wire.TaskType != taskType || wire.ModelParameter != "" {
				t.Fatalf("wrong route/type/model selector %s: %+v", id, wire)
			}
			if stage == "build" {
				p := specialParameter(t, o, "source_task")
				if !p.Required || !p.SameProvider || !p.SameSession || p.TaskSourceKind != schema.TaskSourceUpstreamJob || !slices.Equal(p.TaskOfferings, []schema.OfferingID{schema.OfferingID("meshy/creative-lab/" + product + "/prototype")}) {
					t.Fatalf("unbound prototype %s", id)
				}
				if len(wire.SourceResults) != 1 || wire.SourceResults[0]["task_type"] != "creative-lab-"+product+"-prototype" || wire.SourceResults[0]["paired_field"] != "image_urls" {
					t.Fatalf("source result identity missing %s", id)
				}
				if product == "keycap" && (p.TaskSourceBinding != "provider" || wire.SourceResults[0]["value_field"] != "candidate_ids" || wire.SourceResults[0]["wire_name"] != "candidate_id") {
					t.Fatal("missing private candidate binding")
				}
			} else {
				p := specialParameter(t, o, "source_image")
				if p.Type != "asset" || !p.Required || p.MaxBytes != 20000000 || p.WireName != "image_url" || !slices.Equal(p.MediaTypes, []string{"image/jpeg", "image/png", "image/webp"}) {
					t.Fatalf("wrong image source %s", id)
				}
			}
			for _, p := range o.Parameters {
				if p.Name == "candidate_id" || strings.Contains(p.Name, "model_version") {
					t.Fatalf("private value exposed %s", id)
				}
			}
			for _, out := range o.Outputs {
				if out.Minimum == nil || out.MaxBytes <= 0 || len(out.MediaTypes) == 0 || out.Delivery == "" {
					t.Fatalf("unbounded output %s: %+v", id, out)
				}
			}
		}
	}
	if count != 17 || total != 320 {
		t.Fatalf("stages=%d quoted=%d", count, total)
	}
}

func TestCreativeLabGeometryConstraintsAndQualityDefaults(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, product := range []string{"keychain", "fridge-magnet"} {
		o, _ := c.Get(schema.OfferingID("meshy/creative-lab/" + product + "/build"))
		quality := specialParameter(t, o, "export_resolution")
		if quality.Fixed || string(quality.Default) != "512" || quality.Maximum == nil || *quality.Maximum != 512 || quality.Minimum == nil || *quality.Minimum != 64 {
			t.Fatal("quality must follow the current official range and highest default")
		}
		// The official Keychain and Fridge Magnet build pages define 0 as
		// "use the default" for size_mm, curve_param and relief_scale.
		for _, name := range []string{"size_mm", "curve_param", "relief_scale"} {
			frozen, err := normalizeInput(o, json.RawMessage(`{"source_task":"task","`+name+`":0}`))
			if err != nil {
				t.Fatalf("rejected documented zero default for %s: %v", name, err)
			}
			var values map[string]any
			if err := json.Unmarshal(frozen, &values); err != nil {
				t.Fatal(err)
			}
			if values[name] != float64(0) {
				t.Fatalf("%s=0 frozen as %v", name, values[name])
			}
			description := specialParameter(t, o, name).Description
			if !strings.Contains(description, "0") || !strings.Contains(description, "默认") {
				t.Fatalf("%s description does not explain the zero default: %q", name, description)
			}
		}
		for _, input := range []string{
			`{"source_task":"task","size_mm":-1}`,
			`{"source_task":"task","size_mm":401}`,
			`{"source_task":"task","curve_param":-0.1}`,
			`{"source_task":"task","curve_param":10.1}`,
			`{"source_task":"task","relief_scale":-0.1}`,
			`{"source_task":"task","relief_scale":10.1}`,
			`{"source_task":"task","export_resolution":63}`,
			`{"source_task":"task","export_resolution":513}`,
			`{"source_task":"task","export_resolution":256.5}`,
		} {
			if _, err := normalizeInput(o, json.RawMessage(input)); err == nil {
				t.Fatalf("accepted out-of-range %s", input)
			}
		}
		if _, err := normalizeInput(o, json.RawMessage(`{"source_task":"task","badge_shape":"rounded-rect","export_resolution":256}`)); err != nil {
			t.Fatal(err)
		}
	}
	pixel, _ := c.Get("meshy/creative-lab/fidget-pixel/build")
	for _, input := range []string{`{"source_task":"task","shape":"hex","piece_size_mm":"10"}`, `{"source_task":"task","color_count":9}`, `{"source_task":"task","piece_height_mm":10.5}`} {
		if _, err := normalizeInput(pixel, json.RawMessage(input)); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	if _, err := normalizeInput(pixel, json.RawMessage(`{"source_task":"task","shape":"hex","piece_size_mm":"6"}`)); err != nil {
		t.Fatal(err)
	}
	lamp, _ := c.Get("meshy/creative-lab/lamp/build")
	if _, err := normalizeInput(lamp, json.RawMessage(`{"source_task":"task","thickness_mm":0}`)); err == nil {
		t.Fatal("zero wall accepted")
	}
	collapsible, _ := c.Get("meshy/creative-lab/fidget-collapsible/generate")
	if len(collapsible.Parameters) != 1 {
		t.Fatal("web-only collapsible controls exposed")
	}
}

func TestCreativeSourceBindingInvalidDeclarationsFailBeforeRuntime(t *testing.T) {
	for _, name := range []string{"same-session-on-image", "unknown-binding", "asset-task-binding", "optional-binding", "cross-provider-binding", "multiple-source-offerings", "retained-without-identity", "identity-without-retention", "retained-background", "retained-many", "retained-prefix", "retained-post-success"} {
		t.Run(name, func(t *testing.T) {
			var d map[string]any
			if err := json.Unmarshal(defaultCatalog, &d); err != nil {
				t.Fatal(err)
			}
			var prototype, build map[string]any
			for _, raw := range d["offerings"].([]any) {
				o := raw.(map[string]any)
				if o["id"] == "meshy/creative-lab/keycap/prototype" {
					prototype = o
				}
				if o["id"] == "meshy/creative-lab/keycap/build" {
					build = o
				}
			}
			source := build["parameters"].([]any)[0].(map[string]any)
			output := prototype["outputs"].([]any)[0].(map[string]any)
			switch name {
			case "same-session-on-image":
				prototype["parameters"].([]any)[0].(map[string]any)["same_session"] = true
			case "unknown-binding":
				source["task_source_binding"] = "upstream-get"
			case "asset-task-binding":
				source["task_source_kind"] = "assets"
			case "optional-binding":
				source["required"] = false
			case "cross-provider-binding":
				source["same_provider"] = false
			case "multiple-source-offerings":
				source["task_offerings"] = []any{"meshy/creative-lab/keycap/prototype", "meshy/creative-lab/keychain/prototype"}
			case "retained-without-identity":
				delete(prototype["provider_options"].(map[string]any), "result_identity")
			case "identity-without-retention":
				delete(output, "retain_dependency_identity")
			case "retained-background":
				output["delivery"] = "background"
			case "retained-many":
				output["minimum"] = 2
			case "retained-prefix":
				output["variant_prefix"] = "image."
			case "retained-post-success":
				prototype["post_success"] = map[string]any{"observe": true}
			}
			raw, _ := json.Marshal(d)
			if _, err := Load(bytes.NewReader(raw)); err == nil {
				t.Fatal("unsupported source/retained identity contract accepted")
			}
		})
	}
}

func TestCreativeLabOfferingsAreDefinedButNeverListed(t *testing.T) {
	c, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	listed := map[schema.OfferingID]bool{}
	for _, o := range c.List("") {
		listed[o.ID] = true
	}
	defined := 0
	for _, capability := range []schema.Capability{"creative-lab-prototype", "creative-lab-build"} {
		if len(c.List(capability)) != 0 {
			t.Fatalf("%s must list no Offering", capability)
		}
	}
	for product := range creativeLabEnabledProducts {
		for _, stage := range []string{"prototype", "build", "generate"} {
			id := schema.OfferingID("meshy/creative-lab/" + product + "/" + stage)
			o, ok := c.Get(id)
			if !ok {
				continue
			}
			defined++
			if o.Enabled || listed[id] || len(o.ProviderOptions) == 0 {
				t.Fatalf("%s must stay defined with its wire mapping but unlisted", id)
			}
		}
	}
	if defined != 17 {
		t.Fatalf("defined Creative Lab Offerings = %d", defined)
	}
	for _, model := range c.Application().ProviderDiscovery.LegacyModels {
		if model.ProviderKey == "meshy-creative-lab" || strings.HasPrefix(model.ModelID, "creative-lab-") {
			t.Fatalf("discovery lists hidden model %s", model.ModelID)
		}
	}
}
