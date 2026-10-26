package app

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestAgentOfferingProjectionIncludesProcessingDependenciesAndCredits(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Capabilities []agentCapabilityView `json:"capabilities"`
	}
	if err := json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil), &projection); err != nil {
		t.Fatal(err)
	}
	for _, capability := range projection.Capabilities {
		if capability.Capability != "animation" {
			continue
		}
		for _, offering := range capability.Offerings {
			if offering.OfferingID != "meshy/processing/animation" {
				continue
			}
			if offering.EstimatedCredits != 3 || len(offering.Sources) != 1 || offering.Sources[0].Parameter != "source_task" || len(offering.Sources[0].TaskCapabilities) != 1 || offering.Sources[0].TaskCapabilities[0] != "rigging" {
				t.Fatalf("animation projection = %#v", offering)
			}
			return
		}
	}
	t.Fatal("animation offering missing")
}

func TestAgentOfferingProjectionListsActionChoicesAndExtraCredits(t *testing.T) {
	// Conversion ships disabled (the 3D stage reads GLB only); the projection
	// of its extra-credit inputs is still checked with it enabled.
	product := catalogtest.LoadDefaultWithTripoConversion(t)
	var projection struct {
		Capabilities []agentCapabilityView `json:"capabilities"`
	}
	if err := json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil), &projection); err != nil {
		t.Fatal(err)
	}
	find := func(id string) agentOfferingView {
		for _, capability := range projection.Capabilities {
			for _, offering := range capability.Offerings {
				if offering.OfferingID == id {
					return offering
				}
			}
		}
		t.Fatalf("offering %s missing", id)
		return agentOfferingView{}
	}
	animation := find("meshy/processing/animation")
	var action *agentInputView
	for index := range animation.Inputs {
		if animation.Inputs[index].Name == "action_id" {
			action = &animation.Inputs[index]
		}
	}
	if action == nil || !action.Required || len(action.Options) != 0 || !strings.HasPrefix(action.OptionsCompact, "0:Idle,1:Walking_Woman,") {
		t.Fatalf("animation action input = %#v", action)
	}
	conversion := find("tripo/tripo-v3.1/format-conversion")
	if conversion.ExtraCredits <= 0 || len(conversion.ExtraCreditParameters) == 0 {
		t.Fatalf("format conversion extra credits = %#v", conversion)
	}
	for _, input := range conversion.Inputs {
		if input.Name == "authorize_extra_credits" || input.Name == "source_task" {
			t.Fatalf("input %s must not be exposed to the Agent", input.Name)
		}
	}
}

func TestAgentOfferingProjectionOmitsDisabledFormatConversion(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Capabilities []agentCapabilityView `json:"capabilities"`
	}
	if err := json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil), &projection); err != nil {
		t.Fatal(err)
	}
	for _, capability := range projection.Capabilities {
		for _, offering := range capability.Offerings {
			if offering.OfferingID == "tripo/tripo-v3.1/format-conversion" {
				t.Fatalf("disabled format conversion offered to the Agent: %#v", offering)
			}
		}
	}
}

