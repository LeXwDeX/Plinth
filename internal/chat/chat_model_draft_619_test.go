package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// #619: a request outside a turn that names the owner's session (the Skill
// draft) runs on that session's chat model, the same one its next turn would
// use; without a session it runs on the default, and another owner's session
// is not found.
func TestChatModelForFollowsTheSessionChoice(t *testing.T) {
	ctx := context.Background()
	fixture, models := newModelFixture(t)
	if connection, model, err := fixture.service.ChatModelFor(ctx, "owner-a", ""); err != nil || connection != "LLM" || model != "deepseek" {
		t.Fatalf("no session = %q %q %v", connection, model, err)
	}
	if connection, model, err := fixture.service.ChatModelFor(ctx, "owner-a", fixture.session); err != nil || connection != "LLM" || model != "deepseek" {
		t.Fatalf("session on the default = %q %q %v", connection, model, err)
	}
	if _, err := fixture.service.SetSessionChatModel(ctx, "owner-a", fixture.session, "GLM"); err != nil {
		t.Fatal(err)
	}
	if connection, model, err := fixture.service.ChatModelFor(ctx, "owner-a", fixture.session); err != nil || connection != "GLM" || model != "glm-5.3" {
		t.Fatalf("session on GLM = %q %q %v", connection, model, err)
	}
	if _, _, err := fixture.service.ChatModelFor(ctx, "owner-b", fixture.session); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign session = %v", err)
	}
	// The chosen model gone offline: the session's turns fall back to the
	// default, and so does the draft.
	models.replace(chat.ChatModelSet{Options: []chat.ChatModelOption{deepseekOption}, DefaultID: "LLM"})
	if connection, _, err := fixture.service.ChatModelFor(ctx, "owner-a", fixture.session); err != nil || connection != "LLM" {
		t.Fatalf("offline choice = %q %v", connection, err)
	}
}
