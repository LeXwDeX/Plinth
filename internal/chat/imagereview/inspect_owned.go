package imagereview

// The read-only inspection entry the external MCP inspect_image tool calls
// (#332 7b-2). It owns the whole read: validate the Schema checklist and the
// purpose argument, open the named picture for its owner, enforce the byte and
// pixel caps, then reuse the same check assembly as the platform tool. It never
// touches a session, a round, credits, a stored self-check or a visual verdict.

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

// The Kind of an InspectRefusal. The MCP handler maps each to its own business
// code; the words stay here so a refusal reads the same for every caller.
const (
	InspectNotFound         = "not_found"
	InspectNotReady         = "not_ready"
	InspectTooLarge         = "too_large"
	InspectUnreadable       = "unreadable"
	InspectUnavailable      = "unavailable"
	InspectUnknownChecklist = "unknown_checklist"
	InspectUnknownPurpose   = "unknown_purpose"
)

// InspectRefusal is one inspection the service refused before checking any
// bytes. Message is ready to show to the caller and never carries a handle.
type InspectRefusal struct {
	Kind    string
	Message string
}

func (refusal *InspectRefusal) Error() string { return refusal.Message }

// PictureSource names the one picture to inspect: a direct input asset (an
// upload), or a saved asset of a finished Task. Exactly one of InputAsset and
// TaskID+TaskAsset must be set; anything else is a caller bug.
type PictureSource struct {
	InputAsset generation.AssetID
	TaskID     generation.TaskID
	TaskAsset  generation.AssetID
}

// InspectRequest is one read-only inspection. MaxBytes and MaxPixels of zero
// use the Schema's chat.image_review.limits; a positive value is capped by the
// Schema's own limit, so a caller can narrow a limit but never loosen it.
type InspectRequest struct {
	Owner     generation.OwnerID
	Source    PictureSource
	Checklist string
	// Purpose optionally names a chat.image2d_usage profile to judge the
	// delivery checklist against. It is refused with 3d_input, and an unknown
	// name is unknown_purpose.
	Purpose string
	// Brief supplies the session's production brief when Purpose is empty. An
	// explicit Purpose still honours the brief's transparent request.
	Brief     chat.ProductionBrief
	MaxBytes  int64
	MaxPixels int64
}

// InspectOwned inspects one picture the owner holds, or refuses it. Checklist
// and purpose are validated before any bytes are opened; the byte cap is judged
// from the recorded size before the body is read, the pixel cap while the
// picture is decoded. The check assembly is the platform tool's own.
func (service *Service) InspectOwned(ctx context.Context, request InspectRequest) (Inspection, error) {
	checklistName := request.Checklist
	if checklistName == "" {
		checklistName = "delivery"
	}
	if _, _, ok := service.checklist(checklistName); !ok {
		return Inspection{}, refuse(InspectUnknownChecklist, "unknown checklist")
	}
	var judgement *purposeJudgement
	if request.Purpose != "" {
		// The 3D input checklist has its own gate and judges no 2D purpose.
		if checklistName != "delivery" {
			return Inspection{}, refuse(InspectUnknownPurpose, "unknown purpose")
		}
		judgement = service.judgementForPurpose(request.Purpose, request.Brief)
		if judgement == nil {
			return Inspection{}, refuse(InspectUnknownPurpose, "unknown purpose")
		}
	} else if checklistName == "delivery" {
		judgement = service.judgement(request.Brief)
	}
	limits := imageinspect.Limits{
		MaxBytes:  effectiveLimit(request.MaxBytes, service.review.Limits.MaxBytes),
		MaxPixels: effectiveLimit(request.MaxPixels, service.review.Limits.MaxPixels),
	}
	data, err := service.readOwned(ctx, request.Owner, request.Source, limits.MaxBytes)
	if err != nil {
		return Inspection{}, err
	}
	checked, err := service.inspectBytesWith(data, checklistName, judgement, limits)
	if err != nil {
		if errors.Is(err, errUnknownChecklist) {
			return Inspection{}, refuse(InspectUnknownChecklist, "unknown checklist")
		}
		if errors.Is(err, imageinspect.ErrTooLarge) {
			return Inspection{}, refuse(InspectTooLarge, err.Error())
		}
		return Inspection{}, refuse(InspectUnreadable, err.Error())
	}
	// The external service inspects PNG, JPEG and WebP only. A decoder another
	// package registered for this process (GIF) must not widen that contract.
	if checked.MediaType == "" {
		return Inspection{}, refuse(InspectUnreadable, "unsupported picture format")
	}
	return checked, nil
}

