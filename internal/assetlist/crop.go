package assetlist

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/listcrop"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// ErrCropRegionInvalid reports a crop instruction whose normalized region is
// out of bounds or has no area (#331). It stays an ErrInvalid refusal, so a
// transport without a mapping still answers 400.
var ErrCropRegionInvalid = errors.New("assetlist: invalid crop region")

// ErrCropRegionTooSmall reports a crop instruction whose shortest side is under
// application.asset_list.decompose.crop.minimum_short_edge (#331). It is also
// an ErrCropRegionInvalid and an ErrInvalid refusal.
var ErrCropRegionTooSmall = errors.New("assetlist: crop region too small")

// ErrCropImageTooLarge reports a crop instruction refused by a byte or pixel
// limit: the source picture, the region area or the encoded crop (#331). It is
// also an ErrInvalid refusal.
var ErrCropImageTooLarge = errors.New("assetlist: crop image too large")

// CropInstruction is the optional per-item edit instruction (#331): cut this
// normalized region out of the owner's input picture and store the result as
// the item's crop reference. Proposals do not carry instructions; the Agent
// cuts through crop_image_regions and references the returned asset ids.
type CropInstruction struct {
	// SourceInputAssetID names one of the owner's stored input pictures. A Task
	// result is not accepted here, even though the crop tool accepts one, and
	// neither is a stored crop.
	SourceInputAssetID string `json:"source_input_asset_id"`
	// Region is normalized relative to the source picture, right and bottom
	// edges exclusive, like the crop tool.
	Region listcrop.Region `json:"region"`
}

// Cropper cuts normalized regions out of one owned source picture. listcrop is
// the production implementation; internal/app injects it through Config and
// this package never reads the asset tables itself.
type Cropper interface {
	CropRegions(ctx context.Context, request listcrop.Request) ([]listcrop.Result, error)
}

// cropRegionRefusal is one refused crop region. tooSmall distinguishes the
// shortest-side refusal from malformed or out-of-bounds coordinates.
type cropRegionRefusal struct {
	reason   string
	tooSmall bool
}

func (refusal cropRegionRefusal) Error() string {
	return "assetlist: crop region: " + refusal.reason
}

func (refusal cropRegionRefusal) Is(target error) bool {
	switch target {
	case ErrInvalid, ErrCropRegionInvalid:
		return true
	case ErrCropRegionTooSmall:
		return refusal.tooSmall
	default:
		return false
	}
}

// cropImageTooLargeRefusal is the byte or pixel limit refusal. It stays an
// ErrInvalid refusal so a transport without a mapping still answers 400.
type cropImageTooLargeRefusal struct{ reason string }

func (refusal cropImageTooLargeRefusal) Error() string {
	return "assetlist: crop image too large: " + refusal.reason
}

func (cropImageTooLargeRefusal) Is(target error) bool {
	return target == ErrInvalid || target == ErrCropImageTooLarge
}

// applyCrop cuts one region out of the owner's source picture and returns the
// item's new reference set: the item's previous crop references are replaced,
// every other reference is preserved in order and the new crop is appended.
// The crop is validated and stored by the injected port, which writes its
// provenance; the returned reference then goes through the same list-wide
// reference validation as a proposal.
func (service *Service) applyCrop(ctx context.Context, ownerID string,
	instruction *CropInstruction, references []project.Reference,
) ([]project.Reference, error) {
	if service.crops == nil || service.referenceAssets == nil {
		return nil, invalidReference("crop instructions are unavailable")
	}
	sourceID := strings.TrimSpace(instruction.SourceInputAssetID)
	if sourceID == "" {
		return nil, invalidReference("a crop instruction needs a source input asset")
	}
	owner := generation.OwnerID(ownerID)
	source, found, err := service.referenceAssets.ResolveInputAsset(ctx, owner,
		generation.AssetID(sourceID))
	if err != nil {
		return nil, fmt.Errorf("assetlist: resolve crop source: %w", err)
	}
	if !found || !strings.HasPrefix(strings.ToLower(strings.TrimSpace(source.MediaType)), "image/") {
		return nil, invalidReference("the crop source is not one of the owner's input pictures")
	}
	// A stored crop is a reference for the re-draw step, never a crop source:
	// cropping a crop would blur both the picture and the provenance chain.
	if source.IsCrop {
		return nil, invalidReference("the crop source is itself a stored crop")
	}
	kept := make([]project.Reference, 0, len(references)+1)
	for _, reference := range references {
		if reference.InputAssetID == "" {
			kept = append(kept, reference)
			continue
		}
		resolved, found, err := service.referenceAssets.ResolveInputAsset(ctx, owner,
			generation.AssetID(reference.InputAssetID))
		if err != nil {
			return nil, fmt.Errorf("assetlist: resolve reference: %w", err)
		}
		// A crop is replaced by the new one. Because this item gets a new crop,
		// a reference whose asset was deleted is dropped instead of kept: the
		// shared validation would otherwise refuse the whole edit and the person
		// could not even rename the item. Items without a new crop keep the
		// previous behavior, so a stale plain reference still refuses the edit.
		if !found || resolved.IsCrop {
			continue
		}
		kept = append(kept, reference)
	}
	results, err := service.crops.CropRegions(ctx, listcrop.Request{
		Owner:   owner,
		Source:  generation.AssetID(sourceID),
		Regions: []listcrop.Region{instruction.Region},
	})
	if err != nil {
		return nil, cropInstructionRefusal(err)
	}
	if len(results) != 1 || results[0].InputAssetID == "" {
		return nil, fmt.Errorf("assetlist: crop returned %d results for one region", len(results))
	}
	return append(kept, project.Reference{InputAssetID: string(results[0].InputAssetID)}), nil
}

// cropInstructionRefusal maps the crop service's region, source and limit
// refusals to the asset-list vocabulary. A region failure keeps its measured
// kind: too small, over a byte/pixel limit, or over the per-source crop limit.
// A batch limit becomes ErrImageItemLimit; an unusable source becomes
// ErrReferenceInvalid. Anything else (an unavailable store, a corrupt result)
// stays a service error.
func cropInstructionRefusal(err error) error {
	var failure *listcrop.RegionFailure
	if errors.As(err, &failure) {
		var (
			tooSmall *listcrop.TooSmallError
			tooLarge *listcrop.TooLargeError
			limit    *listcrop.LimitError
		)
		switch {
		case errors.As(err, &tooSmall):
			return cropRegionRefusal{reason: err.Error(), tooSmall: true}
		case errors.As(err, &tooLarge):
			return cropImageTooLargeRefusal{reason: err.Error()}
		case errors.As(err, &limit):
			return imageItemLimitRefusal{reason: "too many items for one source picture"}
		default:
			return cropRegionRefusal{reason: err.Error()}
		}
	}
	switch {
	case errors.Is(err, listcrop.ErrLimit):
		return imageItemLimitRefusal{reason: "too many items for one source picture"}
	case errors.Is(err, listcrop.ErrTooLarge):
		return cropImageTooLargeRefusal{reason: err.Error()}
	case errors.Is(err, listcrop.ErrNotFound),
		errors.Is(err, listcrop.ErrInvalidSource),
		errors.Is(err, listcrop.ErrInvalid):
		return invalidReference("the crop source is not a usable picture: " + err.Error())
	default:
		return fmt.Errorf("assetlist: crop: %w", err)
	}
}
