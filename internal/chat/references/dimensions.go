package references

import (
	"context"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"

	_ "golang.org/x/image/webp"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

var _ chat.ImageDimensionReader = (*Resolver)(nil)

// maximumDimensionHeaderBytes bounds how much of a picture is read to find its
// size; the header of every accepted format lies well within it.
const maximumDimensionHeaderBytes = 8 << 20

// ImageDimensions reads the pixel size of an input asset the owner holds. The
// chat service measures an artboard snapshot with it instead of trusting a
// size the client claims (#272). Only the image header is decoded.
func (resolver *Resolver) ImageDimensions(ctx context.Context, ownerID, assetID string) (int, int, error) {
	if ownerID == "" || assetID == "" {
		return 0, 0, chat.ErrAttachmentRejected
	}
	_, body, err := resolver.inputs.OpenOwned(ctx, generation.OwnerID(ownerID), generation.AssetID(assetID))
	if err != nil {
		return 0, 0, err
	}
	defer body.Close()
	config, _, err := image.DecodeConfig(io.LimitReader(body, maximumDimensionHeaderBytes))
	if err != nil {
		return 0, 0, err
	}
	if config.Width <= 0 || config.Height <= 0 {
		return 0, 0, errors.New("chat references: picture without a size")
	}
	return config.Width, config.Height, nil
}

var _ chat.InputAssetDigester = (*Resolver)(nil)

// InputAssetRecords reads an owner's input asset record without opening its
// bytes. The input asset service implements it; OpenOwned would re-hash the
// whole file on every call (#49).
type InputAssetRecords interface {
	OwnedInputAsset(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, error)
}

// InputAssetDigest returns the SHA-256 the platform recorded for an input
// asset the owner holds. The chat service compares an artboard snapshot with
// the session's earlier pictures by it (#49); only the record is read, never
// the bytes, so comparing against a long history stays cheap.
func (resolver *Resolver) InputAssetDigest(ctx context.Context, ownerID, assetID string) (string, error) {
	if ownerID == "" || assetID == "" {
		return "", chat.ErrAttachmentRejected
	}
	records, ok := resolver.inputs.(InputAssetRecords)
	if !ok {
		return "", errors.New("chat references: input asset records unavailable")
	}
	asset, err := records.OwnedInputAsset(ctx, generation.OwnerID(ownerID), generation.AssetID(assetID))
	if err != nil {
		return "", err
	}
	return asset.SHA256, nil
}
