// Package listcrop implements the read-only crop_image_regions service
// (#331): it cuts normalized regions out of one owner's source picture at
// original resolution, stores every crop as a lossless PNG input asset of
// type "list_crop" and records its provenance in input_asset_crops.
//
// It creates no Task, charges nothing, counts no step and writes no
// self-check. A Task result used as the source is first copied into an input
// asset, so the source form is uniform; the provenance keeps the original
// form as a snapshot. Owner-scoped reads never reveal another owner's asset.
package listcrop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/imageinspect"
)

const (
	// CropAssetType is the asset_type of every crop this service stores.
	CropAssetType = "list_crop"

	// SourceKindInput marks a source read from an input asset.
	SourceKindInput = "input_asset"
	// SourceKindTask marks a source copied from a Task result.
	SourceKindTask = "task_asset"

	// DefaultMaximumCropsPerSource bounds the crops one source keeps. It is the
	// fallback for application.asset_list.decompose.maximum_items_per_image when
	// the Catalog declares no decompose section.
	DefaultMaximumCropsPerSource = 24
	// DefaultSourceMaxBytes mirrors image.mask.limits.max_bytes.
	DefaultSourceMaxBytes int64 = 32 << 20
	// DefaultSourceMaxPixels mirrors image.mask.limits.max_pixels.
	DefaultSourceMaxPixels int64 = 40_000_000
	// DefaultMinimumSide is the shortest side one crop may keep.
	DefaultMinimumSide = 64
	// DefaultMaximumRegionPixels is the largest area one crop may cover.
	DefaultMaximumRegionPixels int64 = 4_000_000
	// DefaultUploadMaxBytes bounds the encoded PNG of one crop. The live value
	// comes from UploadAsset.MaxBytes in the Catalog (#331b).
	DefaultUploadMaxBytes int64 = 16 << 20
	// DefaultSourceAssetType is the asset_type of a copied Task result.
	DefaultSourceAssetType = "image"

	// normalizedEpsilon tolerates floating-point noise at the normalized
	// right/bottom edge (0.7 + 0.3 is 0.9999999999999999).
	normalizedEpsilon = 1e-9
)

// Region is a normalized region relative to the source picture: x, y, width
// and height are in [0, 1]. The right and bottom edges are exclusive.
type Region struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

// PixelRect is a source-pixel rectangle. X1 and Y1 are exclusive.
type PixelRect struct {
	X0 int `json:"x0"`
	Y0 int `json:"y0"`
	X1 int `json:"x1"`
	Y1 int `json:"y1"`
}

// Width is the rectangle width in pixels.
func (rect PixelRect) Width() int { return rect.X1 - rect.X0 }

// Height is the rectangle height in pixels.
func (rect PixelRect) Height() int { return rect.Y1 - rect.Y0 }

// Crop is one stored provenance row.
type Crop struct {
	AssetID       generation.AssetID
	OwnerID       generation.OwnerID
	SourceAssetID generation.AssetID
	// SourceKind is the source form snapshot: SourceKindInput or
	// SourceKindTask.
	SourceKind string
	// SourceInputAssetID is the input asset whose bytes were cropped. It
	// equals SourceAssetID for an input source and names the copy made from a
	// Task result for a Task source.
	SourceInputAssetID generation.AssetID
	SourceWidth        int
	SourceHeight       int
	Region             Region
	PixelRect          PixelRect
	SHA256             string
	CreatedAt          time.Time
}

// Result is one crop as the tool returns it: the asset id and the geometry
// the caller can name again, never the picture bytes.
func (crop Crop) Result() Result {
	return Result{
		InputAssetID:     crop.AssetID,
		NormalizedRegion: crop.Region,
		PixelRect:        crop.PixelRect,
		SourceWidth:      crop.SourceWidth,
		SourceHeight:     crop.SourceHeight,
		SHA256:           crop.SHA256,
	}
}

// Result is one cropped region's public description.
type Result struct {
	InputAssetID     generation.AssetID `json:"input_asset_id"`
	NormalizedRegion Region             `json:"normalized_region"`
	PixelRect        PixelRect          `json:"pixel_rect"`
	SourceWidth      int                `json:"source_width"`
	SourceHeight     int                `json:"source_height"`
	SHA256           string             `json:"sha256"`
}

