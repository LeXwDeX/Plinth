package input

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// Messages are the Schema's refusal texts for input images that cannot be
// sent within an Offering's limits (runtime.input_assets.messages, #276).
type Messages struct {
	ParameterTooLarge       string
	CompressionInsufficient string
	// TransparencyKept explains a refusal where the lossy step could not run
	// because the image's transparency must be kept (no alpha_flatten) and
	// JPEG has none.
	TransparencyKept string
	TotalTooLarge    string
	TooManyPixels    string
	TooSmall         string
	AspectRatio      string
	UnreadableImage  string
}

// Fitting is the Schema's image_fitting policy. An empty Steps list disables
// compression; images over a limit are then refused.
type Fitting struct {
	ParameterSemantics []string
	Policy             imagefit.Policy
}

// derivedInputAssets is the repository capability Fit needs to record what a
// compressed image was made from.
type derivedInputAssets interface {
	CreateDerivedInputAsset(context.Context, generation.InputAsset, generation.InputAssetDerivation) error
}

const resolveOperation = "resolve_input_assets"

// refusal is a user-fixable input error whose Detail is the Schema text, or
// the historical fallback when no Schema text is configured. Only the Schema
// text is marked UserFacing, so HTTP returns it as the reason (#288); the
// fallback names the internal parameter and stays diagnostic.
func (s *Service) refusal(template string, fallback string, replacements ...string) error {
	if strings.TrimSpace(template) == "" {
		return &generation.Error{Code: generation.ErrInvalidInput, Operation: resolveOperation, Detail: fallback}
	}
	return &generation.Error{Code: generation.ErrInvalidInput, Operation: resolveOperation,
		Detail: strings.NewReplacer(replacements...).Replace(template), UserFacing: true,
		DetailTemplate: template, DetailValues: replacements}
}

func sizeReplacements(parameter generation.Parameter, size int64, limit int64) []string {
	return []string{
		"{label}", parameterLabel(parameter),
		"{size}", schema.SizeText(size, true),
		"{limit}", schema.SizeText(limit, false),
	}
}

func parameterLabel(parameter generation.Parameter) string {
	if strings.TrimSpace(parameter.Label) != "" {
		return parameter.Label
	}
	return parameter.Name
}

// checkImageInput enforces a parameter's declared image dimensions from the
// image header. Images are never resized to satisfy it.
func (s *Service) checkImageInput(ctx context.Context, parameter generation.Parameter, asset generation.InputAsset) error {
	constraint := parameter.ImageInput
	if constraint == nil {
		return nil
	}
	label := parameterLabel(parameter)
	config, err := s.imageConfig(ctx, asset)
	if err != nil {
		var domainErr *generation.Error
		if errors.As(err, &domainErr) {
			return err
		}
		return s.refusal(s.messages.UnreadableImage, fmt.Sprintf("输入资产参数 %q 的图片无法读取", parameter.Name), "{label}", label)
	}
	width, height := int64(config.Width), int64(config.Height)
	dimensions := []string{"{label}", label, "{width}", strconv.FormatInt(width, 10), "{height}", strconv.FormatInt(height, 10)}
	if width < constraint.MinWidth || height < constraint.MinHeight {
		return s.refusal(s.messages.TooSmall, fmt.Sprintf("输入资产参数 %q 的图片尺寸过小", parameter.Name),
			append(dimensions, "{min_width}", strconv.FormatInt(constraint.MinWidth, 10),
				"{min_height}", strconv.FormatInt(constraint.MinHeight, 10))...)
	}
	if width*height > constraint.MaxPixels {
		return s.refusal(s.messages.TooManyPixels, fmt.Sprintf("输入资产参数 %q 的图片像素过多", parameter.Name),
			append(dimensions, "{max_pixels}", strconv.FormatInt(constraint.MaxPixels, 10))...)
	}
	if ratio := constraint.MaxAspectRatio; ratio > 0 &&
		float64(max(width, height)) > ratio*float64(min(width, height)) {
		return s.refusal(s.messages.AspectRatio, fmt.Sprintf("输入资产参数 %q 的图片宽高比超出范围", parameter.Name),
			append(dimensions, "{max_ratio}", strconv.FormatFloat(ratio, 'f', -1, 64))...)
	}
	return nil
}

