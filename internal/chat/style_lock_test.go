package chat_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

type styleLockVerifier struct {
	owned map[string]bool
}

func (v styleLockVerifier) VerifyAttachment(_ context.Context, owner, _ string, ref chat.AttachmentRef) (chat.Attachment, error) {
	if owner != "owner-a" || ref.Kind != chat.AttachmentKindImage || !v.owned[ref.AssetID] {
		return chat.Attachment{}, chat.ErrAttachmentRejected
	}
	return chat.Attachment{Kind: ref.Kind, AssetID: ref.AssetID}, nil
}

func newStyleLockService(t *testing.T, verifier chat.AttachmentVerifier) (*chat.Service, *memoryChatRepository) {
	t.Helper()
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.MaximumStyleLockText = 5
		config.MaximumStyleLockReferences = 2
		config.Attachments = verifier
	})
	return service, repository
}

func TestStyleLockDefaultsPersistsAndReplacesAsAWhole(t *testing.T) {
	service, _ := newStyleLockService(t, styleLockVerifier{owned: map[string]bool{"asset-a": true}})
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "style")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := service.StyleLock(ctx, "owner-a", session.ID); err != nil || got.Enabled || got.Text != "" || len(got.References) != 0 {
		t.Fatalf("default style lock = %+v, %v", got, err)
	}
	stored, err := service.SetStyleLock(ctx, "owner-a", session.ID, true, "  ink  ", []string{"asset-a"})
	if err != nil || !stored.Enabled || stored.Text != "ink" || len(stored.References) != 1 || stored.References[0] != "asset-a" {
		t.Fatalf("stored style lock = %+v, %v", stored, err)
	}
	stored, err = service.SetStyleLock(ctx, "owner-a", session.ID, false, "", nil)
	if err != nil || stored.Enabled || stored.Text != "" || len(stored.References) != 0 {
		t.Fatalf("replacement = %+v, %v", stored, err)
	}
}

func TestStyleLockRejectsBoundsForeignReferencesAndSessions(t *testing.T) {
	service, _ := newStyleLockService(t, styleLockVerifier{owned: map[string]bool{"asset-a": true}})
	ctx := context.Background()
	session, _ := service.CreateSession(ctx, "owner-a", "Owner", "style")
	if _, err := service.SetStyleLock(ctx, "owner-a", session.ID, true, strings.Repeat("设", 6), nil); !errors.Is(err, chat.ErrStyleLockTextTooLong) {
		t.Fatalf("overlong text error = %v", err)
	}
	if _, err := service.SetStyleLock(ctx, "owner-a", session.ID, true, "ok", []string{"a", "b", "c"}); !errors.Is(err, chat.ErrStyleLockReferenceLimit) {
		t.Fatalf("reference limit error = %v", err)
	}
	if _, err := service.SetStyleLock(ctx, "owner-a", session.ID, true, "ok", []string{"asset-b"}); !errors.Is(err, chat.ErrStyleLockReferenceInvalid) {
		t.Fatalf("foreign reference error = %v", err)
	}
	if _, err := service.StyleLock(ctx, "owner-b", session.ID); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign session read error = %v", err)
	}
}
