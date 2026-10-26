package chat

import (
	"context"
	"errors"
)

// ErrPickRejected reports a candidate that cannot be picked: it is not a
// generated 2D picture of the owner's session (a preview of a 3D model is not
// one). The claimed IDs are not echoed.
var ErrPickRejected = errors.New("chat: candidate cannot be picked")

// PickCandidate makes one generated picture the session's current object. The
// claim is verified for the owner and session exactly like a selected object,
// and only a generated 2D picture (a candidate with a Task and an asset whose
// Task is not a 3D model) can be picked. The returned attachment carries the
// handle the session already gave that picture, when it has one.
func (service *Service) PickCandidate(
	ctx context.Context,
	ownerID, sessionID string,
	ref AttachmentRef,
) (Attachment, error) {
	owns, err := service.repository.OwnsSession(ctx, ownerID, sessionID)
	if err != nil {
		return Attachment{}, err
	}
	if !owns {
		return Attachment{}, ErrSessionNotFound
	}
	if service.attachments == nil {
		return Attachment{}, ErrPickRejected
	}
	ref.Kind, ref.ImageSlot = AttachmentKindImage, 0
	verified, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, ref)
	if err != nil || verified.Role != AttachmentRoleGenerated || verified.TaskID == "" || verified.AssetID == "" ||
		service.isModelPreview(ctx, ownerID, sessionID, verified) {
		return Attachment{}, ErrPickRejected
	}
	found, err := service.repository.SetSessionPick(ctx, ownerID, sessionID, verified)
	if err != nil {
		return Attachment{}, err
	}
	if !found {
		return Attachment{}, ErrSessionNotFound
	}
	return service.withKnownHandle(ctx, ownerID, sessionID, verified), nil
}

// ClearSessionPick forgets the session's pick.
func (service *Service) ClearSessionPick(ctx context.Context, ownerID, sessionID string) error {
	found, err := service.repository.SetSessionPick(ctx, ownerID, sessionID, Attachment{})
	if err != nil {
		return err
	}
	if !found {
		return ErrSessionNotFound
	}
	return nil
}

// SessionPick returns the session's picked candidate, re-verified for the
// owner and session. ok is false when nothing is picked, the picked object no
// longer verifies (its Task is gone), or the picked picture is the preview of a
// 3D model (a pick stored before such pictures were refused); a stale pick is
// ignored, never trusted.
func (service *Service) SessionPick(ctx context.Context, ownerID, sessionID string) (pick Attachment, ok bool, err error) {
	stored, found, err := service.repository.SessionPick(ctx, ownerID, sessionID)
	if err != nil {
		return Attachment{}, false, err
	}
	if !found {
		return Attachment{}, false, ErrSessionNotFound
	}
	if stored.Kind == "" || service.attachments == nil {
		return Attachment{}, false, nil
	}
	verified, verifyErr := service.attachments.VerifyAttachment(ctx, ownerID, sessionID, AttachmentRef{
		Kind: stored.Kind, AssetID: stored.AssetID, TaskID: stored.TaskID, CaptureID: stored.CaptureID,
	})
	if verifyErr != nil || service.isModelPreview(ctx, ownerID, sessionID, verified) {
		return Attachment{}, false, nil
	}
	return service.withKnownHandle(ctx, ownerID, sessionID, verified), true, nil
}

// isModelPreview reports whether a verified generated picture belongs to a 3D
// model Task of the session. Such a picture is the model's preview (the render
// of the built model or a picture the provider drew before modelling), not a 2D
// candidate, so it can neither be picked nor stay the picked object. The Task
// counts as a model when it verifies as a model attachment for the owner and
// session. The callers have checked that service.attachments is set.
func (service *Service) isModelPreview(ctx context.Context, ownerID, sessionID string, picture Attachment) bool {
	if picture.Kind != AttachmentKindImage || picture.Role != AttachmentRoleGenerated || picture.TaskID == "" {
		return false
	}
	_, err := service.attachments.VerifyAttachment(ctx, ownerID, sessionID,
		AttachmentRef{Kind: AttachmentKindModel, TaskID: picture.TaskID})
	return err == nil
}

// withKnownHandle gives a verified picture the handle the session's messages
// already carry for the same object. An unseen picture has no handle yet.
func (service *Service) withKnownHandle(ctx context.Context, ownerID, sessionID string, attachment Attachment) Attachment {
	history, err := service.repository.ListMessages(ctx, ownerID, sessionID)
	if err != nil {
		return attachment
	}
	for index := len(history) - 1; index >= 0; index-- {
		for _, candidate := range history[index].Attachments {
			if SameObject(candidate, attachment) {
				attachment.Handle = candidate.Handle
				return attachment
			}
		}
	}
	return attachment
}
