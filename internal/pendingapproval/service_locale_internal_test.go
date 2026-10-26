package pendingapproval

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/generationtool"
)

// emptyTurnChat reports a stored turn without a frozen locale.
type emptyTurnChat struct{}

func (emptyTurnChat) GetTurn(context.Context, string, string, string) (chat.Turn, error) {
	return chat.Turn{}, nil
}
func (emptyTurnChat) OwnsSession(context.Context, string, string) (bool, error) { return true, nil }
func (emptyTurnChat) ListMessages(context.Context, string, string) ([]chat.Message, error) {
	return nil, nil
}
func (emptyTurnChat) TurnStopRequested(context.Context, string, string, string) (bool, error) {
	return false, nil
}
func (emptyTurnChat) PostAssistantMessage(context.Context, string, chat.Message) (chat.Message, bool, error) {
	return chat.Message{}, false, nil
}
func (emptyTurnChat) UpdateMessage(context.Context, string, chat.Message) error { return nil }
func (emptyTurnChat) AttachResults(context.Context, string, *chat.Message, map[string]bool) {
}

// #358: without a wired DefaultLocale, an empty frozen turn locale must not
// inherit the language of the request that swept it. The service falls back
// to the Schema's own copy without hardcoding a locale tag.
func TestInTurnLocaleFallsBackToTheSchemaDefault(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	chinese := product.Application().Chat.PendingApprovals.Messages.Denied
	english := texts.Text("en", chinese)
	if chinese == "" || english == chinese {
		t.Fatalf("need distinct default and English denial text")
	}
	op := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a"}
	requestEnglish := catalog.WithLocale(context.Background(), "en")

	for name, service := range map[string]*Service{
		"default locale not wired": {chat: emptyTurnChat{}, texts: texts},
		"default locale wired":     {chat: emptyTurnChat{}, texts: texts, defaultLocale: "zh-CN"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := service.inTurnLocale(requestEnglish, op)
			if got := service.text(ctx, chinese); got != chinese {
				t.Fatalf("text = %q, want the default %q (English %q)", got, chinese, english)
			}
		})
	}
}

// A failed turn read cannot freeze a locale. Background callers may carry any
// request locale, so the service must force its Schema default instead.
type failedTurnChat struct{ emptyTurnChat }

func (failedTurnChat) GetTurn(context.Context, string, string, string) (chat.Turn, error) {
	return chat.Turn{}, errors.New("turn unavailable")
}

func TestInTurnLocaleForcesDefaultWhenTurnReadFails(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	chinese := product.Application().Chat.PendingApprovals.Messages.Denied
	english := texts.Text("en", chinese)
	if chinese == "" || english == chinese {
		t.Fatalf("need distinct default and English denial text")
	}
	op := generationtool.Operation{OwnerID: "owner-a", SessionID: "session-a", AssistantMessageID: "turn-a"}
	requestEnglish := catalog.WithLocale(context.Background(), "en")

	for name, service := range map[string]*Service{
		"default locale not wired": {chat: failedTurnChat{}, texts: texts},
		"default locale wired":     {chat: failedTurnChat{}, texts: texts, defaultLocale: "zh-CN"},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := service.inTurnLocale(requestEnglish, op)
			if got := service.text(ctx, chinese); got != chinese {
				t.Fatalf("text = %q, want the default %q (English %q)", got, chinese, english)
			}
		})
	}
}
