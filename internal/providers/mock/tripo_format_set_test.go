package mock

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #278, revised by #462: a Tripo task whose purpose chose the GLB and FBX only
// succeeds on the GLB alone — the other five formats are on-demand slots, which
// never block success — and delivers the selected ones in the post-success
// round, the way the upstream converts them after the main model is ready. The
// kernel removes the slots the request never asked for before the Task is
// frozen, so the mock only ever sees the selected ones.
func TestMockDeliversOnlyTheSelectedTripoFormats(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const id = "tripo/tripo-v3.1-full/text-to-3d"
	offering, ok := loaded.Get(generation.OfferingID(id))
	if !ok {
		t.Fatalf("%s missing", id)
	}
	for name, formats := range map[string][]string{
		"game":    {"GLTF", "FBX"},
		"print":   {"GLTF", "STL", "3MF"},
		"display": {"GLTF", "USDZ", "FBX", "OBJ", "STL", "3MF"},
	} {
		t.Run(name, func(t *testing.T) {
			_, frozen, err := loaded.Normalize(generation.TextTo3D, id, json.RawMessage(`{"prompt":"a crate","authorize_extra_credits":true,"output_formats":`+mustJSON(t, formats)+`}`))
			if err != nil {
				t.Fatal(err)
			}
			// The kernel resolves the conditional outputs once, at freeze time.
			pinned, err := generation.ResolveConditionalOutputs(offering, frozen)
			if err != nil {
				t.Fatal(err)
			}
			adapter := New()
			files := complete(t, adapter, pinned, "formats-"+name, json.RawMessage(frozen))
			delivered := convertedVariants(files)
			if want := []string{"converted.gltf"}; !slices.Equal(delivered, want) {
				t.Fatalf("success round delivered %v, want only the blocking slot(s) %v", delivered, want)
			}
			// The five on-demand slots the request selected are still pending, so
			// the post-success round converts them.
			pending := pendingSlotsForTest(pinned, formats)
			if len(pending) != len(formats)-1 {
				t.Fatalf("pending on-demand slots = %d, want %d", len(pending), len(formats)-1)
			}
			job := generation.UpstreamJob{ProviderID: pinned.ProviderID, RemoteID: adapter.jobs[adapter.submissions["formats-"+name]].remoteID}
			result, err := adapter.Observe(context.Background(), generation.ObserveRequest{
				TaskID: generation.TaskID("task-formats-" + name), Offering: pinned, Job: job,
				PostSuccess: true, Input: json.RawMessage(frozen), ResourceSlots: pending,
			})
			if err != nil {
				t.Fatal(err)
			}
			post := convertedVariants(convertFiles(result.Artifacts))
			want := make([]string, 0, len(formats)-1)
			for _, format := range formats {
				if format == "GLTF" {
					continue
				}
				want = append(want, "converted."+strings.ToLower(format))
			}
			slices.Sort(post)
			slices.Sort(want)
			if !slices.Equal(post, want) {
				t.Fatalf("post-success delivered %v, want %v", post, want)
			}
		})
	}
}

// The post-success round must honour the slot set the kernel reports. The
// Offering's on-demand set and the reported set are the same list in the happy
// path, so this drives a strict subset: only one of the selected converted
// formats is reported, and the round may deliver nothing else. A mock that
// converted every on-demand slot of the Offering would answer with both.
func TestMockPostSuccessDeliversOnlyTheSlotsItIsGiven(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const id = "tripo/tripo-v3.1-full/text-to-3d"
	offering, ok := loaded.Get(generation.OfferingID(id))
	if !ok {
		t.Fatalf("%s missing", id)
	}
	// GLTF plus two converted formats, so the round has a real choice to make.
	input := `{"prompt":"a crate","authorize_extra_credits":true,"output_formats":["GLTF","STL","3MF"]}`
	_, frozen, err := loaded.Normalize(generation.TextTo3D, id, json.RawMessage(input))
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := generation.ResolveConditionalOutputs(offering, frozen)
	if err != nil {
		t.Fatal(err)
	}
	onDemand := make([]generation.ResourceSlotState, 0, len(pinned.Outputs))
	for _, output := range pinned.Outputs {
		if output.Role != "converted_model" || output.Delivery != generation.AssetDeliveryOnDemand {
			continue
		}
		onDemand = append(onDemand, generation.ResourceSlotState{
			Role: output.Role, Variant: output.Variant, State: generation.ResourcePending,
		})
	}
	if len(onDemand) < 2 {
		t.Fatalf("on-demand converted slots = %d, want at least 2 to choose from", len(onDemand))
	}
	subset := onDemand[:1]
	adapter := New()
	complete(t, adapter, pinned, "formats-subset", json.RawMessage(frozen))
	job := generation.UpstreamJob{ProviderID: pinned.ProviderID, RemoteID: adapter.jobs[adapter.submissions["formats-subset"]].remoteID}
	result, err := adapter.Observe(context.Background(), generation.ObserveRequest{
		TaskID: generation.TaskID("task-formats-subset"), Offering: pinned, Job: job,
		PostSuccess: true, Input: json.RawMessage(frozen), ResourceSlots: subset,
	})
	if err != nil {
		t.Fatal(err)
	}
	if post := convertedVariants(convertFiles(result.Artifacts)); !slices.Equal(post, []string{subset[0].Variant}) {
		t.Fatalf("post-success delivered %v, want only the reported slot %v", post, subset[0].Variant)
	}
}

func convertedVariants(files []deliveredFile) []string {
	variants := make([]string, 0, len(files))
	for _, file := range files {
		if file.claim.Role == "converted_model" {
			variants = append(variants, file.claim.Variant)
		}
	}
	slices.Sort(variants)
	return variants
}

func convertFiles(claims []generation.ArtifactClaim) []deliveredFile {
	files := make([]deliveredFile, 0, len(claims))
	for _, claim := range claims {
		if claim.Role == "converted_model" {
			files = append(files, deliveredFile{claim: claim})
		}
	}
	return files
}

func pendingSlotsForTest(offering generation.Offering, formats []string) []generation.ResourceSlotState {
	slots := make([]generation.ResourceSlotState, 0, len(formats))
	for _, output := range offering.Outputs {
		if output.Role != "converted_model" || output.Delivery != generation.AssetDeliveryOnDemand {
			continue
		}
		slots = append(slots, generation.ResourceSlotState{
			Role: output.Role, Variant: output.Variant, State: generation.ResourcePending,
		})
	}
	return slots
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
