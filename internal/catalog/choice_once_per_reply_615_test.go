package catalog_test

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #615: the shipped Catalog words the refusal of a second choice card in one
// reply, in both locales, and names ask_choice so the Agent knows which call
// to stop repeating.
func TestShippedCatalogWordsTheSecondCardRefusal_615(t *testing.T) {
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	app := loaded.Application()
	chinese := app.Chat.Presentation.Messages.ChoiceOpenInReply
	if !strings.Contains(chinese, "ask_choice") || !strings.Contains(chinese, "卡") {
		t.Fatalf("choice_open_in_reply = %q", chinese)
	}
	english := app.Localization.Bundles["en"].Fields["chat.presentation.messages.choice_open_in_reply"]
	if !strings.Contains(english, "ask_choice") || !strings.Contains(strings.ToLower(english), "card") {
		t.Fatalf("english choice_open_in_reply = %q", english)
	}
}
