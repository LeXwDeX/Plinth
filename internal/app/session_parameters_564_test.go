package app

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// #564: the Agent's session parameter block lists what the next call really
// runs with: the person's panel values and the Agent's own, by label and
// option label, and nothing the image count block or the Offering's fixed
// values already decide.
func TestSessionParameterGroupsListWhatTheNextCallRunsWith(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	store := agentparams.NewStore()
	scope := agentparams.ScopeKey("owner", "session")
	const gpt = "openai/gpt-image-2/text-to-image"
	if err := store.SetUserOffering(scope, gpt, map[string]json.RawMessage{
		"size": json.RawMessage(`"1536x1024"`), "n": json.RawMessage(`3`),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAgentOffering(scope, gpt, map[string]json.RawMessage{
		"background": json.RawMessage(`"transparent"`),
	}, map[string]string{"background": "图标要透明底"}); err != nil {
		t.Fatal(err)
	}
	read := sessionParameterGroups(store, loaded.Get)
	groups := read("owner", "session")
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want one for %s", groups, gpt)
	}
	values := map[string]string{}
	sources := map[string]string{}
	for _, parameter := range groups[0].Parameters {
		values[parameter.Label] = parameter.Value
		sources[parameter.Label] = parameter.Source
	}
	offering, _ := loaded.Get(generation.OfferingID(gpt))
	labels := map[string]string{}
	for _, parameter := range offering.Parameters {
		labels[parameter.Name] = parameter.Label
	}
	if values[labels["size"]] != "1536x1024" || sources[labels["size"]] != "user" {
		t.Errorf("the person's size is missing or misattributed: %+v", groups[0].Parameters)
	}
	if values[labels["background"]] == "" || sources[labels["background"]] != "agent" {
		t.Errorf("the Agent's background is missing or misattributed: %+v", groups[0].Parameters)
	}
	if _, listed := values[labels["n"]]; listed {
		t.Errorf("the count is listed here as well as in the image count block: %+v", groups[0].Parameters)
	}
	if groups := read("owner", "other"); len(groups) != 0 {
		t.Errorf("another session reads this session's values: %+v", groups)
	}
}
