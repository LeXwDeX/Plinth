package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/mock"
)

// #329 follow-up A: a direct preparation (the task API, no Agent) that gives a
// single-picture model more pictures than it takes keeps the first ones, as the
// Agent does, and says so in the Schema's words instead of dropping them
// silently. Other callers (retries of a frozen snapshot, plans, the Agent's own
// preparation) keep the strict refusal.
func TestDirectPreparationKeepsTheFirstPicturesAndSaysSo(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, mock.New(), nil)
	base, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	now := h.clock.Now()
	if _, err := h.repository.CreateSession(ctx, chat.Session{ID: "session-a", OwnerID: "owner-a", OwnerName: "Owner", Title: "limit", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, body := range []string{"image-a", "image-b"} {
		asset, err := h.inputs.Upload(ctx, "owner-a", bytes.NewReader([]byte(body)), "image/png", 1<<20)
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, string(asset.ID))
	}
	module := preparedModuleWith(t, h.repository, base, h.inputs)
	input, _ := json.Marshal(map[string]any{"reference_assets": ids})
	command := generation.PrepareInputCommand{OwnerID: "owner-a", SessionID: "session-a", Capability: generation.ImageTo3D,
		OfferingID: "meshy/meshy-7.1/image-to-3d", Input: input}

	if _, err := module.PrepareInput(ctx, command); !generation.IsCode(err, generation.ErrInvalidInput) {
		t.Fatalf("strict preparation of two pictures = %v, want invalid_input", err)
	}

	command.KeepFirstReferences = true
	prepared, err := module.PrepareInput(ctx, command)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared.Assets) != 1 || string(prepared.Assets[0].ID) != ids[0] {
		t.Fatalf("frozen pictures = %#v, want only %s", prepared.Assets, ids[0])
	}
	var frozen map[string]any
	if err := json.Unmarshal(prepared.NormalizedInput, &frozen); err != nil {
		t.Fatal(err)
	}
	if got, _ := frozen["reference_assets"].([]any); len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("frozen input = %v", frozen)
	}
	offering, _ := base.Get("meshy/meshy-7.1/image-to-3d")
	// Meshy 7.1 takes one picture: the one-picture text (#329 note 99647).
	template := offering.ExecutionMessages.ReferenceLimitOne
	if !strings.Contains(template, "{model}") || template == offering.ExecutionMessages.ReferenceLimit {
		t.Fatalf("no reference limit text declared: %q", template)
	}
	want := strings.NewReplacer("{model}", "Meshy 7.1", "{count}", "1", "{total}", "2").Replace(template)
	if prepared.ReferenceNote != want {
		t.Fatalf("note = %q, want %q", prepared.ReferenceNote, want)
	}

	// Within the limit: no note.
	command.Input, _ = json.Marshal(map[string]any{"reference_assets": ids[:1]})
	single, err := module.PrepareInput(ctx, command)
	if err != nil || single.ReferenceNote != "" {
		t.Fatalf("one picture: note %q, %v", single.ReferenceNote, err)
	}
}
