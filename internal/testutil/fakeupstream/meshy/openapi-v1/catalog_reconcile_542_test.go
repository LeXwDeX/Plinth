package meshyopenapiv1

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// #542 (I12): the fake takes the Catalog's own default parameters of every
// Meshy Offering it models, as the Adapter would send them, and checks two
// things against the committed Catalog. The fake accepts the request (the
// Catalog never defaults to a combination the official page refuses), and the
// price the fake reports for it, the stages summed, equals the Offering's
// estimated_credits or native_pricing. A difference is not hidden: it is listed
// in knownPriceDifferences with its reason, and the test fails when a listed
// difference disappears or an unlisted one appears.

const catalogPath = "../../../../catalog/default_catalog.json"

type catalogFile struct {
	Offerings []map[string]any `json:"offerings"`
}

func loadMeshyOfferings(t *testing.T) map[string]map[string]any {
	t.Helper()
	raw, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var file catalogFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]any{}
	for _, offering := range file.Offerings {
		id, _ := offering["id"].(string)
		if strings.HasPrefix(id, "meshy/") && !strings.HasPrefix(id, "meshy/creative-lab/") {
			out[id] = offering
		}
	}
	return out
}

// defaultsOf is the wire body the Catalog's defaults make: every parameter with
// a default, under its wire name, minus the ones its omit_when rules drop for
// the stage (mode) being sent.
func defaultsOf(offering map[string]any, stage string) map[string]any {
	body := map[string]any{}
	parameters, _ := offering["parameters"].([]any)
	for _, raw := range parameters {
		parameter, _ := raw.(map[string]any)
		value, has := parameter["default"]
		if !has {
			continue
		}
		switch parameter["type"] {
		case "task", "asset", "text":
			continue
		}
		name, _ := parameter["name"].(string)
		if wire, _ := parameter["wire_name"].(string); wire != "" {
			name = wire
		}
		omitted := false
		conditions, _ := parameter["omit_when"].([]any)
		for _, rawCondition := range conditions {
			condition, _ := rawCondition.(map[string]any)
			if condition["parameter"] != "mode" {
				if condition["parameter"] == parameter["name"] && condition["operator"] == "equals" && condition["value"] == value {
					omitted = true
				}
				continue
			}
			equals := condition["value"] == stage
			if (condition["operator"] == "equals") == equals {
				omitted = true
			}
		}
		if !omitted {
			body[name] = value
		}
	}
	return body
}

// modelOf is the value of the ai_model wire field: the Offering's declared
// model_parameter_value, else its model id (the Adapter's own rule).
func modelOf(offering map[string]any) string {
	options, _ := offering["provider_options"].(map[string]any)
	if value, _ := options["model_parameter_value"].(string); value != "" {
		return value
	}
	model, _ := offering["model_id"].(string)
	return model
}

// knownPriceDifferences are the Offerings whose Catalog estimate differs from the
// price the fake derives from the official Pricing page for the Catalog's
// default parameters, with the reason. They are findings about the Catalog,
// recorded in #542, not changed here.
var knownPriceDifferences = map[string]string{
	"meshy/meshy-7/text-to-3d": "Catalog 25 is the preview alone (20 plus 5 for the deprecated ultra_mode the offering sends); the page prices the refine too, 10 for the default 2k texture, so the two stages cost 35",
}

// knownRefusals are the Offerings whose default wire body the fake refuses, with
// the reason. It is empty: the Smart Topology image-to-3d Offering used to send
// "pose_mode": "" (empty default, no omit_when), which the fake has refused since
// #460 B24; #539 gave it omit_when pose_mode == "", so the default body leaves
// pose_mode out like the Text to 3D page says.
var knownRefusals = map[string]string{}

func credits(body map[string]any) float64 {
	value, _ := body["consumed_credits"].(float64)
	return value
}