// checkTotalEncoded enforces a bound on all images of a parameter together
// after base64 encoding.
func (s *Service) checkTotalEncoded(parameter generation.Parameter, assets []generation.InputAsset) error {
	if parameter.MaxTotalEncodedBytes <= 0 {
		return nil
	}
	total := encodedTotal(assets)
	if total <= parameter.MaxTotalEncodedBytes {
		return nil
	}
	return s.refusal(s.messages.TotalTooLarge, fmt.Sprintf("输入资产参数 %q 的总大小超过上限", parameter.Name),
		append(sizeReplacements(parameter, total, parameter.MaxTotalEncodedBytes),
			"{count}", strconv.Itoa(len(assets)))...)
}

func encodedTotal(assets []generation.InputAsset) int64 {
	var total int64
	for _, asset := range assets {
		total += schema.Base64EncodedBytes(asset.Size)
	}
	return total
}

func (s *Service) imageConfig(ctx context.Context, asset generation.InputAsset) (image.Config, error) {
	body, err := s.Open(ctx, asset)
	if err != nil {
		return image.Config{}, &generation.Error{Code: generation.ErrUnavailable, Operation: resolveOperation,
			Detail: "输入资产暂不可读取", Cause: err}
	}
	defer body.Close()
	config, _, err := image.DecodeConfig(body)
	return config, err
}

// Fit replaces each input image over its parameter's byte limit, or in a
// format the parameter does not accept, with a re-encoded copy of the same
// width and height (#276). The Schema's steps run in order: lossless PNG,
// then JPEG at descending qualities. An image with real transparency becomes
// JPEG only when the parameter's alpha_flatten applies to this input (the
// wire would composite it over the declared colour anyway); it is then
// composited over that colour first. Otherwise its transparency is kept and
// only the lossless step can apply. The copy is a new input asset recording
// its source; the original is kept. An image that cannot fit is refused with
// the sizes reached. Dimension limits are left to Resolve, since no step
// changes them.
func (s *Service) Fit(
	ctx context.Context,
	owner generation.OwnerID,
	offering generation.Offering,
	input json.RawMessage,
) (json.RawMessage, error) {
	derived, ok := s.repository.(derivedInputAssets)
	if len(s.fitting.Policy.Steps) == 0 || !ok {
		return input, nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(input, &values); err != nil {
		return input, nil
	}
	var conditionValues map[string]any
	_ = json.Unmarshal(input, &conditionValues)
	changed := false
	for _, parameter := range offering.Parameters {
		if (parameter.Type != "asset" && parameter.Type != "asset-array") ||
			!slices.Contains(s.fitting.ParameterSemantics, string(parameter.Semantic)) {
			continue
		}
		raw, present := values[parameter.Name]
		if !present {
			continue
		}
		ids, single, ok := assetIDs(raw)
		if !ok || len(ids) == 0 {
			continue // Resolve refuses malformed references.
		}
		stored, err := s.repository.LoadInputAssets(ctx, owner, ids)
		if err != nil {
			return nil, &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
				Detail: "输入资产暂不可读取", Cause: err}
		}
		budget := fitBudget(parameter, ids, stored)
		flatten := flattenColour(parameter, conditionValues)
		replaced := false
		for index, id := range ids {
			asset, exists := stored[id]
			if !exists || (budget <= 0 || asset.Size <= budget) && imagefit.Accepts(parameter.MediaTypes, asset.MediaType) {
				continue
			}
			fitted, err := s.fitAsset(ctx, derived, owner, parameter, asset, budget, flatten)
			if err != nil {
				return nil, err
			}
			if fitted != "" {
				ids[index] = fitted
				replaced = true
			}
		}
		if !replaced {
			continue
		}
		var encoded []byte
		if single {
			encoded, err = json.Marshal(string(ids[0]))
		} else {
			encoded, err = json.Marshal(ids)
		}
		if err != nil {
			return nil, err
		}
		values[parameter.Name] = encoded
		changed = true
	}
	if !changed {
		return input, nil
	}
	return json.Marshal(values)
}