// readOwned opens the one named picture for its owner and reads its bytes
// within the byte cap.
func (service *Service) readOwned(ctx context.Context, owner generation.OwnerID, source PictureSource,
	maxBytes int64,
) ([]byte, error) {
	hasInput := source.InputAsset != ""
	hasTask := source.TaskID != "" || source.TaskAsset != ""
	if hasInput && !hasTask {
		return service.readOwnedInput(ctx, owner, source.InputAsset, maxBytes)
	}
	if hasTask && !hasInput && source.TaskID != "" && source.TaskAsset != "" {
		return service.readOwnedTask(ctx, owner, source.TaskID, source.TaskAsset, maxBytes)
	}
	return nil, fmt.Errorf("image review: picture source must name exactly one input asset or task asset")
}

func (service *Service) readOwnedInput(ctx context.Context, owner generation.OwnerID,
	id generation.AssetID, maxBytes int64,
) ([]byte, error) {
	if service.inputs == nil {
		return nil, refuse(InspectUnavailable, "image inspection is unavailable")
	}
	asset, body, err := service.inputs.OpenOwned(ctx, owner, id)
	if err != nil {
		return nil, openRefusal(err)
	}
	if body == nil {
		return nil, refuse(InspectNotReady, "picture has no stored asset")
	}
	defer body.Close()
	if overLimit(asset.Size, maxBytes) {
		return nil, tooLargeRefusal(maxBytes)
	}
	return readOwnedLimited(body, maxBytes)
}

func (service *Service) readOwnedTask(ctx context.Context, owner generation.OwnerID,
	task generation.TaskID, asset generation.AssetID, maxBytes int64,
) ([]byte, error) {
	if service.tasks == nil {
		return nil, refuse(InspectUnavailable, "image inspection is unavailable")
	}
	content, err := service.tasks.OpenAsset(ctx, owner, task, asset)
	if err != nil {
		return nil, openRefusal(err)
	}
	if content.Body == nil {
		return nil, refuse(InspectNotReady, "picture has no stored asset")
	}
	defer content.Body.Close()
	if overLimit(content.Size, maxBytes) {
		return nil, tooLargeRefusal(maxBytes)
	}
	return readOwnedLimited(content.Body, maxBytes)
}

// readOwnedLimited is readLimited with its refusals classified.
func readOwnedLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	data, tooLarge, err := readLimitedWithin(reader, maxBytes)
	if err != nil {
		return nil, refuse(InspectUnreadable, err.Error())
	}
	if tooLarge {
		return nil, tooLargeRefusal(maxBytes)
	}
	return data, nil
}

// openRefusal classifies a reader error. A foreign owner reads exactly like a
// missing picture, as everywhere else; a storage outage is unavailable.
func openRefusal(err error) error {
	if generation.IsCode(err, generation.ErrUnavailable) {
		return refuse(InspectUnavailable, "asset is not readable")
	}
	return refuse(InspectNotFound, "asset is not readable")
}

func overLimit(size, maxBytes int64) bool {
	return maxBytes > 0 && size > maxBytes
}

func tooLargeRefusal(maxBytes int64) error {
	return refuse(InspectTooLarge, fmt.Sprintf("picture is over the %d-byte limit", maxBytes))
}

// effectiveLimit applies the caller's cap within the Schema's.
func effectiveLimit(requested, schema int64) int64 {
	if requested <= 0 {
		return schema
	}
	if schema > 0 && requested > schema {
		return schema
	}
	return requested
}

func refuse(kind, message string) error {
	return &InspectRefusal{Kind: kind, Message: message}
}
