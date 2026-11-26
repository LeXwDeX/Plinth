package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #328, #638: the shipped Schema names the six turn end reasons, with a distinct
// non-empty string each, and publishes them with the chat state strings.
func TestShippedCatalogDeclaresDistinctTurnEndReasons(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	reasons := app.Chat.State.TurnEndReasons
	if !reasons.Declared() {
		t.Fatal("the shipped Schema declares no turn end reasons")
	}
	seen := map[string]bool{}
	for name, value := range map[string]string{
		"completed": reasons.Completed, "awaiting_approval": reasons.AwaitingApproval,
		"stopped": reasons.Stopped, "background": reasons.Background, "error": reasons.Error,
		"empty_response": reasons.EmptyResponse,
	} {
		if strings.TrimSpace(value) == "" || seen[value] {
			t.Fatalf("turn end reason %s = %q is empty or repeated", name, value)
		}
		seen[value] = true
	}
	var decoded struct {
		Chat struct {
			State struct {
				TurnEndReasons map[string]string `json:"turn_end_reasons"`
			} `json:"state"`
		} `json:"chat"`
	}
	encoded, err := json.Marshal(app.Public())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	published := decoded.Chat.State.TurnEndReasons
	if len(published) != 6 || published["empty_response"] != reasons.EmptyResponse || published["completed"] != reasons.Completed ||
		published["awaiting_approval"] != reasons.AwaitingApproval || published["stopped"] != reasons.Stopped ||
		published["background"] != reasons.Background || published["error"] != reasons.Error {
		t.Fatalf("published turn end reasons = %v", published)
	}
}

// The object is optional: a Catalog without it stays valid, records no reason
// and publishes no member.
func TestTurnEndReasonsAreOptional(t *testing.T) {
	c, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := c.Application()
	app.Chat.State.TurnEndReasons = catalog.ChatTurnEndReasonsSchema{}
	if err := catalog.ValidateApplicationSchema(app); err != nil {
		t.Fatalf("a Catalog without turn end reasons is refused: %v", err)
	}
	encoded, err := json.Marshal(app.Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "turn_end_reasons") {
		t.Fatal("an undeclared turn_end_reasons is published")
	}
	// The shipped Schema is not changed by editing the copy.
	if !c.Application().Chat.State.TurnEndReasons.Declared() {
		t.Fatal("editing the Application copy changed the loaded Catalog")
	}
}

// Once declared, every reason needs a value and no two share one.
func TestTurnEndReasonsAreNeverPartialOrRepeated(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*catalog.ChatTurnEndReasonsSchema)
		want   string
	}{
		"completed empty":         {func(r *catalog.ChatTurnEndReasonsSchema) { r.Completed = "" }, "turn_end_reasons.completed"},
		"awaiting_approval empty": {func(r *catalog.ChatTurnEndReasonsSchema) { r.AwaitingApproval = "" }, "turn_end_reasons.awaiting_approval"},
		"stopped empty":           {func(r *catalog.ChatTurnEndReasonsSchema) { r.Stopped = "" }, "turn_end_reasons.stopped"},
		"background empty":        {func(r *catalog.ChatTurnEndReasonsSchema) { r.Background = "" }, "turn_end_reasons.background"},
		"error empty":             {func(r *catalog.ChatTurnEndReasonsSchema) { r.Error = "" }, "turn_end_reasons.error"},
		"empty_response empty":    {func(r *catalog.ChatTurnEndReasonsSchema) { r.EmptyResponse = "" }, "turn_end_reasons.empty_response"},
		"completed repeated":      {func(r *catalog.ChatTurnEndReasonsSchema) { r.Error = r.Completed }, "重复"},
		"stopped repeated":        {func(r *catalog.ChatTurnEndReasonsSchema) { r.Background = r.Stopped }, "重复"},
	} {
		t.Run(name, func(t *testing.T) {
			c, err := catalog.LoadDefault()
			if err != nil {
				t.Fatal(err)
			}
			app := c.Application()
			test.mutate(&app.Chat.State.TurnEndReasons)
			err = catalog.ValidateApplicationSchema(app)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateApplicationSchema err = %v, want %q", err, test.want)
			}
			if own := app.Chat.State.TurnEndReasons.Validate(); own == nil {
				t.Fatal("the schema's own Validate accepts it")
			}
		})
	}
}