// flattenColour is the parameter's alpha_flatten colour when the wire would
// composite transparent pixels away for this input: alpha_flatten is
// declared, no keep_when condition matches and the colour is valid. Nil
// keeps transparency.
func flattenColour(parameter generation.Parameter, values map[string]any) *color.RGBA {
	policy := parameter.AlphaFlatten
	if policy == nil {
		return nil
	}
	for _, condition := range policy.KeepWhen {
		if schema.ConditionMatches(condition, values) {
			return nil
		}
	}
	background, err := imagefit.ParseHexColor(policy.BackgroundColor)
	if err != nil {
		return nil
	}
	return &background
}

// fitBudget is the byte limit for each image of a parameter: max_bytes, and
// when all images together exceed max_total_encoded_bytes, an equal share of
// it in raw bytes.
func fitBudget(parameter generation.Parameter, ids []generation.AssetID, stored map[generation.AssetID]generation.InputAsset) int64 {
	budget := parameter.MaxBytes
	if parameter.MaxTotalEncodedBytes <= 0 {
		return budget
	}
	assets := make([]generation.InputAsset, 0, len(ids))
	for _, id := range ids {
		if asset, exists := stored[id]; exists {
			assets = append(assets, asset)
		}
	}
	if encodedTotal(assets) <= parameter.MaxTotalEncodedBytes {
		return budget
	}
	share := parameter.MaxTotalEncodedBytes / int64(len(ids)) / 4 * 3
	if budget <= 0 || share < budget {
		return share
	}
	return budget
}

// fitAsset returns the derived asset ID for one image, or "" when the image
// is left for Resolve to refuse (its dimensions break a declared limit, which
// re-encoding cannot change).
func (s *Service) fitAsset(
	ctx context.Context,
	derived derivedInputAssets,
	owner generation.OwnerID,
	parameter generation.Parameter,
	asset generation.InputAsset,
	budget int64,
	flatten *color.RGBA,
) (generation.AssetID, error) {
	if err := s.checkImageInput(ctx, parameter, asset); err != nil {
		return "", nil
	}
	label := parameterLabel(parameter)
	// A copy made without compositing keeps the historical ID; a flattened
	// copy has its own, so the two never stand for each other.
	id := derivedAssetID(owner, asset.ID, budget, parameter.MediaTypes, s.fitting.Policy, nil, 0)
	flattenedID := generation.AssetID("")
	if flatten != nil {
		flattenedID = derivedAssetID(owner, asset.ID, budget, parameter.MediaTypes, s.fitting.Policy, flatten, parameter.AlphaFlatten.MaxPixels)
	}
	for _, candidate := range []generation.AssetID{id, flattenedID} {
		if candidate == "" {
			continue
		}
		if existing, err := s.repository.GetInputAsset(ctx, candidate); err == nil && existing.OwnerID == owner {
			return candidate, nil
		}
	}
	body, err := s.Open(ctx, asset)
	if err != nil {
		return "", &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
			Detail: "输入资产暂不可读取", Cause: err}
	}
	source, err := io.ReadAll(io.LimitReader(body, asset.Size+1))
	_ = body.Close()
	if err != nil {
		return "", &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
			Detail: "输入资产暂不可读取", Cause: err}
	}
	// The wire flattens only within alpha_flatten.max_pixels and refuses a
	// larger image; fitting does not widen that.
	if config, _, err := imagefit.Config(source); flatten != nil &&
		(err != nil || int64(config.Width)*int64(config.Height) > parameter.AlphaFlatten.MaxPixels) {
		flatten = nil
	}
	result, err := imagefit.Fit(source, imagefit.Target{MaxBytes: budget, MediaTypes: parameter.MediaTypes, Flatten: flatten}, s.fitting.Policy)
	var insufficient *imagefit.InsufficientError
	switch {
	case errors.Is(err, imagefit.ErrUnreadable):
		return "", s.refusal(s.messages.UnreadableImage, fmt.Sprintf("输入资产参数 %q 的图片无法读取", parameter.Name), "{label}", label)
	case errors.As(err, &insufficient) && insufficient.Transparent:
		return "", s.refusal(s.messages.TransparencyKept,
			fmt.Sprintf("输入资产参数 %q 带透明通道，无损压缩后仍超过大小上限", parameter.Name),
			append(sizeReplacements(parameter, asset.Size, budget),
				"{compressed}", schema.SizeText(insufficient.Smallest, true))...)
	case errors.As(err, &insufficient):
		return "", s.refusal(s.messages.CompressionInsufficient,
			fmt.Sprintf("输入资产参数 %q 压缩后仍超过大小上限", parameter.Name),
			append(sizeReplacements(parameter, asset.Size, budget),
				"{compressed}", schema.SizeText(insufficient.Smallest, true))...)
	case err != nil:
		return "", &generation.Error{Code: generation.ErrInternal, Operation: "fit_input_assets", Cause: err}
	}
	if strings.HasPrefix(result.Method, imagefit.FlattenPrefix) {
		id = flattenedID
	}
	blob, err := s.blobs.PutBlob(ctx, bytes.NewReader(result.Body), result.MediaType, int64(len(result.Body)))
	if err != nil {
		return "", &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
			Detail: "压缩后的输入资产暂不能保存", Cause: err}
	}
	fitted := generation.InputAsset{
		ID: id, OwnerID: owner, StorageKey: blob.StorageKey,
		OriginalFilename: derivedFilename(asset.OriginalFilename, result.MediaType),
		AssetType:        asset.AssetType, MediaType: blob.MediaType, Size: blob.Size,
		SHA256: blob.SHA256, CreatedAt: s.now().UTC(),
	}
	if err := derived.CreateDerivedInputAsset(ctx, fitted, generation.InputAssetDerivation{
		SourceAssetID: asset.ID, Method: result.Method, SourceSize: asset.Size,
	}); err != nil {
		// A concurrent submission of the same input may have stored it first.
		if existing, getErr := s.repository.GetInputAsset(ctx, id); getErr == nil && existing.OwnerID == owner {
			return id, nil
		}
		return "", &generation.Error{Code: generation.ErrUnavailable, Operation: "fit_input_assets",
			Detail: "压缩后的输入资产暂不能登记", Cause: err}
	}
	return id, nil
}

