package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// cropVerifier gives the owner's crop assets the crop role, like the
// production verifier does from stored crop provenance; every other claim is
// verified by recordingVerifier.
type cropVerifier struct {
	recordingVerifier
	crops map[string]bool
}

func (v *cropVerifier) VerifyAttachment(ctx context.Context, owner, session string, ref chat.AttachmentRef) (chat.Attachment, error) {
	attachment, err := v.recordingVerifier.VerifyAttachment(ctx, owner, session, ref)
	if err == nil && attachment.Role == chat.AttachmentRoleUpload && v.crops[attachment.AssetID] {
		attachment.Role = chat.AttachmentRoleCrop
	}
	return attachment, err
}

// #587: an Agent crop has no Task but is an asset of the session; it can be
// picked once the session's messages show it with the crop role. A person's
// own upload, a crop another session shows and a crop claimed otherwise are
// still refused, and the crop pick records no direction preference.
func TestPickCandidateAcceptsACropTheSessionShows(t *testing.T) {
	verifier := &cropVerifier{recordingVerifier: recordingVerifier{held: map[string]map[string]bool{}},
		crops: map[string]bool{"crop-1": true, "crop-other": true}}
	service, repository := newPreferenceService(t, verifier, nil)
	ctx := context.Background()
	session, err := service.CreateSession(ctx, "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	other, err := service.CreateSession(ctx, "owner-a", "Owner", "other")
	if err != nil {
		t.Fatal(err)
	}
	verifier.held["owner-a/"+session.ID] = map[string]bool{"crop-1": true, "crop-other": true, "upload-1": true, "task-1": true}
	repository.mu.Lock()
	repository.messages[session.ID] = []chat.Message{{
		ID: "assistant-crops", SessionID: session.ID, Role: "assistant",
		Attachments: []chat.Attachment{
			{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCrop, AssetID: "crop-1"},
			{Handle: "@img3", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, AssetID: "upload-1"},
		},
	}}
	repository.messages[other.ID] = []chat.Message{{
		ID: "assistant-other", SessionID: other.ID, Role: "assistant",
		Attachments: []chat.Attachment{{Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleCrop, AssetID: "crop-other"}},
	}}
	repository.mu.Unlock()

	for name, ref := range map[string]chat.AttachmentRef{
		"the person's own upload":      {AssetID: "upload-1"},
		"a crop another session shows": {AssetID: "crop-other"},
		"a crop claimed as a capture":  {AssetID: "crop-1", CaptureID: "capture-1"},
		"a crop claimed by URL":        {AssetID: "crop-1", URL: "/api/v1/assets/crop-1"},
	} {
		if _, err := service.PickCandidate(ctx, "owner-a", session.ID, ref); !errors.Is(err, chat.ErrPickRejected) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	picked, err := service.PickCandidate(ctx, "owner-a", session.ID, chat.AttachmentRef{AssetID: "crop-1"})
	if err != nil || picked.TaskID != "" || picked.AssetID != "crop-1" || picked.Role != chat.AttachmentRoleCrop || picked.Handle != "@img2" {
		t.Fatalf("crop pick = %+v, %v", picked, err)
	}
	if stored, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || !ok || stored.AssetID != "crop-1" || stored.Role != chat.AttachmentRoleCrop {
		t.Fatalf("stored crop pick = %+v %v %v", stored, ok, err)
	}
	// Direction preferences name a generated Task's picture; a crop pick adds none.
	if preferences, err := service.ListPreferences(ctx, "owner-a", session.ID); err != nil || len(preferences) != 0 {
		t.Fatalf("a crop pick recorded preferences: %+v %v", preferences, err)
	}
	// Another owner cannot pick it.
	if _, err := service.PickCandidate(ctx, "owner-b", session.ID, chat.AttachmentRef{AssetID: "crop-1"}); !errors.Is(err, chat.ErrSessionNotFound) {
		t.Fatalf("foreign crop pick err = %v", err)
	}
	// A stored crop pick the session no longer shows is ignored, never trusted.
	repository.mu.Lock()
	repository.messages[session.ID] = nil
	repository.mu.Unlock()
	if pick, ok, err := service.SessionPick(ctx, "owner-a", session.ID); err != nil || ok {
		t.Fatalf("a crop the session no longer shows stayed picked: %+v %v %v", pick, ok, err)
	}
}
