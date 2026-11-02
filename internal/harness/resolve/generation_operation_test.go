package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

const (
	multiviewOperation = "multiview-to-3d"
	multiviewOffering  = "tripo/tripo-v3.1-multiview/image-to-3d"
)

func generationSelection(t *testing.T, s Service, offeringID string) harness.Selection {
	t.Helper()
	offering, ok := s.Catalog.Get(generation.OfferingID(offeringID))
	if !ok {
		t.Fatalf("missing %s", offeringID)
	}
	selection := harness.DefaultSelection("owner", "session")
	selection.OperationID = multiviewOperation
	selection.OfferingID = string(offering.ID)
	selection.OfferingRevision = offering.Revision
	selection.EnabledOperations = []string{multiviewOperation}
	selection.Sources = []harness.Source{}
	return selection
}

// #27: a generation operation has no model source. Its pictures come from the
// conversation, so it is ready without a source and its tool asks for 2–4
// image handles.
func TestGenerationOperationTakesItsPicturesFromTheConversation(t *testing.T) {
	s, store, g := fixture(t)
	if _, err := s.Update(context.Background(), "owner", "session", 0, generationSelection(t, s, multiviewOffering)); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.Snapshot(context.Background(), "owner", "session")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != StatusReady || len(snapshot.MissingParameters) != 0 || len(snapshot.SourceFingerprints) != 0 {
		t.Fatalf("status=%s missing=%v fingerprints=%v reason=%q", snapshot.Status, snapshot.MissingParameters, snapshot.SourceFingerprints, snapshot.Reason)
	}
	if snapshot.ToolName != "create_model_3d_from_views" || snapshot.Capability != generation.ImageTo3D {
		t.Fatalf("tool=%s capability=%s", snapshot.ToolName, snapshot.Capability)
	}
	if !slices.Equal(snapshot.ConversationParameters, []string{"reference_assets"}) {
		t.Fatalf("conversation parameters = %v", snapshot.ConversationParameters)
	}
	var contract struct {
		Properties struct {
			Parameters struct {
				Properties map[string]struct {
					Type     string `json:"type"`
					MinItems int    `json:"minItems"`
					MaxItems int    `json:"maxItems"`
				} `json:"properties"`
				Required []string `json:"required"`
			} `json:"parameters"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(snapshot.ToolParameters, &contract); err != nil {
		t.Fatal(err)
	}
	field := contract.Properties.Parameters.Properties["reference_assets"]
	if field.Type != "array" || field.MinItems != 2 || field.MaxItems != 4 || !slices.Contains(contract.Properties.Parameters.Required, "reference_assets") {
		t.Fatalf("reference images contract %s", snapshot.ToolParameters)
	}
	if store.writes != 1 || g.paid != 0 {
		t.Fatal("readiness created work")
	}
}

// The operation names its service; another image-to-3D service, or a model
// source the service does not take, is refused when saving.
func TestGenerationOperationKeepsItsServiceAndTakesNoSource(t *testing.T) {
	s, _, _ := fixture(t)
	other := generationSelection(t, s, "tripo/tripo-v3.1-full/image-to-3d")
	if _, err := s.Update(context.Background(), "owner", "session", 0, other); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("other image-to-3D service accepted: %v", err)
	}
	withSource := generationSelection(t, s, multiviewOffering)
	withSource.Sources = []harness.Source{{Kind: harness.SourceTaskAsset, SelectionMode: "agent", Parameter: "*"}}
	if _, err := s.Update(context.Background(), "owner", "session", 0, withSource); err == nil {
		t.Fatal("model source accepted for a service without one")
	}
}
