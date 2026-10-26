package meshy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"slices"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
	_ "golang.org/x/image/webp"
)

func validateImageInputParameter(p schema.Parameter) error {
	if p.ImageInput == nil {
		return nil
	}
	c := p.ImageInput
	if p.Type != "asset" || p.MaxBytes <= 0 || len(p.MediaTypes) == 0 || c.MinWidth <= 0 || c.MinHeight <= 0 || c.MaxPixels <= 0 || c.MaxPixels/c.MinWidth < c.MinHeight {
		return fmt.Errorf("invalid frozen image_input contract")
	}
	for _, mime := range p.MediaTypes {
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
			return fmt.Errorf("unsupported image_input media type")
		}
	}
	return nil
}

func imageInputFailure(code generation.ErrorCode, cause error) error {
	return &generation.Error{Code: code, Operation: "meshy_image_input", Detail: "图片来源不符合已冻结的输入约束", Cause: cause}
}

var imageInputDiagnostic = errors.New("图片来源不符合已冻结的输入约束")

// inputRefusal prefers the Offering's Schema text for a synchronous input
// check (#358) and keeps the old diagnostic only when the Catalog declares
// none. The original error stays in Cause for logs and the audit.
func inputRefusal(o generation.Offering, reason schema.InputRefusalReason, values schema.InputRefusalValues, code generation.ErrorCode, cause error) error {
	if refusal := o.ExecutionMessages.InputRefusal(reason, values); refusal != nil {
		refusal.Code = code
		refusal.Cause = cause
		return refusal
	}
	return imageInputFailure(code, cause)
}

// Images are already validated by owned asset storage. This checks the exact
// frozen bytes and endpoint dimensions without allocating the full pixel grid.
func (a *Adapter) readImageInput(ctx context.Context, request generation.InputValidationRequest, p schema.Parameter) ([]byte, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if err := validateImageInputParameter(p); err != nil {
		return nil, "", imageInputFailure(generation.ErrUnavailable, err)
	}
	if p.ImageInput == nil || request.OwnerID == "" || a.inputAssets == nil {
		return nil, "", imageInputFailure(generation.ErrInvalidInput, nil)
	}
	var input map[string]json.RawMessage
	var id string
	if json.Unmarshal(request.Input, &input) != nil || json.Unmarshal(input[p.Name], &id) != nil || strings.TrimSpace(id) == "" {
		return nil, "", imageInputFailure(generation.ErrInvalidInput, nil)
	}
	assets := request.InputAssets[p.Name]
	if len(assets) != 1 {
		return nil, "", imageInputFailure(generation.ErrInvalidInput, nil)
	}
	asset := assets[0]
	digest, err := hex.DecodeString(asset.SHA256)
	if err != nil || len(digest) != sha256.Size || asset.ID != generation.AssetID(id) || asset.OwnerID != request.OwnerID || asset.StorageKey == "" || asset.Size <= 0 {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalMismatch, schema.InputRefusalValues{}, generation.ErrInvalidInput, imageInputDiagnostic)
	}
	if asset.Size > p.MaxBytes {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalTooLarge, schema.InputRefusalValues{
			Size: schema.SizeText(asset.Size, true), Limit: schema.SizeText(p.MaxBytes, false),
		}, generation.ErrInvalidInput, imageInputDiagnostic)
	}
	if !slices.Contains(p.MediaTypes, asset.MediaType) {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalMediaType, schema.InputRefusalValues{
			MediaType: asset.MediaType, Formats: strings.Join(p.MediaTypes, ", "),
		}, generation.ErrInvalidInput, imageInputDiagnostic)
	}
	r, err := a.inputAssets.Open(ctx, asset)
	if err != nil {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, err)
	}
	if r == nil {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, nil)
	}
	raw, readErr := io.ReadAll(io.LimitReader(modelContextReader{ctx, r}, asset.Size+1))
	closeErr := r.Close()
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if readErr != nil {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, readErr)
	}
	if closeErr != nil {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalUnavailable, schema.InputRefusalValues{}, generation.ErrUnavailable, closeErr)
	}
	actual := sha256.Sum256(raw)
	if int64(len(raw)) != asset.Size || !bytes.Equal(actual[:], digest) {
		return nil, "", imageInputFailure(generation.ErrConflict, nil)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(raw))
	mime := "image/" + format
	if err != nil || mime != asset.MediaType || !slices.Contains(p.MediaTypes, mime) || config.Width <= 0 || config.Height <= 0 {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalInvalid, schema.InputRefusalValues{}, generation.ErrInvalidInput, err)
	}
	c := p.ImageInput
	width, height := int64(config.Width), int64(config.Height)
	if width < c.MinWidth || height < c.MinHeight || width > c.MaxPixels/height {
		return nil, "", inputRefusal(request.Offering, schema.InputRefusalDimensions, schema.InputRefusalValues{
			Width: width, Height: height, MinWidth: c.MinWidth, MinHeight: c.MinHeight, MaxPixels: c.MaxPixels,
		}, generation.ErrInvalidInput, imageInputDiagnostic)
	}
	return raw, mime, nil
}

func (a *Adapter) imageInputWireValue(ctx context.Context, request generation.StartRequest, p schema.Parameter) (string, error) {
	raw, mime, err := a.readImageInput(ctx, generation.InputValidationRequest{OwnerID: request.OwnerID, Offering: request.Offering, Input: request.Input, InputAssets: request.InputAssets, TaskDependencies: request.TaskDependencies}, p)
	if err != nil {
		return "", &generation.ProviderError{Failure: &generation.Failure{Class: generation.FailureInvalidInput, Retryable: false, SafeDetail: "图片输入与已批准的来源不一致"}, Cause: err}
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw), nil
}