// Request is one batch crop. Source is an owned input asset id; a Task result
// is accepted too and copied into an input asset first. Regions are normalized
// and relative to the source picture.
type Request struct {
	Owner   generation.OwnerID
	Source  generation.AssetID
	Regions []Region
}

// Owner-scoped errors. A missing or foreign source is always ErrNotFound so
// transport layers can refuse without leaking existence.
var (
	ErrInvalid       = errors.New("listcrop: invalid request")
	ErrNotFound      = errors.New("listcrop: source asset not found")
	ErrInvalidSource = errors.New("listcrop: source asset is not a supported image")
	ErrInvalidRegion = errors.New("listcrop: invalid region")
	ErrTooLarge      = errors.New("listcrop: picture too large")
	ErrLimit         = errors.New("listcrop: crop limit reached")
	ErrUnavailable   = errors.New("listcrop: unavailable")
)

// RegionError explains one refused region.
type RegionError struct {
	Reason string
}

func (e *RegionError) Error() string { return "listcrop: " + e.Reason }

func (e *RegionError) Is(target error) bool { return target == ErrInvalidRegion }

// TooSmallError reports one region whose shortest side is under the minimum.
// It stays an ErrInvalidRegion refusal but carries the measured sizes, so a
// caller can word it as a too-small crop instead of malformed coordinates.
type TooSmallError struct {
	Width       int
	Height      int
	MinimumSide int
}

func (e *TooSmallError) Error() string {
	return fmt.Sprintf("listcrop: region is %dx%d pixels; the shortest side must be at least %d",
		e.Width, e.Height, e.MinimumSide)
}

func (e *TooSmallError) Is(target error) bool { return target == ErrInvalidRegion }

// TooLargeError explains one refusal caused by a byte or pixel limit.
type TooLargeError struct {
	Reason string
}

func (e *TooLargeError) Error() string { return "listcrop: " + e.Reason }

func (e *TooLargeError) Is(target error) bool { return target == ErrTooLarge }

// LimitError reports that the source already keeps the maximum number of
// crops. It carries the limit so refusal texts can name it.
type LimitError struct {
	Limit int
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("listcrop: source already keeps the maximum of %d crops", e.Limit)
}

func (e *LimitError) Is(target error) bool { return target == ErrLimit }

// RegionFailure ties a refusal to one region of a batch. Index is 0-based and
// follows the request's region order; Err carries the refusal itself, so
// errors.Is and errors.As keep working through it.
type RegionFailure struct {
	Index int
	Err   error
}

func (e *RegionFailure) Error() string { return e.Err.Error() }

func (e *RegionFailure) Unwrap() error { return e.Err }

// FailureIndex reports the 0-based region a refusal names. found is false for
// a refusal that is not tied to a single region, such as a batch limit.
func FailureIndex(err error) (index int, found bool) {
	var failure *RegionFailure
	if errors.As(err, &failure) {
		return failure.Index, true
	}
	return 0, false
}

// InputAssets uploads and opens one owner's input assets.
type InputAssets interface {
	UploadAsset(context.Context, generation.OwnerID, generation.InputAssetUpload) (generation.InputAsset, error)
	OpenOwned(context.Context, generation.OwnerID, generation.AssetID) (generation.InputAsset, io.ReadCloser, error)
	// LoadInputAssets resolves owned pictures by id. The SQLite adapter also
	// projects a finished Task's outputs, which is how a Task source keeps its
	// task_asset identity before the copy.
	LoadInputAssets(context.Context, generation.OwnerID, []generation.AssetID) (map[generation.AssetID]generation.InputAsset, error)
}

// TaskAssets opens one result of a finished Task for its owner.
type TaskAssets interface {
	OpenAsset(context.Context, generation.OwnerID, generation.TaskID, generation.AssetID) (generation.AssetContent, error)
}

