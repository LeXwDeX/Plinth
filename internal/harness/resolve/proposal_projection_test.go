package resolve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/harness"
)

// Only this test's in-memory Harness declarations expose a conversion prerequisite.
// It exercises the generic proposal contract without registering a product route.
func conversionProposalFixture(t *testing.T) (Service, *memoryStore, *readGateway, Snapshot) {
	t.Helper()
	s, store, gateway := fixture(t)
	forward, _ := s.Catalog.Get("local/static-glb/to-gltf")
	reverse, _ := s.Catalog.Get("local/static-gltf/to-glb")
	goal, _ := s.operation("format-conversion")
	goal.ModelIDs = []string{reverse.ModelID}
	goal.Prerequisites = [][]string{{"forward"}}
	first := goal
	first.ID = "forward"
	first.ModelIDs = []string{forward.ModelID}
	first.Prerequisites = nil
	s.Schema.Operations = append(s.Schema.Operations, first)
	for i := range s.Schema.Operations {
		if s.Schema.Operations[i].ID == goal.ID {
			s.Schema.Operations[i] = goal
		}
	}
	for i := range s.Tools {
		if s.Tools[i].Name == s.Schema.PrerequisiteTool {
			s.Tools[i].Execution.Capabilities = append(s.Tools[i].Execution.Capabilities, generation.FormatConversion)
		}
	}
	gateway.task.Assets[0].SHA256 = strings.Repeat("a", 64)
	choice := harness.DefaultSelection("owner", "session")
	choice.OperationID, choice.OfferingID, choice.OfferingRevision = goal.ID, string(reverse.ID), reverse.Revision
	choice.EnabledOperations = []string{"forward", goal.ID}
	choice.AllowPrerequisites = true
	choice.Sources = modelSource()
	selected, err := s.Update(context.Background(), "owner", "session", 0, choice)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), selected)
	if err != nil || !snapshot.CanPropose {
		t.Fatalf("proposal fixture unavailable: %+v %v", snapshot, err)
	}
	return s, store, gateway, snapshot
}

func TestProposalPredictsDeclaredAssetsTaskOutputWithoutInventedBytes(t *testing.T) {
	s, store, gateway, snapshot := conversionProposalFixture(t)
	proposal, err := s.Propose(context.Background(), snapshot, []string{"forward"})
	if err != nil {
		t.Fatalf("declared GLB→glTF→GLB chain rejected: %v", err)
	}
	if len(proposal.Steps) != 2 || proposal.Steps[0].OfferingID != "local/static-glb/to-gltf" || proposal.Steps[1].OfferingID != "local/static-gltf/to-glb" || proposal.Steps[1].SourceStep != 0 || proposal.Executed || !proposal.NeedsPreparation {
		t.Fatalf("invalid prediction: %+v", proposal)
	}
	if gateway.paid != 0 || store.writes != 1 {
		t.Fatal("prediction executed or persisted work")
	}
}

