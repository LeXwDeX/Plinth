package generationtool

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #243: an order-only multiview Offering reads the pictures by position, so a
// group may only be a contiguous front, left, back, right prefix; front+back
// would send the back picture as left.
func TestOrderOnlyMultiviewAcceptsOnlyAContiguousPrefix(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	executor := &Executor{chat: application.Chat}
	meshy, found := product.Get("meshy/meshy-7.1-multiview/image-to-3d")
	if !found {
		t.Fatal("meshy multiview Offering missing")
	}
	accepted, ordered, declared := multiviewViewsOf(meshy, application.Chat.ThreeView.Multiview)
	if !declared || !ordered || len(accepted) < 3 {
		t.Fatalf("meshy views = %v ordered=%v declared=%v", accepted, ordered, declared)
	}
	for _, views := range [][]string{{"front", "left"}, {"front", "left", "back"}} {
		pictures := multiviewPictures{Views: append([]string(nil), views...)}
		if refusal := executor.fitMultiviewViews(chat.ToolCommand{}, meshy, &pictures, true); refusal != "" {
			t.Fatalf("prefix %v refused: %q", views, refusal)
		}
	}
	for _, views := range [][]string{{"front"}, {"left", "front"}, {"front", "back"}, {"front", "left", "right"}} {
		pictures := multiviewPictures{Views: append([]string(nil), views...)}
		if refusal := executor.fitMultiviewViews(chat.ToolCommand{}, meshy, &pictures, true); refusal == "" {
			t.Fatalf("non-contiguous views %v accepted", views)
		}
	}
}

// The labelled Tripo P2 Offering keeps its reference-views behavior: the
// pictures are filtered by their labels, not by position.
func TestReferenceViewsMultiviewOfferingKeepsLabelledBehavior(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	executor := &Executor{chat: application.Chat}
	tripo, found := product.Get("tripo/tripo-p2-multiview/image-to-3d")
	if !found {
		t.Fatal("tripo p2 multiview Offering missing")
	}
	accepted, ordered, declared := multiviewViewsOf(tripo, application.Chat.ThreeView.Multiview)
	if !declared || ordered || len(accepted) == 0 {
		t.Fatalf("tripo views = %v ordered=%v declared=%v", accepted, ordered, declared)
	}
	pictures := multiviewPictures{Views: []string{"front", "back"}, URLs: []string{"front-url", "back-url"}}
	if refusal := executor.fitMultiviewViews(chat.ToolCommand{}, tripo, &pictures, true); refusal != "" {
		t.Fatalf("labelled front+back refused: %q", refusal)
	}
	if len(pictures.Views) != 2 || pictures.Views[0] != "front" || pictures.Views[1] != "back" {
		t.Fatalf("labelled views changed: %v", pictures.Views)
	}
}

// A plain multi-reference Offering (several pictures, no view keys) is not a
// multiview step and is never selected or accepted.
func TestPlainMultiReferenceOfferingIsNotMultiview(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var plain generation.Offering
	for _, candidate := range product.List("") {
		if candidate.ID == "hyper3d/rodin-gen-2.5/image-to-3d" {
			plain = candidate
		}
	}
	if plain.ID == "" {
		t.Fatal("rodin image-to-3d Offering missing")
	}
	if _, _, declared := multiviewViewsOf(plain, application.Chat.ThreeView.Multiview); declared {
		t.Fatal("a plain multi-reference Offering declared a multiview contract")
	}
	if multiviewAccepts(plain, []string{"front", "left"}, true, application.Chat.ThreeView.Multiview) {
		t.Fatal("a plain multi-reference Offering accepted a multiview input")
	}
}