func TestCatalogDefaultsAreAcceptedAndPricedLikeTheEstimate(t *testing.T) {
	offerings := loadMeshyOfferings(t)
	f := newFixture(t, fakeupstream.Config{})
	source := f.source()
	text := f.create("/text-to-image", map[string]any{"ai_model": "nano-banana-pro", "prompt": "p"})
	f.finish("/text-to-image", text, key)

	type totals struct {
		price float64
		err   string
	}
	run := func(id string, offering map[string]any) totals {
		model := modelOf(offering)
		endpoint, _ := offering["provider_options"].(map[string]any)["endpoint"].(string)
		post := func(version, path string, body map[string]any) (map[string]any, string) {
			prefix := Prefix
			if version == "v2" {
				prefix = "/meshy/openapi/v2"
			}
			status, _, response := f.Do("POST", prefix+path, key, body)
			if status != 202 {
				return nil, fmt.Sprintf("POST %s = %d %v", path, status, response)
			}
			taskID := response["result"].(string)
			for i := 0; i < 8; i++ {
				_, _, polled := f.Do("GET", prefix+path+"/"+taskID, key, nil)
				if polled["status"] == "SUCCEEDED" {
					return polled, ""
				}
			}
			return nil, "task never finished"
		}
		switch endpoint {
		case "text-to-3d":
			previewBody := defaultsOf(offering, "preview")
			previewBody["ai_model"], previewBody["mode"], previewBody["prompt"] = model, "preview", "p"
			preview, message := post("v2", "/text-to-3d", previewBody)
			if message != "" {
				return totals{err: "preview: " + message}
			}
			refineBody := defaultsOf(offering, "refine")
			refineBody["mode"], refineBody["preview_task_id"] = "refine", preview["id"]
			delete(refineBody, "ai_model") // the Catalog omits it for a refine
			refine, message := post("v2", "/text-to-3d", refineBody)
			if message != "" {
				return totals{err: "refine: " + message}
			}
			return totals{price: credits(preview) + credits(refine)}
		case "image-to-3d", "multi-image-to-3d":
			body := defaultsOf(offering, "")
			body["ai_model"] = model
			if endpoint == "image-to-3d" {
				body["image_url"] = "https://example.test/a.png"
			} else {
				body["image_urls"] = []string{"https://example.test/a.png", "https://example.test/b.png"}
			}
			polled, message := post("v1", "/"+endpoint, body)
			return totals{price: credits(polled), err: message}
		case "text-to-image", "image-to-image":
			body := defaultsOf(offering, "")
			body["ai_model"], body["prompt"] = model, "p"
			if endpoint == "image-to-image" {
				body["reference_image_urls"] = []string{"https://example.test/a.png"}
			}
			polled, message := post("v1", "/"+endpoint, body)
			return totals{price: credits(polled), err: message}
		case "retexture":
			body := defaultsOf(offering, "")
			body["ai_model"], body["input_task_id"], body["text_style_prompt"] = model, source, "p"
			polled, message := post("v1", "/retexture", body)
			return totals{price: credits(polled), err: message}
		}
		return totals{err: "not modelled"}
	}

	var ids []string
	for id := range offerings {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	checked := 0
	for _, id := range ids {
		offering := offerings[id]
		endpoint, _ := offering["provider_options"].(map[string]any)["endpoint"].(string)
		switch endpoint {
		case "text-to-3d", "image-to-3d", "multi-image-to-3d", "text-to-image", "image-to-image", "retexture":
		default:
			continue
		}
		t.Run(id, func(t *testing.T) {
			got := run(id, offering)
			if reason, known := knownRefusals[id]; known {
				if got.err == "" {
					t.Fatalf("the fake accepts the defaults now; drop the known refusal (%s)", reason)
				}
				return
			}
			if got.err != "" {
				t.Fatalf("the fake refused the Catalog's defaults: %s", got.err)
			}
			checked++
			var want float64
			if pricing, ok := offering["native_pricing"].(map[string]any); ok {
				want = nativePrice(t, pricing, defaultsOf(offering, ""))
			} else {
				want, _ = offering["estimated_credits"].(float64)
			}
			reason, known := knownPriceDifferences[id]
			switch {
			case got.price != want && !known:
				t.Fatalf("fake price %v, Catalog %v: an unlisted difference", got.price, want)
			case got.price == want && known:
				t.Fatalf("fake price %v equals the Catalog now; drop the known difference (%s)", got.price, reason)
			}
		})
	}
	if checked < 9 {
		t.Fatalf("only %d Offerings were checked", checked)
	}
}

// nativePrice evaluates a native_pricing declaration for the default
// parameters: the base amount plus every component whose condition holds.
func nativePrice(t *testing.T, pricing map[string]any, parameters map[string]any) float64 {
	t.Helper()
	amount := func(value any) float64 {
		var number float64
		text, _ := value.(string)
		if _, err := fmt.Sscanf(text, "%g", &number); err != nil {
			t.Fatalf("amount %v: %v", value, err)
		}
		return number
	}
	total := amount(pricing["base_amount"])
	components, _ := pricing["components"].([]any)
	for _, raw := range components {
		component, _ := raw.(map[string]any)
		when, _ := component["when"].(map[string]any)
		if when["operator"] == "equals" && parameters[when["parameter"].(string)] == when["value"] {
			total += amount(component["amount"])
		}
	}
	return total
}