// CropStore persists crop provenance.
type CropStore interface {
	// InsertOrGet inserts one row and returns it; when the unique
	// (owner, source, pixel rect) row already exists it returns that row
	// instead, so parallel calls share one asset.
	InsertOrGet(context.Context, Crop) (Crop, bool, error)
	// Find returns the stored crop of one source rectangle.
	Find(context.Context, generation.OwnerID, generation.AssetID, PixelRect) (Crop, bool, error)
	// CountBySource counts every crop of one source, across calls.
	CountBySource(context.Context, generation.OwnerID, generation.AssetID) (int, error)
	// SourceInputCopy returns the input asset a Task source was copied into
	// by an earlier crop, if any.
	SourceInputCopy(context.Context, generation.OwnerID, generation.AssetID) (generation.AssetID, bool, error)
}

// Config is the policy and the ports of the service. Zero limits fall back to
// the defaults above.
type Config struct {
	Inputs InputAssets
	Tasks  TaskAssets
	Crops  CropStore

	UploadMaxBytes             int64
	GeneratedReferenceMaxBytes int64
	SourceMaxBytes             int64
	SourceMaxPixels            int64
	MinimumSide                int
	MaximumRegionPixels        int64
	MaximumCropsPerSource      int
	SourceAssetType            string
	Now                        func() time.Time
}

// Service implements the batch crop.
type Service struct {
	inputs                     InputAssets
	tasks                      TaskAssets
	crops                      CropStore
	uploadMaxBytes             int64
	generatedReferenceMaxBytes int64
	sourceMaxBytes             int64
	sourceMaxPixels            int64
	minimumSide                int
	maximumRegionPixels        int64
	maximumCropsPerSource      int
	sourceAssetType            string
	now                        func() time.Time
}

// New builds the service.
func New(config Config) (*Service, error) {
	if config.Inputs == nil || config.Tasks == nil || config.Crops == nil {
		return nil, errors.New("listcrop: incomplete dependencies")
	}
	service := &Service{
		inputs:                     config.Inputs,
		tasks:                      config.Tasks,
		crops:                      config.Crops,
		uploadMaxBytes:             config.UploadMaxBytes,
		generatedReferenceMaxBytes: config.GeneratedReferenceMaxBytes,
		sourceMaxBytes:             config.SourceMaxBytes,
		sourceMaxPixels:            config.SourceMaxPixels,
		minimumSide:                config.MinimumSide,
		maximumRegionPixels:        config.MaximumRegionPixels,
		maximumCropsPerSource:      config.MaximumCropsPerSource,
		sourceAssetType:            strings.TrimSpace(config.SourceAssetType),
		now:                        config.Now,
	}
	if service.uploadMaxBytes <= 0 {
		service.uploadMaxBytes = DefaultUploadMaxBytes
	}
	if service.generatedReferenceMaxBytes <= 0 {
		service.generatedReferenceMaxBytes = service.uploadMaxBytes
	}
	if service.sourceMaxBytes <= 0 {
		service.sourceMaxBytes = DefaultSourceMaxBytes
	}
	if service.sourceMaxPixels <= 0 {
		service.sourceMaxPixels = DefaultSourceMaxPixels
	}
	if service.minimumSide <= 0 {
		service.minimumSide = DefaultMinimumSide
	}
	if service.maximumRegionPixels <= 0 {
		service.maximumRegionPixels = DefaultMaximumRegionPixels
	}
	if service.maximumCropsPerSource <= 0 {
		service.maximumCropsPerSource = DefaultMaximumCropsPerSource
	}
	if service.sourceAssetType == "" {
		service.sourceAssetType = DefaultSourceAssetType
	}
	if service.now == nil {
		service.now = time.Now
	}
	return service, nil
}

