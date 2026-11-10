package threeview

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/schema"
	"github.com/LeXwDeX/Plinth/internal/skillrun"
)

func TestARejectedSourceKeepsTheDependentViewsClosed(t *testing.T) {
	run := skillrun.Run{Steps: []skillrun.Step{
		{Spec: skillrun.StepSpec{Key: "front"}, Phase: skillrun.OutputReady, Review: skillrun.Rejected},
		{Spec: skillrun.StepSpec{Key: "left", SourceStepKey: "front"}},
		{Spec: skillrun.StepSpec{Key: "back", SourceStepKey: "front"}},
	}}
	slots := []Slot{
		{Key: "front", Status: Rejected, Review: string(skillrun.Rejected), Retryable: true},
		{Key: "left", Status: NotStarted, Startable: true},
		{Key: "back", Status: NotStarted, Startable: true},
	}
	applyViewDependencies(run, slots)
	if slots[1].Startable || slots[2].Startable {
		t.Fatalf("a rejected front reopened the dependent views: %+v", slots)
	}
}

func TestAReplacedSourceReopensTheDependentViews(t *testing.T) {
	run := skillrun.Run{Steps: []skillrun.Step{
		{Spec: skillrun.StepSpec{Key: "front"}, Phase: skillrun.OutputReady,
			Review: skillrun.Rejected, ReplacementAssetID: "upload"},
		{Spec: skillrun.StepSpec{Key: "left", SourceStepKey: "front"}},
		{Spec: skillrun.StepSpec{Key: "back", SourceStepKey: "front"}},
	}}
	slots := []Slot{
		{Key: "front", Status: Succeeded, Review: string(skillrun.Rejected), Replaced: true,
			OutputAssetID: "upload"},
		{Key: "left", Status: NotStarted, Startable: true},
		{Key: "back", Status: NotStarted, Startable: true},
	}
	applyViewDependencies(run, slots)
	if !slots[1].Startable || !slots[2].Startable {
		t.Fatalf("a replaced front must reopen the dependent views: %+v", slots)
	}
}

func TestUnstartedViewsArePricedLikeTheOnesThatHaveATask(t *testing.T) {
	group := Group{Slots: []Slot{
		{Key: "front", EstimatedCredits: 6},
		{Key: "side"},
		{Key: "back"},
	}}
	group.priceUnstartedViews()
	for _, slot := range group.Slots {
		if slot.EstimatedCredits != 6 {
			t.Fatalf("slot %s priced %d, want 6", slot.Key, slot.EstimatedCredits)
		}
	}
}

func TestAGroupWithNoTaskYetHasNoPrice(t *testing.T) {
	group := Group{Slots: []Slot{{Key: "front"}, {Key: "side"}}}
	group.priceUnstartedViews()
	for _, slot := range group.Slots {
		if slot.EstimatedCredits != 0 {
			t.Fatalf("slot %s priced %d, want 0 until a view has a task", slot.Key, slot.EstimatedCredits)
		}
	}
}

func TestNativeDefaultEstimatesNeverReplaceFrozenSlots(t *testing.T) {
	q := func(n string) *schema.NativeQuote {
		return &schema.NativeQuote{Version: 1, Unit: "vendor.credit", UnitLabel: "Vendor credits", BaseAmount: n, ExtraAmount: "0", TotalAmount: n}
	}
	g := Group{Slots: []Slot{{TaskID: "first", NativeQuote: q("0.5"), CreditsMetered: true}, {TaskID: "second", NativeQuote: q("1")}, {RequestID: "prepared", NativeQuote: q("2")}, {Key: "unstarted"}, {TaskID: "legacy-free", CreditsMetered: true}}}
	g.priceUnstartedViews()
	if g.Slots[1].NativeQuote.TotalAmount != "1" || g.Slots[2].NativeQuote.TotalAmount != "2" || g.Slots[4].NativeQuote != nil {
		t.Fatalf("frozen slot price replaced: %+v", g.Slots)
	}
	if g.Slots[3].NativeQuote == nil || g.Slots[3].NativeQuote.TotalAmount != "0.5" {
		t.Fatal("unstarted default missing")
	}
}
