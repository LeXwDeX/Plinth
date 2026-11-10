package chat

import (
	"context"
	"fmt"
	"strings"
)

// freezeSelectionMask verifies and freezes the selection mask of a canvas turn
// (#244). The mask is an input, not a claim: every failure refuses the turn
// with its own error, so a painted selection can never silently become a
// whole-image redraw. The mask must ride a canvas turn, belong to the owner,
// be readable, select at least one pixel and have exactly the artboard
// snapshot's pixel size; its measured facts are stored with the turn options.
func (service *Service) freezeSelectionMask(ctx context.Context, command StartTurn, options *TurnOptions) error {
	if command.SelectionMask == nil {
		return nil
	}
	assetID := strings.TrimSpace(command.SelectionMask.MaskAssetID)
	if assetID == "" {
		return ErrSelectionMaskInvalid
	}
	if service.canvasInputSource == "" || options.InputSource != service.canvasInputSource {
		return ErrSelectionMaskWithoutCanvas
	}
	snapshot, ok := canvasSnapshotAttachment(*options)
	if !ok || snapshot.AssetID == "" || snapshot.Canvas == nil {
		return ErrSelectionMaskWithoutCanvas
	}
	measurer, ok := service.attachments.(SelectionMaskMeasurer)
	if !ok {
		return ErrSelectionMaskInvalid
	}
	mask, err := measurer.MeasureMask(ctx, command.OwnerID, assetID)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrSelectionMaskInvalid, err)
	}
	if mask.Width != snapshot.Canvas.Width || mask.Height != snapshot.Canvas.Height {
		return ErrSelectionMaskSizeMismatch
	}
	if !mask.Editable() {
		return ErrSelectionMaskInvalid
	}
	mask.MaskAssetID = assetID
	options.SelectionMask = &mask
	return nil
}

// canvasSnapshotAttachment finds the verified artboard snapshot of a canvas
// turn: the selected picture the service marked with the canvas role and whose
// measured layout it stored.
func canvasSnapshotAttachment(options TurnOptions) (Attachment, bool) {
	if options.SelectionPicked {
		return Attachment{}, false
	}
	for _, attachment := range options.Selection {
		if attachment.Kind == AttachmentKindImage && attachment.Role == AttachmentRoleCanvas &&
			attachment.Canvas != nil && attachment.URL != "" {
			return attachment, true
		}
	}
	return Attachment{}, false
}