// CropRegions cuts every region out of the source and returns one result per
// region in request order. Every region is validated before any byte is
// written, so an invalid batch stores nothing. Repeated rectangles return the
// asset stored by the first call, including across parallel calls.
func (service *Service) CropRegions(ctx context.Context, request Request) ([]Result, error) {
	sourceID := generation.AssetID(strings.TrimSpace(string(request.Source)))
	if request.Owner == "" || sourceID == "" {
		return nil, fmt.Errorf("%w: owner and source are required", ErrInvalid)
	}
	if len(request.Regions) == 0 {
		return nil, fmt.Errorf("%w: at least one region is required", ErrInvalid)
	}
	if len(request.Regions) > service.maximumCropsPerSource {
		return nil, &LimitError{Limit: service.maximumCropsPerSource}
	}
	source, err := service.resolveSource(ctx, request.Owner, sourceID)
	if err != nil {
		return nil, err
	}
	rects := make([]PixelRect, len(request.Regions))
	for index, region := range request.Regions {
		rect, err := PixelRectForRegion(region, source.width, source.height)
		if err != nil {
			return nil, &RegionFailure{Index: index, Err: err}
		}
		if err := service.validateRect(rect); err != nil {
			return nil, &RegionFailure{Index: index, Err: err}
		}
		rects[index] = rect
	}
	// Refuse an over-limit batch before storing any of it; the per-region
	// check in cropOne still guards a race between parallel batches.
	count, err := service.crops.CountBySource(ctx, request.Owner, source.assetID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	newRects := make(map[PixelRect]struct{}, len(rects))
	for _, rect := range rects {
		if _, found, err := service.crops.Find(ctx, request.Owner, source.assetID, rect); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
		} else if !found {
			newRects[rect] = struct{}{}
		}
	}
	if count+len(newRects) > service.maximumCropsPerSource {
		return nil, &LimitError{Limit: service.maximumCropsPerSource}
	}
	results := make([]Result, 0, len(request.Regions))
	for index, region := range request.Regions {
		result, err := service.cropOne(ctx, request.Owner, source, region, rects[index])
		if err != nil {
			return nil, &RegionFailure{Index: index, Err: err}
		}
		results = append(results, result)
	}
	return results, nil
}

// PixelRectForRegion converts a normalized region to source pixels. X0 and Y0
// are round(x*width) and round(y*height); X1 and Y1 are round((x+width)*width)
// and round((y+height)*height) and stay exclusive. It checks the normalized
// range but not the crop size policy, which needs Config.
func PixelRectForRegion(region Region, width, height int) (PixelRect, error) {
	if width <= 0 || height <= 0 {
		return PixelRect{}, &RegionError{Reason: "source picture has no pixels"}
	}
	for _, value := range []float64{region.X, region.Y, region.Width, region.Height} {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return PixelRect{}, &RegionError{Reason: "region has a non-finite coordinate"}
		}
	}
	if region.Width <= 0 || region.Height <= 0 {
		return PixelRect{}, &RegionError{Reason: "region has no area"}
	}
	if region.X < 0 || region.Y < 0 || region.X >= 1 || region.Y >= 1 {
		return PixelRect{}, &RegionError{Reason: "region starts outside the picture"}
	}
	if region.X+region.Width > 1+normalizedEpsilon ||
		region.Y+region.Height > 1+normalizedEpsilon {
		return PixelRect{}, &RegionError{Reason: "region extends past the picture"}
	}
	x0 := int(math.Round(region.X * float64(width)))
	y0 := int(math.Round(region.Y * float64(height)))
	x1 := int(math.Round((region.X + region.Width) * float64(width)))
	y1 := int(math.Round((region.Y + region.Height) * float64(height)))
	x0 = clamp(x0, 0, width)
	y0 = clamp(y0, 0, height)
	x1 = clamp(x1, 0, width)
	y1 = clamp(y1, 0, height)
	if x1 <= x0 || y1 <= y0 {
		return PixelRect{}, &RegionError{Reason: "region is smaller than one pixel"}
	}
	return PixelRect{X0: x0, Y0: y0, X1: x1, Y1: y1}, nil
}

func (service *Service) validateRect(rect PixelRect) error {
	width, height := rect.Width(), rect.Height()
	if width < service.minimumSide || height < service.minimumSide {
		return &TooSmallError{Width: width, Height: height, MinimumSide: service.minimumSide}
	}
	if int64(width)*int64(height) > service.maximumRegionPixels {
		return &TooLargeError{Reason: fmt.Sprintf(
			"region is %dx%d pixels, over the %d-pixel limit",
			width, height, service.maximumRegionPixels)}
	}
	return nil
}

// sourcePicture is a decoded source after the Task-copy step.
type sourcePicture struct {
	assetID      generation.AssetID
	kind         string
	inputAssetID generation.AssetID
	width        int
	height       int
	picture      *image.NRGBA
}

