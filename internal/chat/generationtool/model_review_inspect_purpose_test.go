package generationtool_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// TestModelReviewInspectExplicitPurposeAndAllowedFixes is #332 step 7c-1: an
// argument purpose judges this inspection only (purpose_source=argument, no
// session note) and the caller's executable set keeps fix hints to what its own
// tool can run. The platform path passes neither and keeps the 7a behaviour.
func TestModelReviewInspectExplicitPurposeAndAllowedFixes(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	gateway.offerings = map[generation.Capability][]generation.Offering{
		"remesh":    {{ID: "test/remesh", Capability: "remesh", Enabled: true}},
		"uv-unwrap": {{ID: "test/uv-unwrap", Capability: "uv-unwrap", Enabled: true}},
	}
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, nil)
	brief := chat.ProductionBrief{"purpose": []byte(`"render"`)}
	inspected, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-a", TaskID: "task-a", SourceVariantPreference: []string{"glb"}, Brief: brief,
		Purpose:      "game_character",
		AllowedFixes: []generation.Capability{"remesh", "rigging", "format-conversion"},
	})
	if err != nil {
		t.Fatalf("core inspect: %v", err)
	}
	usage := inspected.UsageCheck
	if usage == nil {
		t.Fatal("the argument purpose produced no usage check")
	}
	if usage.Purpose != "game_character" || usage.PurposeSource != "argument" {
		t.Fatalf("usage = %s/%s, want game_character/argument", usage.Purpose, usage.PurposeSource)
	}
	if usage.Note != "" {
		t.Fatalf("an argument purpose wrote a session note: %q", usage.Note)
	}
	coreHint := func(check string) string {
		for _, item := range usage.Checks {
			if item.Check == check {
				return item.FixHint
			}
		}
		t.Fatalf("no %s check", check)
		return ""
	}
	if hint := coreHint("triangles"); !strings.Contains(hint, "remesh") {
		t.Fatalf("triangles hint = %q, want remesh", hint)
	}
	if hint := coreHint("uv"); strings.Contains(hint, "uv-unwrap") {
		t.Fatalf("uv hint names a capability process_model does not declare: %q", hint)
	}

	// The platform path keeps the brief's purpose and every runtime-available
	// capability, even though the same runtime offers uv-unwrap.
	command := reviewCommand("inspect_model", `{"model":"task-a"}`)
	command.Options.ProductionBrief = chat.ProductionBrief{"purpose": []byte(`"game_character"`)}
	platform, err := executor.InspectModel(context.Background(), command)
	if err != nil || platform.Error != "" {
		t.Fatalf("platform inspect = %+v err=%v", platform, err)
	}
	payload := reviewPayload(t, platform)
	platformUsage := payload["usage_check"].(map[string]any)
	if platformUsage["purpose"] != "game_character" || platformUsage["purpose_source"] != "production_brief" {
		t.Fatalf("platform usage = %v", platformUsage)
	}
	if note, _ := platformUsage["note"].(string); note == "" {
		t.Fatal("the platform brief purpose lost its note")
	}
	hints := map[string]string{}
	for _, raw := range platformUsage["checks"].([]any) {
		item := raw.(map[string]any)
		hints[item["check"].(string)], _ = item["fix_hint"].(string)
	}
	if !strings.Contains(hints["uv"], "uv-unwrap") {
		t.Fatalf("platform uv hint = %q, want the available uv-unwrap", hints["uv"])
	}
	if !strings.Contains(hints["triangles"], "remesh") {
		t.Fatalf("platform triangles hint = %q, want remesh", hints["triangles"])
	}
}

// An argument the Schema does not declare is refused before the file is read;
// it is never silently replaced by the brief's purpose.
func TestModelReviewInspectRefusesAnUnknownPurpose(t *testing.T) {
	glb := reviewModelGLB(t, 1, 1)
	gateway := newReviewGateway(reviewTask("task-a", "owner-a", "session-a", glb, generation.TaskSucceeded))
	assets := newReviewAssets(map[generation.AssetID][]byte{"task-a-glb": glb})
	executor, _ := reviewExecutor(t, gateway, assets, nil)
	_, err := executor.InspectReviewedModel(context.Background(), generationtool.ModelInspectRequest{
		Owner: "owner-a", TaskID: "task-a", Purpose: "no_such_purpose",
	})
	var refusal *generationtool.ModelInspectRefusal
	if !errors.As(err, &refusal) || refusal.Kind != generationtool.ModelInspectUnknownPurpose {
		t.Fatalf("inspect = %+v err=%v, want an unknown_purpose refusal", err, err)
	}
	if len(assets.opens) != 0 {
		t.Fatalf("an unknown purpose read the file: %v", assets.opens)
	}
}