func TestProposalRejectsIncompatibleProjectedOutputAndInvalidRealFirstSource(t *testing.T) {
	for _, reason := range []string{"variant", "media", "budget", "real-source"} {
		t.Run(reason, func(t *testing.T) {
			s, store, gateway, snapshot := conversionProposalFixture(t)
			if reason == "real-source" {
				gateway.task.Assets[0].Size = 10*1024*1024 + 1
				snapshot.SourceFingerprints[0].Size = gateway.task.Assets[0].Size
			} else {
				// Catalog.Get returns an isolated value. Change the test Catalog so
				// Propose sees the altered declaration through its normal lookup.
				raw, err := os.ReadFile("../../catalog/default_catalog.json")
				if err != nil {
					t.Fatal(err)
				}
				var doc map[string]any
				if err := json.Unmarshal(raw, &doc); err != nil {
					t.Fatal(err)
				}
				var output map[string]any
				for _, item := range doc["offerings"].([]any) {
					offering := item.(map[string]any)
					if offering["id"] == "local/static-glb/to-gltf" {
						output = offering["outputs"].([]any)[0].(map[string]any)
						break
					}
				}
				if output == nil {
					t.Fatal("forward offering output missing")
				}
				switch reason {
				case "variant":
					output["variant"] = "fbx"
				case "media":
					output["media_types"] = []any{"model/gltf-binary"}
				case "budget":
					output["max_bytes"] = output["max_bytes"].(float64) + 1
				}
				raw, err = json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				s.Catalog, err = catalog.Load(bytes.NewReader(raw))
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Propose(context.Background(), snapshot, []string{"forward"}); !errors.Is(err, harness.ErrInvalid) {
				t.Fatalf("%s accepted or wrong rejection: %v", reason, err)
			}
			if gateway.paid != 0 || store.writes != 1 {
				t.Fatal("rejected prediction executed or persisted work")
			}
		})
	}
	t.Run("optional", func(t *testing.T) {
		s, _, _, _ := conversionProposalFixture(t)
		forward, _ := s.Catalog.Get("local/static-glb/to-gltf")
		reverse, _ := s.Catalog.Get("local/static-gltf/to-glb")
		forward.Outputs[0].Delivery = "optional"
		if _, err := generation.BindProjectedModelSource(reverse, forward); err == nil {
			t.Fatal("optional projected model output accepted")
		}
	})
}

// This in-memory fixture offers two first steps in Catalog order. The first
// accepts the real source but fails its output or normalization contract; only
// the second can form an admissible complete chain.
func TestProposalUsesTheSameCompleteCandidateChainAsSnapshot(t *testing.T) {
	for _, reason := range []string{"output", "normalization"} {
		t.Run(reason, func(t *testing.T) {
			s, store, gateway, snapshot := conversionProposalFixture(t)
			raw, err := os.ReadFile("../../catalog/default_catalog.json")
			if err != nil {
				t.Fatal(err)
			}
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			offerings := map[string]map[string]any{}
			for _, item := range doc["offerings"].([]any) {
				o := item.(map[string]any)
				offerings[o["id"].(string)] = o
			}
			first := offerings["local/static-glb/to-gltf"]["outputs"].([]any)[0].(map[string]any)
			goodOutput := map[string]any{}
			for key, value := range first {
				goodOutput[key] = value
			}
			if reason == "output" {
				first["variant"], first["media_types"] = "glb", []any{"model/gltf-binary"}
			} else {
				// A valid source contract is insufficient when its frozen full-format
				// cost requires consent absent from a read-only prerequisite proposal.
				native := offerings["tripo/processing/rigging"]
				candidate := offerings["local/static-glb/to-gltf"]
				candidate["cost"], candidate["cost_policy"] = native["cost"], native["cost_policy"]
				for _, item := range native["parameters"].([]any) {
					parameter := item.(map[string]any)
					if parameter["name"] == "output_formats" || parameter["name"] == "authorize_extra_credits" {
						candidate["parameters"] = append(candidate["parameters"].([]any), parameter)
					}
				}
			}
			other := offerings["tripo/format-conversion/uploaded-glb-to-stl"]
			other["outputs"].([]any)[0] = goodOutput
			other["parameters"].([]any)[0].(map[string]any)["uploaded_model"].(map[string]any)["native_only"] = false
			offerings["local/static-gltf/to-glb"]["parameters"].([]any)[0].(map[string]any)["task_offerings"] = []any{"local/static-glb/to-gltf", "tripo/format-conversion/uploaded-glb-to-stl"}
			raw, err = json.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			s.Catalog, err = catalog.Load(bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			forward, _ := s.Catalog.Get("local/static-glb/to-gltf")
			alternate, _ := s.Catalog.Get("tripo/format-conversion/uploaded-glb-to-stl")
			for i := range s.Schema.Operations {
				if s.Schema.Operations[i].ID == "forward" {
					s.Schema.Operations[i].ModelIDs = []string{forward.ModelID, alternate.ModelID}
				}
			}
			if _, err := generation.BindModelSource(forward, gateway.task, "model-asset", nil); err != nil {
				t.Fatal(err)
			}
			goal, _ := s.Catalog.Get("local/static-gltf/to-glb")
			_, projectedErr := generation.BindProjectedModelSource(goal, forward)
			if (projectedErr != nil) != (reason == "output") {
				t.Fatalf("fixture projection error=%v reason=%s", projectedErr, reason)
			}
			if reason == "normalization" {
				if _, _, err := s.Catalog.Normalize(forward.Capability, forward.ID, []byte(`{"source_asset":"model-asset"}`)); err == nil {
					t.Fatal("fixture's first complete candidate must fail normalization")
				}
				if _, _, err := s.Catalog.Normalize(alternate.Capability, alternate.ID, []byte(`{"source_asset":"model-asset"}`)); err != nil {
					t.Fatalf("alternate must normalize: %v", err)
				}
			}
			snapshot, err = s.ResolveSelection(context.Background(), snapshot.Selection)
			if err != nil || !snapshot.CanPropose {
				t.Fatalf("valid alternate not offered: %+v %v", snapshot, err)
			}
			proposal, err := s.Propose(context.Background(), snapshot, []string{"forward"})
			if err != nil || len(proposal.Steps) != 2 || proposal.Steps[0].OfferingID != string(alternate.ID) || proposal.Steps[1].OfferingID != string(goal.ID) {
				t.Fatalf("proposal did not use complete compatible chain: %+v %v", proposal, err)
			}
			if gateway.paid != 0 || store.writes != 1 {
				t.Fatal("chain search executed or changed selection")
			}
		})
	}
}
