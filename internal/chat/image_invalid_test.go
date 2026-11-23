package chat_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #497: a picture the model read and refused is reported as a bad picture,
// with what the upstream objected to in the Schema's words, never as a model
// without image input and never with the upstream's own wording. The error
// body is the dev-test refusal verbatim.
func TestOrchestratorSaysWhenTheUpstreamRefusedThePictureItself(t *testing.T) {
	const upstreamWording = "The image length and width do not meet the model restrictions. " +
		"[height:1 or width:1 must be larger than 32]"
	schema := configuredChatSchema(t)
	invalidText := schema.Presentation.Messages.AgentImageInvalid
	if invalidText == "" {
		t.Fatal("the default Schema declares no agent_image_invalid")
	}
	if !strings.Contains(schema.Presentation.Messages.AgentImageInvalidReasons, "{reasons}") {
		t.Fatalf("agent_image_invalid_reasons = %q, want one with {reasons}",
			schema.Presentation.Messages.AgentImageInvalidReasons)
	}
	if schema.Presentation.Messages.AgentImageInvalidReasonLabels["dimensions"] == "" {
		t.Fatalf("agent_image_invalid_reason_labels = %v, want a dimensions label",
			schema.Presentation.Messages.AgentImageInvalidReasonLabels)
	}
	content := runFailingTurn(t, failingAgent{err: errors.Join(
		&agents.ImageInvalidity{Reasons: []string{"dimensions"}},
		&agents.StatusError{Status: 400},
		errors.New(upstreamWording),
	)})
	if !strings.HasPrefix(content, invalidText) ||
		!strings.Contains(content, schema.Presentation.Messages.AgentImageInvalidReasonLabels["dimensions"]) ||
		strings.Contains(content, "upstream") || strings.Contains(content, "height") || strings.Contains(content, "model restrictions") {
		t.Fatalf("assistant = %q, want it to start with %q and name the reason", content, invalidText)
	}
	if !strings.Contains(content, strings.SplitN(schema.Presentation.Messages.ErrorReference, "{id}", 2)[0]) {
		t.Fatalf("assistant = %q lacks the error reference", content)
	}
}

// The new text exists in both default languages and keeps its placeholder, so
// the English reader gets the same sentence with the reason named.
func TestImageInvalidTextIsTranslated(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	texts, err := catalog.NewTextLocalizer(application)
	if err != nil {
		t.Fatal(err)
	}
	messages := application.Chat.Presentation.Messages
	chinese := messages.AgentImageInvalid
	english := texts.Text("en", chinese)
	if english == chinese || strings.ContainsAny(english, "，。「」") {
		t.Fatalf("english = %q", english)
	}
	reasons := texts.Text("en", messages.AgentImageInvalidReasons)
	if reasons == messages.AgentImageInvalidReasons || !strings.Contains(reasons, "{reasons}") {
		t.Fatalf("english reasons = %q", reasons)
	}
	for token, label := range messages.AgentImageInvalidReasonLabels {
		if texts.Text("en", label) == label {
			t.Fatalf("the %q label is not translated: %q", token, label)
		}
	}
}