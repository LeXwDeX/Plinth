package references

import (
	"context"
	"encoding/base64"
	"errors"
	"io"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/imaging"
)

var _ chat.SelectionMaskMeasurer = (*Resolver)(nil)

// MeasureMask reads the pixel facts of an owned selection mask (#244). Only a
// PNG input asset of the owner is accepted; the bytes are decoded under the
// Schema's limits. A read or decode failure rejects the mask, which refuses
// the turn instead of degrading into a whole-image redraw.
func (resolver *Resolver) MeasureMask(ctx context.Context, ownerID, assetID string) (chat.SelectionMask, error) {
	if ownerID == "" || assetID == "" {
		return chat.SelectionMask{}, chat.ErrAttachmentRejected
	}
	data, err := resolver.readOwnedInput(ctx, generation.OwnerID(ownerID), generation.AssetID(assetID))
	if err != nil {
		return chat.SelectionMask{}, err
	}
	report, err := imageinspect.MeasureMask(data, resolver.selectionMaskLimits)
	if err != nil {
		return chat.SelectionMask{}, err
	}
	return chat.SelectionMask{
		Width: report.Width, Height: report.Height,
		BBox: report.BBox, Coverage: report.Coverage,
	}, nil
}

// OpenSelectionPreview composes the picture the Agent reads beside a frozen
// selection mask (#244): the owned snapshot dimmed outside the transparent
// (redrawn) region and tinted inside it. Both inputs are owned assets; the
// snapshot is named by its verified platform URL. It reports false when either
// picture cannot be read; the turn keeps its text description then.
func (resolver *Resolver) OpenSelectionPreview(
	ctx context.Context,
	ownerID, pictureURL, maskAssetID string,
) (string, bool) {
	if ownerID == "" || pictureURL == "" || maskAssetID == "" {
		return "", false
	}
	snapshotID, ok := resolver.inputAssetID(pictureURL)
	if !ok {
		return "", false
	}
	owner := generation.OwnerID(ownerID)
	picture, err := resolver.readOwnedInput(ctx, owner, snapshotID)
	if err != nil {
		return "", false
	}
	mask, err := resolver.readOwnedInput(ctx, owner, generation.AssetID(maskAssetID))
	if err != nil {
		return "", false
	}
	preview, err := imaging.RenderSelectionPreview(picture, mask, resolver.selectionPreview)
	if err != nil {
		return "", false
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(preview), true
}

func (resolver *Resolver) inputAssetID(rawURL string) (generation.AssetID, bool) {
	referencePath, err := resolver.localPath(rawURL)
	if err != nil {
		return "", false
	}
	for _, route := range resolver.inputAssetRoutes {
		if values, matched := matchRoute(route, referencePath); matched && len(values) == 1 {
			return generation.AssetID(values[0]), true
		}
	}
	return "", false
}

func (resolver *Resolver) readOwnedInput(
	ctx context.Context,
	owner generation.OwnerID,
	assetID generation.AssetID,
) ([]byte, error) {
	asset, body, err := resolver.inputs.OpenOwned(ctx, owner, assetID)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	limit := resolver.selectionMaskLimits.MaxBytes
	if limit <= 0 {
		limit = 32 << 20
	}
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("chat references: selection mask over the byte limit")
	}
	if asset.Size > 0 && int64(len(data)) != asset.Size {
		return nil, errors.New("chat references: selection mask size changed")
	}
	return data, nil
}
