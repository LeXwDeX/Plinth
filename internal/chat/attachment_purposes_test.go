package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

func TestAttachmentPurposeValidation(t *testing.T) {
	for _, tc := range []struct {
		name     string
		purposes []chat.AttachmentPurpose
		want     error
	}{
		{"legacy", nil, nil},
		{"valid", []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}, {ImageSlot: 2, Purpose: "input"}}, nil},
		{"zero slot", []chat.AttachmentPurpose{{ImageSlot: 0, Purpose: "style"}}, chat.ErrInvalidAttachmentPurpose},
		{"out of range", []chat.AttachmentPurpose{{ImageSlot: 3, Purpose: "style"}}, chat.ErrInvalidAttachmentPurpose},
		{"unknown", []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "other"}}, chat.ErrInvalidAttachmentPurpose},
		{"duplicate", []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}, {ImageSlot: 1, Purpose: "layout"}}, chat.ErrInvalidAttachmentPurpose},
		{"two inputs", []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "input"}, {ImageSlot: 2, Purpose: "input"}}, chat.ErrInvalidAttachmentPurpose},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier := &recordingVerifier{held: map[string]map[string]bool{}}
			service, _, _ := newHandleService(t, verifier)
			ctx := context.Background()
			session, err := service.CreateSession(ctx, "owner", "Owner", "chat")
			if err != nil {
				t.Fatal(err)
			}
			_, err = service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: session.ID,
				Content: "edit", ImageURLs: []string{"data:image/png;base64,QQ==", "data:image/png;base64,Qg=="}, AttachmentPurposes: tc.purposes})
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestAttachmentPurposeRejectsUnverifiedClaim(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{"another-owner/other-session": {"foreign": true}}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	command := chat.StartTurn{OwnerID: "owner", SessionID: session.ID, Content: "edit",
		ImageURLs: []string{"/foreign"}, Attachments: []chat.AttachmentRef{{ImageSlot: 1, AssetID: "foreign"}},
		AttachmentPurposes: []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}}}
	if _, err := service.StartTurn(ctx, command); !errors.Is(err, chat.ErrAttachmentPurposeAttachmentInvalid) {
		t.Fatalf("got %v", err)
	}
	// An old client does not opt into strict binding, so its rejected claim
	// retains the existing plain-upload fallback.
	command.AttachmentPurposes = nil
	if _, err := service.StartTurn(ctx, command); err != nil {
		t.Fatalf("legacy claim: %v", err)
	}
}

func TestAttachmentPurposesFreezeAssignedHandles(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assistant, err := service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: session.ID,
		Content: "edit", ImageURLs: []string{"data:image/png;base64,QQ==", "data:image/png;base64,Qg=="},
		AttachmentPurposes: []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}, {ImageSlot: 2, Purpose: "input"}}})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(ctx, "owner", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.Options.AttachmentPurposes) != 2 || turn.Options.AttachmentPurposes["@img1"] != "style" || turn.Options.AttachmentPurposes["@img2"] != "input" {
		t.Fatalf("purposes = %#v", turn.Options.AttachmentPurposes)
	}
}

func TestPurposeRejectsAmbiguousClaims(t *testing.T) {
	verifier := &recordingVerifier{held: map[string]map[string]bool{}}
	service, _, _ := newHandleService(t, verifier)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: session.ID, Content: "edit",
		ImageURLs:          []string{"data:image/png;base64,QQ=="},
		Attachments:        []chat.AttachmentRef{{ImageSlot: 1}, {ImageSlot: 1, TaskID: "foreign"}},
		AttachmentPurposes: []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}}})
	if !errors.Is(err, chat.ErrAttachmentPurposeAttachmentInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestPurposeRejectsClaimFromAnotherOwnerOrSession(t *testing.T) {
	for _, foreignOwner := range []bool{false, true} {
		verifier := &recordingVerifier{held: map[string]map[string]bool{}}
		service, _, _ := newHandleService(t, verifier)
		ctx := context.Background()
		session, err := service.CreateSession(ctx, "owner", "Owner", "chat")
		if err != nil {
			t.Fatal(err)
		}
		key := "owner/other-session"
		if foreignOwner {
			key = "other-owner/" + session.ID
		}
		verifier.held[key] = map[string]bool{"foreign": true}
		_, err = service.StartTurn(ctx, chat.StartTurn{OwnerID: "owner", SessionID: session.ID, Content: "edit",
			ImageURLs: []string{"/foreign"}, Attachments: []chat.AttachmentRef{{ImageSlot: 1, TaskID: "foreign"}},
			AttachmentPurposes: []chat.AttachmentPurpose{{ImageSlot: 1, Purpose: "style"}}})
		if !errors.Is(err, chat.ErrAttachmentPurposeAttachmentInvalid) {
			t.Fatalf("foreignOwner=%v: %v", foreignOwner, err)
		}
	}
}