func (service *Service) resolveSource(
	ctx context.Context,
	owner generation.OwnerID,
	sourceID generation.AssetID,
) (*sourcePicture, error) {
	loaded, err := service.inputs.LoadInputAssets(ctx, owner, []generation.AssetID{sourceID})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	asset, found := loaded[sourceID]
	if !found || asset.ID != sourceID || asset.OwnerID != owner {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, sourceID)
	}
	mediaType := asset.MediaType
	inputAssetID := sourceID
	kind := SourceKindInput
	var data []byte
	if asset.TaskID != "" {
		copied, body, resolvedMediaType, err := service.taskSourceBytes(ctx, owner, sourceID, asset)
		if err != nil {
			return nil, err
		}
		inputAssetID = copied
		mediaType = resolvedMediaType
		kind = SourceKindTask
		data = body
	} else {
		if !isImageMediaType(mediaType) {
			return nil, fmt.Errorf("%w: %s", ErrInvalidSource, mediaType)
		}
		opened, body, err := service.inputs.OpenOwned(ctx, owner, sourceID)
		if err != nil {
			return nil, service.openError(err, sourceID)
		}
		defer body.Close()
		mediaType = defaultString(opened.MediaType, mediaType)
		data, err = readAtMost(body, service.sourceMaxBytes)
		if err != nil {
			return nil, service.readError(err)
		}
	}
	picture, err := service.decode(data, mediaType)
	if err != nil {
		return nil, err
	}
	bounds := picture.Bounds()
	return &sourcePicture{
		assetID:      sourceID,
		kind:         kind,
		inputAssetID: inputAssetID,
		width:        bounds.Dx(),
		height:       bounds.Dy(),
		picture:      picture,
	}, nil
}

