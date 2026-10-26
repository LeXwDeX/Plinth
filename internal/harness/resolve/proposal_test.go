package resolve

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/harness"
)

func TestPrerequisiteProposalPreservesGoalAndDoesNotExecute(t *testing.T) {
	s, store, g := fixture(t)
	choice := choose(t, s, "animation")
	choice.EnabledOperations = []string{"remesh", "rigging", "animation"}
	choice.AllowPrerequisites = true
	choice.Sources = modelSource()
	choice.FixedParameters = map[string]json.RawMessage{"action_id": json.RawMessage(`1`)}
	selected, err := s.Update(context.Background(), "owner", "session", 0, choice)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), selected)
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.Propose(context.Background(), snapshot, []string{"rigging"})
	if err != nil {
		t.Fatal(err)
	}
	if len(proposal.Steps) != 2 || proposal.Goal.Binding != selected.Binding || proposal.Steps[1].OfferingID != selected.OfferingID || proposal.Steps[1].OfferingRevision != selected.OfferingRevision || proposal.Steps[0].SourceStep != -1 || proposal.Steps[1].SourceStep != 0 || proposal.Executed || !proposal.NeedsPreparation {
		t.Fatalf("proposal identity or relation lost: %+v", proposal)
	}
	if g.paid != 0 || store.writes != 1 {
		t.Fatalf("proposal wrote paid objects: %d/%d", g.paid, store.writes)
	}
	if _, err := s.Propose(context.Background(), snapshot, []string{"retexture"}); !errors.Is(err, harness.ErrInvalid) {
		t.Fatalf("unlisted path accepted: %v", err)
	}
	store.selection.Revision++
	if _, err := s.Propose(context.Background(), snapshot, []string{"rigging"}); !errors.Is(err, harness.ErrStale) {
		t.Fatalf("stale proposal accepted: %v", err)
	}
}

func TestPrerequisiteProposalRetainsMissingRequiredParameters(t *testing.T) {
	s, store, g := fixture(t)
	choice := choose(t, s, "animation")
	choice.EnabledOperations = []string{"rigging", "animation"}
	choice.AllowPrerequisites = true
	choice.Sources = modelSource()
	choice.FillableParameters = []string{"action_id"}
	selected, err := s.Update(context.Background(), "owner", "session", 0, choice)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ResolveSelection(context.Background(), selected)
	if err != nil || !snapshot.CanPropose {
		t.Fatalf("missing input blocked proposal: %+v %v", snapshot, err)
	}
	proposal, err := s.Propose(context.Background(), snapshot, []string{"rigging"})
	if err != nil || len(proposal.Steps) != 2 || !slices.Contains(proposal.Steps[1].MissingParameters, "action_id") {
		t.Fatalf("missing required parameter must remain preparable: %+v %v", proposal, err)
	}
	if g.paid != 0 || store.writes != 1 {
		t.Fatal("missing-input proposal executed or changed selection")
	}
}
