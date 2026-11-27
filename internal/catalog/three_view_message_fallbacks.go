package catalog

import (
	"encoding/json"
	"sync"
)

// fillThreeViewMessageFallbacks gives the three-view texts #665 added a value
// when a Catalog does not declare them. The rule for keys added to a released
// Schema is that a Catalog written for an earlier release still loads (see
// dropRetiredFields), and product copy stays in the Schema, never a string in
// Go. A missing text takes, in order:
//
//   - a text the same Catalog declares with the same meaning (a stopped turn,
//     arguments of the wrong shape, asking for one picture again), so its
//     wording and locale copies stay the Catalog's own; or
//   - the same key of the built-in Catalog document (default_catalog.json),
//     which is data shipped with the release and must declare every one of
//     them (TestDefaultCatalogDeclaresThe665ThreeViewTexts). A text with
//     another meaning is never borrowed: a refusal to start is not a queue
//     timeout, and a denied grant is not "the person refused".
//
// It runs before validation, which still requires every one of them; a
// Catalog without the three-view recipe is left alone.
func fillThreeViewMessageFallbacks(application *ApplicationSchema) {
	if application == nil {
		return
	}
	chat := &application.Chat
	if chat.ThreeView.RecipeID == "" {
		return
	}
	messages := &chat.ThreeView.Messages
	presentation := chat.Presentation.Messages
	fallback := func(target *string, sources ...string) {
		if *target != "" {
			return
		}
		for _, source := range sources {
			if source != "" {
				*target = source
				return
			}
		}
	}
	builtIn := builtInThreeViewMessages()
	// Nothing submitted because the turn stopped.
	fallback(&messages.TurnStopped, presentation.TurnStopped, builtIn.TurnStopped)
	// The arguments do not have the declared shape.
	fallback(&messages.ArgumentsInvalid, presentation.ToolArgumentsInvalid, builtIn.ArgumentsInvalid)
	// The chosen picture cannot be used: ask for one picture again.
	fallback(&messages.ImageUnavailable, messages.ReferenceRequired, builtIn.ImageUnavailable)
	// No text of the Catalog says these to the Agent; the release's own.
	fallback(&messages.ModelUnavailable, builtIn.ModelUnavailable)
	fallback(&messages.Denied, builtIn.Denied)
	fallback(&messages.DuplicateRun, builtIn.DuplicateRun)
	// A view the person already started, and a redraw bounded by the turn's
	// generation checkpoint (#689, #693).
	fallback(&messages.AgentPausedStarted, builtIn.AgentPausedStarted, messages.AgentPaused)
	fallback(&messages.ConsistencyRedraw, builtIn.ConsistencyRedraw)
}

// builtInThreeViewMessages reads the three-view texts of the built-in Catalog
// document once. It decodes only that object, so it does not depend on the
// whole document loading (LoadDefault itself goes through Load).
var builtInThreeViewMessages = sync.OnceValue(func() ChatThreeViewMessages {
	var document struct {
		Application struct {
			Chat struct {
				ThreeView struct {
					Messages ChatThreeViewMessages `json:"messages"`
				} `json:"three_view"`
			} `json:"chat"`
		} `json:"application"`
	}
	_ = json.Unmarshal(defaultCatalog, &document)
	return document.Application.Chat.ThreeView.Messages
})
