package agentparams

import (
	"encoding/json"
	"testing"
)

// #586: the panel's session image count is gone, but a row an older server
// stored under image_count stays private: it never reaches a parameter
// projection or a generation request, and reading it is no error.
func TestLegacySessionImageCountStaysPrivate(t *testing.T) {
	store := NewStore()
	store.parameterOnly = true
	store.scopes["s1"] = map[string]json.RawMessage{
		"image_count": json.RawMessage(`4`),
		"size":        json.RawMessage(`"1024x1024"`),
	}
	store.userOverrides["s1"] = map[string]json.RawMessage{
		"image_count": json.RawMessage(`6`),
	}
	got := store.Get("s1")
	if _, leaked := got["image_count"]; leaked {
		t.Fatalf("legacy image count leaked into parameters: %v", got)
	}
	if string(got["size"]) != `"1024x1024"` {
		t.Fatalf("size = %s", got["size"])
	}
	if !isPrivateAgentStateKey(LegacyImageCountKey) {
		t.Fatal("legacy image count key is not private")
	}
}

// #586: an old panel count pinned for one Offering neither applies nor hides
// the count the Agent sets for it; other pins keep winning as before.
func TestRetiredPanelCountPinYieldsToTheAgentsCount(t *testing.T) {
	store := NewStore()
	const id = "openai/gpt-image-2/text-to-image"
	if err := store.SetUserOffering("s1", id, map[string]json.RawMessage{
		"n": json.RawMessage(`3`), "size": json.RawMessage(`"1536x1024"`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentOffering("s1", id, map[string]json.RawMessage{
		"n": json.RawMessage(`4`), "size": json.RawMessage(`"1024x1024"`),
	}, map[string]string{"n": "四个变体"}); err != nil {
		t.Fatal(err)
	}
	retired := func(name string) bool { return name == "n" }
	states := store.OfferingStatesExcept("s1", id, retired)
	if states["n"].Source != "agent" || string(states["n"].Value) != "4" || states["n"].Reason != "四个变体" {
		t.Fatalf("n = %+v, want the Agent's 4", states["n"])
	}
	if states["size"].Source != "user" || string(states["size"].Value) != `"1536x1024"` {
		t.Fatalf("size = %+v, want the person's pin", states["size"])
	}
	values := store.OfferingOverridesExcept("s1", id, retired)
	if string(values["n"]) != "4" || string(values["size"]) != `"1536x1024"` {
		t.Fatalf("values = %v", values)
	}
	// The stored rows stay: the plain read still sees the old pin.
	if got := store.OfferingStates("s1", id)["n"]; got.Source != "user" || string(got.Value) != "3" {
		t.Fatalf("the stored pin changed: %+v", got)
	}
}