func TestAgentGenerationAdviceUsesTheSamePublicDefaultAndBounds(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var projection struct {
		Capabilities []agentCapabilityView `json:"capabilities"`
	}
	if err = json.Unmarshal(buildAgentOfferingsProjection(product.Application().Capabilities, product, nil), &projection); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, capability := range projection.Capabilities {
		for _, o := range capability.Offerings {
			if o.OfferingID != "openai/gpt-image-2/text-to-image" {
				continue
			}
			for _, p := range o.Inputs {
				if p.Name == "moderation" {
					t.Fatal("hidden protocol parameter exposed")
				}
				if p.Name == "quality" {
					found = true
					if p.AdjustmentPolicy != "advised" || p.AdjustmentReason == "" || string(p.Default) != `"max"` || len(p.Options) < 2 {
						t.Fatalf("advice projection=%+v", p)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("generation advised parameter missing")
	}
}

// #265: the Meshy action library has 656 entries. The Agent projection is read
// into LLM context on every list_offerings call, so a large option list is
// sent as one compact "value:OfficialName" list; the public Schema keeps the
// full Chinese labels and groups. Small option lists keep the full form.
func TestAgentProjectionCompactsLargeOptionLists(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil)
	var projection struct {
		Capabilities []agentCapabilityView `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	var action, quality *agentInputView
	for _, capability := range projection.Capabilities {
		for _, offering := range capability.Offerings {
			for index := range offering.Inputs {
				input := &offering.Inputs[index]
				if offering.OfferingID == "meshy/processing/animation" && input.Name == "action_id" {
					action = input
				}
				if offering.OfferingID == "openai/gpt-image-2/text-to-image" && input.Name == "quality" {
					quality = input
				}
			}
		}
	}
	if action == nil || quality == nil {
		t.Fatalf("inputs missing: action=%v quality=%v", action != nil, quality != nil)
	}
	encoded, _ := json.Marshal(action)
	if len(encoded) > 16*1024 || len(action.Options) != 0 {
		t.Fatalf("action_id projection = %d bytes, %d full options", len(encoded), len(action.Options))
	}
	items := strings.Split(action.OptionsCompact, ",")
	if len(items) != 656 || !slices.Contains(items, "4:Attack") || !slices.Contains(items, "290:Wave_One_Hand") || strings.ContainsAny(action.OptionsCompact, "（(日") {
		t.Fatalf("compact actions = %d items: %.120s", len(items), action.OptionsCompact)
	}
	if len(quality.Options) < 2 || quality.Options[0].Label == "" || quality.OptionsCompact != "" {
		t.Fatalf("small option list changed: %#v", quality)
	}
	// #189 enabled the 17 Creative Lab stages (about 9 KB of inputs: the relief,
	// lampshade, keycap and pixel build options), so the budget moved from 64 to
	// 80 KiB; the full default projection was 69,783 bytes on 2026-10-22.
	if len(raw) > 80*1024 {
		t.Fatalf("agent list_offerings projection = %d bytes", len(raw))
	}
}

// #321 I: list_offerings showed "estimated_credits":55 and "extra_credits":10
// for Tripo Smart Segmentation, and the Agent told the person "about 10 more
// credits if you pick extra formats"; the five formats are fixed and cost 50.
// The projection now prices the default input as the step will be frozen.
func TestAgentOfferingProjectionPricesTheFixedExtraFormats(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil)
	var projection struct {
		Capabilities []struct {
			Offerings []map[string]json.RawMessage `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	for _, capability := range projection.Capabilities {
		for _, offering := range capability.Offerings {
			if string(offering["offering_id"]) != `"tripo/geometry/smartsegment-model"` {
				continue
			}
			var quote struct {
				Base       int    `json:"base_credits"`
				Extra      int    `json:"extra_credits"`
				ExtraFor   string `json:"extra_for"`
				ExtraFixed bool   `json:"extra_fixed"`
				Total      int    `json:"total_credits"`
			}
			if err := json.Unmarshal(offering["default_quote"], &quote); err != nil {
				t.Fatalf("default_quote = %s: %v", offering["default_quote"], err)
			}
			if quote.Base != 55 || quote.Extra != 50 || quote.Total != 105 || !quote.ExtraFixed || !strings.Contains(quote.ExtraFor, "USDZ") {
				t.Fatalf("default_quote = %+v", quote)
			}
			return
		}
	}
	t.Fatal("Tripo Smart Segmentation missing from the projection")
}

// projectionOfferingWire is one offering of the JSON the Agent's and MCP's
// list_offerings tools receive, kept raw so the test can tell an absent
// capacity from a zero one.
type projectionOfferingWire map[string]json.RawMessage

func projectionWireOfferings(t *testing.T, raw json.RawMessage) map[string]projectionOfferingWire {
	t.Helper()
	var wire struct {
		Capabilities []struct {
			Offerings []projectionOfferingWire `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	result := map[string]projectionOfferingWire{}
	for _, capability := range wire.Capabilities {
		for _, offering := range capability.Offerings {
			var id string
			if err := json.Unmarshal(offering["offering_id"], &id); err != nil {
				t.Fatalf("offering_id = %s: %v", offering["offering_id"], err)
			}
			result[id] = offering
		}
	}
	return result
}

// #356: list_offerings hid asset-array inputs, so the Agent could not compare
// how many reference pictures the models take and told the person that no
// model takes more than the selected one. The projection now carries the
// read-only scalar reference_image_max for a reference-image list only.
func TestAgentOfferingProjectionStatesReferenceImageCapacity(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil)
	offerings := projectionWireOfferings(t, raw)
	for id, want := range map[string]string{
		"hyper3d/rodin-gen-2.5/image-to-3d":     "5",
		"meshy/meshy-7.1-multiview/image-to-3d": "4",
		"meshy/meshy-7.1/image-to-3d":           "1",
		"meshy/meshy-t2.5/image-to-3d":          "1",
		"tripo/tripo-v3.1-full/image-to-3d":     "1",
	} {
		offering, ok := offerings[id]
		if !ok {
			t.Fatalf("%s missing from the projection", id)
		}
		if got := string(offering["reference_image_max"]); got != want {
			t.Errorf("%s reference_image_max = %q, want %q", id, got, want)
		}
		// The capacity is read-only: the reference and texture lists remain
		// absent from the writable inputs the Agent may set.
		var inputs []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		}
		if err := json.Unmarshal(offering["inputs"], &inputs); err != nil {
			t.Fatalf("%s inputs = %s: %v", id, offering["inputs"], err)
		}
		for _, input := range inputs {
			if input.Type == "asset" || input.Type == "asset-array" {
				t.Errorf("%s exposes asset input %s (%s) to the Agent", id, input.Name, input.Type)
			}
		}
	}
	// A model without a reference-image list states no capacity; the field is
	// absent, never a made-up one.
	for _, id := range []string{"meshy/meshy-7.1/text-to-3d", "openai/gpt-image-2/text-to-image"} {
		offering, ok := offerings[id]
		if !ok {
			t.Fatalf("%s missing from the projection", id)
		}
		if _, present := offering["reference_image_max"]; present {
			t.Errorf("%s declares reference_image_max without a reference-image list: %s", id, offering["reference_image_max"])
		}
	}
	if len(raw) > 80*1024 {
		t.Fatalf("agent list_offerings projection = %d bytes", len(raw))
	}
}

// #356: unavailable models stay filtered out with the new scalar; a disabled or
// unconfigured model must not reappear just because its capacity is known.
func TestAgentOfferingProjectionKeepsAvailabilityFilterWithReferenceCapacity(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	available := func(id generation.OfferingID) bool { return id == "meshy/meshy-7.1/image-to-3d" }
	offerings := projectionWireOfferings(t, buildAgentOfferingsProjection(product.Application().Capabilities, product, available))
	if _, present := offerings["hyper3d/rodin-gen-2.5/image-to-3d"]; present {
		t.Fatal("unavailable Rodin reappeared in the Agent projection")
	}
	offering, ok := offerings["meshy/meshy-7.1/image-to-3d"]
	if !ok || string(offering["reference_image_max"]) != "1" {
		t.Fatalf("available Meshy 7.1 = %v", offering)
	}
}

// adjustedOfferings lets a test change the Schema's declared reference list
// without touching the loaded Catalog.
type adjustedOfferings struct {
	catalog *catalog.Catalog
	adjust  func(*generation.Offering)
}

func (a adjustedOfferings) List(capability generation.Capability) []generation.Offering {
	offerings := a.catalog.List(capability)
	if a.adjust != nil {
		for index := range offerings {
			a.adjust(&offerings[index])
		}
	}
	return offerings
}

// #356: only a reference-image asset-array states the capacity. Another asset
// list (the multiview texture references) must not leak into it, and a list
// without a positive max_items stays absent rather than becoming 1.
func TestAgentOfferingProjectionDoesNotInferReferenceCapacityFromOtherArrays(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for name, test := range map[string]struct {
		reference, texture int
		present            bool
		want               string
	}{
		"texture list is not a reference list": {reference: 4, texture: 99, present: true, want: "4"},
		"no positive reference limit":          {reference: 0, texture: 99},
	} {
		t.Run(name, func(t *testing.T) {
			source := adjustedOfferings{catalog: product, adjust: func(offering *generation.Offering) {
				if offering.ID != "meshy/meshy-7.1-multiview/image-to-3d" {
					return
				}
				// The texture list goes first so reading the first asset array
				// would state 99 instead of the reference list's real value.
				for index := range offering.Parameters {
					if offering.Parameters[index].Name == "texture_assets" && index > 0 {
						offering.Parameters[0], offering.Parameters[index] = offering.Parameters[index], offering.Parameters[0]
						break
					}
				}
				if offering.Parameters[0].Name != "texture_assets" {
					t.Fatal("texture_assets is not before reference_assets")
				}
				for index := range offering.Parameters {
					switch offering.Parameters[index].Name {
					case "reference_assets":
						offering.Parameters[index].MaxItems = test.reference
					case "texture_assets":
						offering.Parameters[index].MaxItems = test.texture
					}
				}
			}}
			raw := buildAgentOfferingsProjection(product.Application().Capabilities, source, nil)
			offering := projectionWireOfferings(t, raw)["meshy/meshy-7.1-multiview/image-to-3d"]
			value, present := offering["reference_image_max"]
			if present != test.present || (present && string(value) != test.want) {
				t.Fatalf("reference_image_max = %s (present %t), want %q (present %t)", value, present, test.want, test.present)
			}
			var inputs []struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(offering["inputs"], &inputs); err != nil {
				t.Fatal(err)
			}
			for _, input := range inputs {
				if input.Name == "reference_assets" || input.Name == "texture_assets" {
					t.Fatalf("asset input %s is writable by the Agent", input.Name)
				}
			}
		})
	}
}
