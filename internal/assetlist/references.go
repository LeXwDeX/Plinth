package assetlist

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// ErrReferenceInvalid reports a reference that does not name one of the
// owner's pictures, or a crop reference that breaks the one-crop-per-item and
// same-source rules (#331). It stays an ErrInvalid refusal, so transports and
// the proposal tool keep their existing mapping.
var ErrReferenceInvalid = errors.New("assetlist: invalid reference")

// InputAssetReference is one stored input asset as reference validation sees
// it.
type InputAssetReference struct {
	// MediaType is the stored media type; only pictures may be referenced.
	MediaType string
	// IsCrop reports whether the asset is a stored crop. A crop asset whose
	// provenance row is missing is reported as a crop with an empty source, so
	// the service refuses it instead of treating it as a plain picture.
	IsCrop bool
	// CropSourceAssetID is the source picture the crop was cut from.
	CropSourceAssetID generation.AssetID
}

// ReferenceAssets resolves one item's input-asset references against the
// owner's stored assets and crop provenance. internal/app adapts the input
// asset service and the SQLite crop repository; this package never reads the
// asset tables itself, and a nil port keeps only the #327 shape checks.
type ReferenceAssets interface {
	// ResolveInputAsset returns the owner's stored input asset. found is false
	// when the id names no input asset of the owner (including another owner's
	// asset, so existence never leaks).
	ResolveInputAsset(ctx context.Context, ownerID generation.OwnerID,
		inputAssetID generation.AssetID) (InputAssetReference, bool, error)
}

// referenceRefusal is an invalid reference that also reports ErrInvalid.
type referenceRefusal struct{ reason string }

func (refusal referenceRefusal) Error() string {
	return "assetlist: invalid reference: " + refusal.reason
}

func (referenceRefusal) Is(target error) bool {
	return target == ErrInvalid || target == ErrReferenceInvalid
}

func invalidReference(reason string) error { return referenceRefusal{reason: reason} }

// cropReferenceScope validates the crop rules of one item set: at most one
// crop per item, and every crop of the list cut from the same source picture.
// The item count resets per item; the source carries across the whole list.
type cropReferenceScope struct {
	itemCrops int
	source    generation.AssetID
	hasSource bool
}

// resetItem starts the next item's per-item rule.
func (scope *cropReferenceScope) resetItem() { scope.itemCrops = 0 }

// addCrop applies both crop rules to one crop reference.
func (scope *cropReferenceScope) addCrop(source generation.AssetID) error {
	scope.itemCrops++
	if scope.itemCrops > 1 {
		return invalidReference("an item may reference at most one crop")
	}
	if scope.hasSource && scope.source != source {
		return invalidReference("crops in one list must come from the same source picture")
	}
	scope.source, scope.hasSource = source, true
	return nil
}

// validateInputReference checks one input-asset reference against the port and
// the crop rules. A nil port keeps the #327 behavior: the reference's shape
// was already checked and its source is not re-verified here.
func (service *Service) validateInputReference(ctx context.Context, ownerID string,
	reference project.Reference, scope *cropReferenceScope,
) error {
	if service.referenceAssets == nil {
		return nil
	}
	asset, found, err := service.referenceAssets.ResolveInputAsset(ctx,
		generation.OwnerID(ownerID), generation.AssetID(reference.InputAssetID))
	if err != nil {
		return fmt.Errorf("assetlist: resolve reference: %w", err)
	}
	if !found {
		return invalidReference("the input asset does not exist or belongs to another owner")
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(asset.MediaType)), "image/") {
		return invalidReference("the reference is not a picture")
	}
	if !asset.IsCrop {
		return nil
	}
	if asset.CropSourceAssetID == "" {
		return invalidReference("the crop has no provenance")
	}
	return scope.addCrop(asset.CropSourceAssetID)
}