// taskSourceBytes returns the input asset and bytes one Task result is cropped
// from. The copy is remembered through an existing crop row so a repeated call
// crops the same input asset and the unique index keeps its identity; without
// one the result is copied under the generated-reference limit, like
// references.Resolver.uploadGenerated (#276).
func (service *Service) taskSourceBytes(
	ctx context.Context,
	owner generation.OwnerID,
	sourceID generation.AssetID,
	asset generation.InputAsset,
) (generation.AssetID, []byte, string, error) {
	copied, found, err := service.crops.SourceInputCopy(ctx, owner, sourceID)
	if err != nil {
		return "", nil, "", fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if found {
		copiedAsset, body, err := service.inputs.OpenOwned(ctx, owner, copied)
		if err == nil {
			data, readErr := readAtMost(body, service.sourceMaxBytes)
			body.Close()
			if readErr != nil {
				return "", nil, "", service.readError(readErr)
			}
			return copied, data, defaultString(copiedAsset.MediaType, asset.MediaType), nil
		}
		// The remembered copy is gone; fall through and copy the Task result
		// again.
	}
	content, err := service.tasks.OpenAsset(ctx, owner, asset.TaskID, sourceID)
	if err != nil {
		return "", nil, "", service.openError(err, sourceID)
	}
	defer content.Body.Close()
	if content.Size > service.generatedReferenceMaxBytes {
		return "", nil, "", &TooLargeError{Reason: fmt.Sprintf(
			"source picture is %d bytes, over the %d-byte limit",
			content.Size, service.generatedReferenceMaxBytes)}
	}
	mediaType := defaultString(content.MediaType, asset.MediaType)
	if !isImageMediaType(mediaType) {
		return "", nil, "", fmt.Errorf("%w: %s", ErrInvalidSource, mediaType)
	}
	data, err := readAtMost(content.Body, service.generatedReferenceMaxBytes)
	if err != nil {
		return "", nil, "", service.readError(err)
	}
	uploaded, err := service.inputs.UploadAsset(ctx, owner, generation.InputAssetUpload{
		Body:             bytes.NewReader(data),
		OriginalFilename: sourceFilename(mediaType),
		AssetType:        service.sourceAssetType,
		MediaType:        mediaType,
		MaxBytes:         service.generatedReferenceMaxBytes,
	})
	if err != nil {
		return "", nil, "", service.uploadError(err)
	}
	return uploaded.ID, data, defaultString(uploaded.MediaType, mediaType), nil
}

func (service *Service) decode(data []byte, mediaType string) (*image.NRGBA, error) {
	if !isImageMediaType(mediaType) {
		return nil, fmt.Errorf("%w: %s", ErrInvalidSource, mediaType)
	}
	picture, err := imageinspect.Decode(data, imageinspect.Limits{
		MaxBytes:  service.sourceMaxBytes,
		MaxPixels: service.sourceMaxPixels,
	})
	if err != nil {
		if errors.Is(err, imageinspect.ErrTooLarge) {
			return nil, &TooLargeError{Reason: err.Error()}
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidSource, err)
	}
	return picture, nil
}

func (service *Service) cropOne(
	ctx context.Context,
	owner generation.OwnerID,
	source *sourcePicture,
	region Region,
	rect PixelRect,
) (Result, error) {
	stored, found, err := service.crops.Find(ctx, owner, source.assetID, rect)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if found {
		return stored.Result(), nil
	}
	count, err := service.crops.CountBySource(ctx, owner, source.assetID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if count >= service.maximumCropsPerSource {
		return Result{}, &LimitError{Limit: service.maximumCropsPerSource}
	}
	encoded, err := encodePNG(cropPicture(source.picture, rect))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if int64(len(encoded)) > service.uploadMaxBytes {
		return Result{}, &TooLargeError{Reason: fmt.Sprintf(
			"crop is %d bytes, over the %d-byte limit", len(encoded), service.uploadMaxBytes)}
	}
	asset, err := service.inputs.UploadAsset(ctx, owner, generation.InputAssetUpload{
		Body:             bytes.NewReader(encoded),
		OriginalFilename: "crop.png",
		AssetType:        CropAssetType,
		MediaType:        "image/png",
		MaxBytes:         service.uploadMaxBytes,
	})
	if err != nil {
		return Result{}, service.uploadError(err)
	}
	sha := asset.SHA256
	if sha == "" {
		sum := sha256.Sum256(encoded)
		sha = hex.EncodeToString(sum[:])
	}
	crop := Crop{
		AssetID:            asset.ID,
		OwnerID:            owner,
		SourceAssetID:      source.assetID,
		SourceKind:         source.kind,
		SourceInputAssetID: source.inputAssetID,
		SourceWidth:        source.width,
		SourceHeight:       source.height,
		Region:             region,
		PixelRect:          rect,
		SHA256:             sha,
		CreatedAt:          service.now().UTC(),
	}
	stored, _, err = service.crops.InsertOrGet(ctx, crop)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	return stored.Result(), nil
}

func (service *Service) openError(err error, assetID generation.AssetID) error {
	var domain *generation.Error
	if errors.As(err, &domain) {
		switch domain.Code {
		case generation.ErrNotFound, generation.ErrForbidden:
			return fmt.Errorf("%w: %s", ErrNotFound, assetID)
		}
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func (service *Service) readError(err error) error {
	var tooLarge *TooLargeError
	if errors.As(err, &tooLarge) {
		return tooLarge
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// uploadError maps a refused upload to a refusal. The bytes are a valid PNG
// and the byte limit was passed to UploadAsset, so the invalid-input boundary
// is the size refusal.
func (service *Service) uploadError(err error) error {
	var domain *generation.Error
	if errors.As(err, &domain) && domain.Code == generation.ErrInvalidInput {
		return fmt.Errorf("%w: %w", ErrTooLarge, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

func cropPicture(picture *image.NRGBA, rect PixelRect) *image.NRGBA {
	target := image.NewNRGBA(image.Rect(0, 0, rect.Width(), rect.Height()))
	source := image.Pt(rect.X0, rect.Y0)
	draw.Draw(target, target.Bounds(), picture, source, draw.Src)
	return target
}

func encodePNG(picture image.Image) ([]byte, error) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

// readAtMost reads body up to maxBytes and refuses a longer stream, so an
// oversized picture is never fully buffered.
func readAtMost(body io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(body, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, &TooLargeError{Reason: fmt.Sprintf(
			"picture is over the %d-byte limit", maxBytes)}
	}
	return data, nil
}

func isImageMediaType(mediaType string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(mediaType)), "image/")
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func sourceFilename(mediaType string) string {
	switch strings.ToLower(mediaType) {
	case "image/png":
		return "source.png"
	case "image/jpeg":
		return "source.jpg"
	case "image/webp":
		return "source.webp"
	default:
		return "source-image"
	}
}

func clamp(value, low, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
