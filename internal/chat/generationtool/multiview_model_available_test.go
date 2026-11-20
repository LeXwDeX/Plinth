package generationtool_test

import (
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #373: a structured multiview request starts a turn only when its model names
// an enabled, runtime-available Offering that takes several views of one
// object, whether it labels each picture (reference-views) or reads them in
// order (multi-image endpoint). A model that takes one picture or many
// unlabelled ones (Rodin) is not a multiview model.
func TestMultiviewModelAvailableNamesAnAvailableMultiviewOfferingOnly(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	var capabilities []generation.Capability
	for _, tool := range application.Chat.Tools {
		if tool.Name == application.Chat.ThreeView.Multiview.Tool {
			capabilities = tool.Execution.Capabilities
		}
	}
	if len(capabilities) == 0 {
		t.Fatal("the shipped multiview tool declares no capability")
	}
	offerings := product.List("")
	everything := func(generation.OfferingID) bool { return true }
	for _, model := range []string{
		"tripo-v3.1-multiview",                 // labelled, by model ID
		"tripo-p2-multiview",                   // labelled, by model ID
		"meshy-7.1-multiview",                  // read in order, by model ID
		"tripo/tripo-p2-multiview/image-to-3d", // the Offering ID
		"tripo/tripo-p2-multiview",             // the provider/model pair
		" tripo-p2-multiview ",                 // the pick as typed
	} {
		if !generationtool.MultiviewModelAvailable(offerings, capabilities, everything, model) {
			t.Errorf("multiview model %q was refused", model)
		}
	}
	for _, model := range []string{
		"", "   ", "missing-model",
		"rodin-gen-2.5",            // several pictures with no views
		"tripo-p2",                 // one picture
		"meshy-7.1",                // one picture
		"tripo-smartsegment-image", // an image-to-3D input that is no multiview model
	} {
		if generationtool.MultiviewModelAvailable(offerings, capabilities, everything, model) {
			t.Errorf("model %q was accepted as a multiview model", model)
		}
	}

	// Runtime availability: an Offering without runtime configuration cannot run.
	missing := func(id generation.OfferingID) bool { return id != "tripo/tripo-p2-multiview/image-to-3d" }
	if generationtool.MultiviewModelAvailable(offerings, capabilities, missing, "tripo-p2-multiview") {
		t.Error("an Offering without runtime configuration was accepted")
	}
	if !generationtool.MultiviewModelAvailable(offerings, capabilities, missing, "tripo-v3.1-multiview") {
		t.Error("an available Offering was refused because another one is not")
	}
	// A nil availability check leaves runtime state out (a Catalog-only view).
	if !generationtool.MultiviewModelAvailable(offerings, capabilities, nil, "tripo-p2-multiview") {
		t.Error("a nil availability check refused an enabled Offering")
	}

	// A disabled Offering is not offered, and a capability the tool does not
	// serve does not count.
	disabled := append([]generation.Offering(nil), offerings...)
	for index := range disabled {
		if disabled[index].ModelID == "tripo-p2-multiview" {
			disabled[index].Enabled = false
		}
	}
	if generationtool.MultiviewModelAvailable(disabled, capabilities, everything, "tripo-p2-multiview") {
		t.Error("a disabled Offering was accepted")
	}
	if generationtool.MultiviewModelAvailable(offerings, []generation.Capability{generation.TextTo3D}, everything, "tripo-p2-multiview") {
		t.Error("an Offering of a capability the multiview tool does not serve was accepted")
	}
}
