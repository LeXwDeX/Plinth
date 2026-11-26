package catalog

// fillThreeViewMessageFallbacks gives the three-view texts #665 added a value
// when a Catalog does not declare them. The rule for keys added to a released
// Schema is that a Catalog written for an earlier release still loads (see
// dropRetiredFields; multiview_model_unavailable and budget_plan_inactive
// fall back the same way), and product copy stays in the Schema: an empty
// text takes the nearest text that Catalog already declares, never a string
// in Go. Translations are looked up by the default-language text, so the
// borrowed text keeps the locale copies of its source. It runs before
// validation, which still requires every one of them; a Catalog without the
// three-view recipe is left alone.
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
	// Nothing submitted because the turn stopped.
	fallback(&messages.TurnStopped, presentation.TurnStopped)
	// The arguments do not have the declared shape.
	fallback(&messages.ArgumentsInvalid, presentation.ToolArgumentsInvalid)
	// The session's model cannot draw the views: the turn-start refusal of a
	// model that takes no several views, else the generic refusal.
	fallback(&messages.ModelUnavailable, presentation.MultiviewModelUnavailable, presentation.InvalidParameters)
	// The chosen picture cannot be used: ask for one picture again.
	fallback(&messages.ImageUnavailable, messages.ReferenceRequired)
	// The step was not approved: the late-decision text of a refusal.
	fallback(&messages.Denied, chat.PendingApprovals.Messages.Denied)
	// An earlier group of the same picture is still going: nothing was
	// submitted or charged, and the person continues on the card.
	fallback(&messages.DuplicateRun, messages.AdmissionFull)
}
