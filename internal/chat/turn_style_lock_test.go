package chat_test

import (
	"context"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestStartTurnFreezesStyleLockAndSingleTurnSkip(t *testing.T) {
	service, _ := newStyleLockService(t, styleLockVerifier{owned: map[string]bool{}})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "style")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetStyleLock(ctx, "owner-a", session.ID, true, "ink", nil); err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "draw",
		Options: chat.TurnOptions{StyleLockSkipped: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !turn.Options.StyleLock.Enabled || turn.Options.StyleLock.Text != "ink" || !turn.Options.StyleLockSkipped {
		t.Fatalf("frozen style lock = %+v", turn.Options)
	}
	if _, err := service.SetStyleLock(ctx, "owner-a", session.ID, false, "", nil); err != nil {
		t.Fatal(err)
	}
	turn, err = service.GetTurn(ctx, "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !turn.Options.StyleLock.Enabled || turn.Options.StyleLock.Text != "ink" || !turn.Options.StyleLockSkipped {
		t.Fatalf("frozen turn changed after session update = %+v", turn.Options)
	}
}
