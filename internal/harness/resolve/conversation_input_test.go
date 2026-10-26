package resolve

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

func referenceImageSelection(t *testing.T, s Service, offeringID, operation string) harness.Selection {
	t.Helper()
	offering, ok := s.Catalog.Get(generation.OfferingID(offeringID))
	if !ok {
		t.Fatalf("missing %s", offeringID)
	}
	selection := harness.DefaultSelection("owner", "session")
	selection.OperationID = operation
	selection.OfferingID = string(offering.ID)
	selection.OfferingRevision = offering.Revision
	selection.EnabledOperations = []string{operation}
	selection.Sources = []harness.Source{{Parameter: "source_asset", TaskID: "model-task", AssetID: "model-asset"}}
	selection.FillableParameters = []string{"prompt"}
	return selection
}

// #187 D: a required reference image has no place in the saved settings; it
// comes from the conversation, so it neither blocks the operation nor hides
// its tool. The tool contract asks for an image handle of this chat.
func TestRequiredReferenceImageComesFromTheConversation(t *testing.T) {
	for _, tc := range []struct{ offering, operation string }{
		{"hyper3d/texture/local-model", "retexture"},
		{"hyper3d/bang/local-model", "mesh-segmentation"},
	} {
		t.Run(tc.offering, func(t *testing.T) {
			s, store, g := fixture(t)
			if _, err := s.Update(context.Background(), "owner", "session", 0, referenceImageSelection(t, s, tc.offering, tc.operation)); err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.Snapshot(context.Background(), "owner", "session")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Status != StatusReady || slices.Contains(snapshot.MissingParameters, "reference_image") {
				t.Fatalf("reference image blocked the operation: status=%s missing=%v reason=%q", snapshot.Status, snapshot.MissingParameters, snapshot.Reason)
			}
			if !slices.Equal(snapshot.ConversationParameters, []string{"reference_image"}) {
				t.Fatalf("conversation parameters = %v", snapshot.ConversationParameters)
			}
			var contract struct {
				Properties struct {
					Parameters struct {
						Properties map[string]struct {
							Type        string `json:"type"`
							Pattern     string `json:"pattern"`
							Description string `json:"description"`
						} `json:"properties"`
						Required []string `json:"required"`
					} `json:"parameters"`
				} `json:"properties"`
			}
			if err := json.Unmarshal(snapshot.ToolParameters, &contract); err != nil {
				t.Fatal(err)
			}
			field, ok := contract.Properties.Parameters.Properties["reference_image"]
			if !ok || field.Type != "string" || field.Pattern == "" || field.Description == "" {
				t.Fatalf("reference image field = %+v (%v) in %s", field, ok, snapshot.ToolParameters)
			}
			if !slices.Contains(contract.Properties.Parameters.Required, "reference_image") {
				t.Fatalf("required reference image is optional in the tool: %s", snapshot.ToolParameters)
			}
			if store.writes != 1 || g.paid != 0 {
				t.Fatal("readiness created work")
			}
		})
	}
}

// An optional reference image (Meshy's style image) is offered the same way
// and stays optional.
func TestOptionalReferenceImageIsOfferedButNotRequired(t *testing.T) {
	s, _, _ := fixture(t)
	selection := choose(t, s, "retexture")
	if selection.OfferingID != "meshy/processing/retexture" {
		t.Skipf("first retexture offering is %s", selection.OfferingID)
	}
	selection.Sources = modelSource()
	selection.FillableParameters = []string{"prompt"}
	if _, err := s.Update(context.Background(), "owner", "session", 0, selection); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil || snapshot.Status != StatusReady || !slices.Equal(snapshot.ConversationParameters, []string{"style_asset"}) {
		t.Fatalf("style image %+v %v", snapshot.ConversationParameters, err)
	}
	var contract struct {
		Properties struct {
			Parameters struct {
				Properties map[string]json.RawMessage `json:"properties"`
				Required   []string                   `json:"required"`
			} `json:"parameters"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(snapshot.ToolParameters, &contract); err != nil {
		t.Fatal(err)
	}
	if _, ok := contract.Properties.Parameters.Properties["style_asset"]; !ok || slices.Contains(contract.Properties.Parameters.Required, "style_asset") {
		t.Fatalf("style image contract %s", snapshot.ToolParameters)
	}
}