// derivedAssetID is stable for one owner, source and set of limits, so a
// repeated submission reuses the same copy and keeps its request identity.
// A flattened copy also names its background colour and pixel bound; without
// flattening the digest is the historical imagefit-v1 form, so earlier copies
// keep their IDs.
func derivedAssetID(owner generation.OwnerID, source generation.AssetID, budget int64, mediaTypes []string, policy imagefit.Policy, flatten *color.RGBA, flattenMaxPixels int64) generation.AssetID {
	text := fmt.Sprintf("imagefit-v1\x00%s\x00%s\x00%d\x00%s\x00%s\x00%v\x00%d",
		owner, source, budget, strings.Join(mediaTypes, ","), strings.Join(policy.Steps, ","),
		policy.JPEGQualities, policy.MaxDecodePixels)
	if flatten != nil {
		text += fmt.Sprintf("\x00flatten=#%02x%02x%02x\x00%d", flatten.R, flatten.G, flatten.B, flattenMaxPixels)
	}
	digest := sha256.Sum256([]byte(text))
	return generation.AssetID("asset_" + hex.EncodeToString(digest[:16]))
}

func derivedFilename(original string, mediaType string) string {
	stem := strings.TrimSuffix(path.Base(strings.TrimSpace(original)), path.Ext(strings.TrimSpace(original)))
	if stem == "" || stem == "." || stem == "/" {
		stem = "reference"
	}
	if mediaType == "image/jpeg" {
		return stem + ".jpg"
	}
	return stem + ".png"
}

func assetIDs(raw json.RawMessage) ([]generation.AssetID, bool, bool) {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return []generation.AssetID{generation.AssetID(single)}, true, strings.TrimSpace(single) != ""
	}
	var values []string
	if json.Unmarshal(raw, &values) != nil {
		return nil, false, false
	}
	ids := make([]generation.AssetID, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, false, false
		}
		ids = append(ids, generation.AssetID(value))
	}
	return ids, false, true
}

var _ generation.InputFitter = (*Service)(nil)
