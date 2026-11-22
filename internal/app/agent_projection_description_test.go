package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #130: list_offerings carries each Offering's Schema description, so the
// Agent learns that Meshy Remesh keeps neither textures nor size and that the
// Tripo decimation bakes textures. Offerings without one omit the field.
func TestAgentOfferingProjectionCarriesTheSchemaDescription(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil, "", "", true)
	var projection struct {
		Capabilities []struct {
			Capability string                       `json:"capability"`
			Offerings  []map[string]json.RawMessage `json:"offerings"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	for _, capability := range projection.Capabilities {
		for _, offering := range capability.Offerings {
			var id string
			if err := json.Unmarshal(offering["offering_id"], &id); err != nil {
				t.Fatal(err)
			}
			declared, ok := product.Get(generation.OfferingID(id))
			if !ok {
				t.Fatalf("projected offering %s is not in the catalog", id)
			}
			field, present := offering["description"]
			if declared.Description == "" {
				if present {
					t.Fatalf("%s projects a description it does not declare: %s", id, field)
				}
				continue
			}
			var description string
			if err := json.Unmarshal(field, &description); err != nil || description != declared.Description {
				t.Fatalf("%s description = %s, want the Schema text %q", id, field, declared.Description)
			}
			seen[id] = description
		}
	}
	remesh := seen["meshy/processing/remesh"]
	if !strings.Contains(remesh, "不保留贴图") || !strings.Contains(remesh, "尺寸") {
		t.Fatalf("remesh description missing from list_offerings: %q", remesh)
	}
	if !strings.Contains(seen["tripo/geometry/decimate-triangle"], "带烘焙的减面") {
		t.Fatalf("decimation description missing from list_offerings: %q", seen["tripo/geometry/decimate-triangle"])
	}
	for _, tool := range product.Application().Chat.AgentTools {
		if tool.Name == "list_offerings" {
			if !strings.Contains(tool.Description, "description") {
				t.Fatal("the list_offerings tool does not tell the Agent to read the description")
			}
			return
		}
	}
	t.Fatal("list_offerings tool missing")
}

// #382: list_offerings without an argument lists ids only, and one capability
// or one model narrows the detail to it: the detail of every Offering is what
// made this call cost 45-98 KB per round.
func TestAgentOfferingProjectionIsIdsOnlyByNameAndNarrowsToCapabilityOrModel(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	applications := product.Application()
	type capabilityView struct {
		Capability string                       `json:"capability"`
		Offerings  []map[string]json.RawMessage `json:"offerings"`
	}
	parse := func(raw json.RawMessage) []capabilityView {
		var projection struct {
			Capabilities []capabilityView `json:"capabilities"`
		}
		if err := json.Unmarshal(raw, &projection); err != nil {
			t.Fatal(err)
		}
		return projection.Capabilities
	}
	idsOnly := func(t *testing.T, offering map[string]json.RawMessage) string {
		t.Helper()
		var id string
		if err := json.Unmarshal(offering["model_id"], &id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	slim := parse(buildAgentOfferingsProjection(applications.Capabilities, product, nil, "", "", false))
	if len(slim) == 0 {
		t.Fatal("the ids-only projection lists no capability")
	}
	for _, capability := range slim {
		for _, offering := range capability.Offerings {
			for _, field := range []string{"offering_id", "model_id"} {
				if _, present := offering[field]; !present {
					t.Fatalf("%s without %s: %v", capability.Capability, field, offering)
				}
			}
			for _, heavy := range []string{"description", "knowledge", "inputs", "default_quote"} {
				if _, present := offering[heavy]; present {
					t.Fatalf("%s carries %s in the ids-only view", capability.Capability, heavy)
				}
			}
		}
	}
	detail := parse(buildAgentOfferingsProjection(applications.Capabilities, product, nil, "", "", true))
	if len(detail) == 0 {
		t.Fatal("the detailed projection lists no capability")
	}
	// One capability, in full. remesh is the capability whose offerings declare
	// a description, so the detail view must carry each one exactly as the
	// Schema declares it and omit it where the Schema declares none (#130).
	// text-to-3d declares no description on any of its offerings, so it cannot
	// stand for "the detail is complete"; #382 picked it by mistake.
	one := parse(buildAgentOfferingsProjection(applications.Capabilities, product, nil, "remesh", "", true))
	if len(one) != 1 || one[0].Capability != "remesh" || len(one[0].Offerings) == 0 {
		t.Fatalf("capability filter = %+v", one)
	}
	for _, offering := range one[0].Offerings {
		var id string
		if err := json.Unmarshal(offering["offering_id"], &id); err != nil {
			t.Fatal(err)
		}
		declared, ok := product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("projected offering %s is not in the catalog", id)
		}
		field, present := offering["description"]
		if declared.Description == "" {
			if present {
				t.Fatalf("%s projects a description it does not declare: %s", id, field)
			}
			continue
		}
		var description string
		if err := json.Unmarshal(field, &description); err != nil || description != declared.Description {
			t.Fatalf("%s description = %s, want the Schema text %q", id, field, declared.Description)
		}
	}
	// One model, across capabilities, in full and only of that model.
	chosen := ""
	for _, capability := range detail {
		for _, offering := range capability.Offerings {
			if model := idsOnly(t, offering); model != "" {
				chosen = model
				break
			}
		}
		if chosen != "" {
			break
		}
	}
	if chosen == "" {
		t.Fatal("no model_id to filter on")
	}
	filtered := parse(buildAgentOfferingsProjection(applications.Capabilities, product, nil, "", chosen, true))
	if len(filtered) == 0 {
		t.Fatalf("model filter %q kept nothing", chosen)
	}
	for _, capability := range filtered {
		for _, offering := range capability.Offerings {
			if model := idsOnly(t, offering); !strings.EqualFold(model, chosen) {
				t.Fatalf("model filter %q kept %q: %+v", chosen, model, capability)
			}
		}
	}
}
