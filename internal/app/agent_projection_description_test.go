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
	raw := buildAgentOfferingsProjection(product.Application().Capabilities, product, nil)
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
