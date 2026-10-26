package input_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/assets/imagefit"
	inputassets "github.com/LeXwDeX/Plinth/internal/assets/input"
	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

var fitMessages = inputassets.Messages{
	ParameterTooLarge:       "{label} {size} > {limit}",
	CompressionInsufficient: "{label} {size} -> {compressed} > {limit}",
	TransparencyKept:        "{label} alpha {size} -> {compressed} > {limit}",
	TotalTooLarge:           "{label} {count} {size} > {limit}",
	TooManyPixels:           "{label} {width}x{height} > {max_pixels}",
	TooSmall:                "{label} {width}x{height} < {min_width}x{min_height}",
	AspectRatio:             "{label} {width}x{height} ratio > {max_ratio}",
	UnreadableImage:         "{label} unreadable",
}

var fitPolicy = inputassets.Fitting{
	ParameterSemantics: []string{"reference-image"},
	Policy: imagefit.Policy{
		Steps:           []string{imagefit.StepLosslessPNG, imagefit.StepLossy},
		JPEGQualities:   []int{95, 90, 85, 80, 75},
		MaxDecodePixels: 100_000_000,
	},
}

type fitHarness struct {
	t          *testing.T
	repository *sqlite.Repository
	service    *inputassets.Service
}

func newFitHarness(t *testing.T, fitting inputassets.Fitting) fitHarness {
	t.Helper()
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.NewWithConfig(repository, store, inputassets.Config{Messages: fitMessages, Fitting: fitting})
	if err != nil {
		t.Fatal(err)
	}
	return fitHarness{t: t, repository: repository, service: service}
}

// picture is a width×height PNG; noise keeps it large. alpha < 255 makes it
// really transparent.
func picture(t *testing.T, width, height int, alpha uint8, level png.CompressionLevel) []byte {
	t.Helper()
	random := rand.New(rand.NewSource(int64(width*height) + int64(alpha)))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			img.SetNRGBA(x, y, color.NRGBA{R: byte(random.Intn(256)), G: byte(x), B: byte(y), A: alpha})
		}
	}
	var buffer bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: level}).Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func (h fitHarness) upload(body []byte) generation.InputAsset {
	h.t.Helper()
	asset, err := h.service.UploadAsset(h.t.Context(), "owner-a", generation.InputAssetUpload{
		Body: bytes.NewReader(body), OriginalFilename: "source.png", AssetType: "image",
		MediaType: "image/png", MaxBytes: 1 << 30,
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return asset
}

func referenceOffering(parameter generation.Parameter) generation.Offering {
	parameter.Name, parameter.Label, parameter.Semantic = "reference_assets", "参考图", generation.SemanticReferenceImage
	if parameter.Type == "" {
		parameter.Type = "asset-array"
	}
	if parameter.MediaTypes == nil {
		parameter.MediaTypes = []string{"image/png", "image/jpeg"}
	}
	return generation.Offering{ID: "configured/offering", Parameters: []generation.Parameter{parameter}}
}

func referenceInput(ids ...generation.AssetID) json.RawMessage {
	raw, _ := json.Marshal(map[string]any{"reference_assets": ids})
	return raw
}

func (h fitHarness) fitAndResolve(offering generation.Offering, input json.RawMessage) ([]generation.InputAsset, error) {
	fitted, err := h.service.Fit(h.t.Context(), "owner-a", offering, input)
	if err != nil {
		return nil, err
	}
	resolved, err := h.service.Resolve(h.t.Context(), "owner-a", offering, fitted)
	return resolved["reference_assets"], err
}

func (h fitHarness) dimensions(asset generation.InputAsset) (int, int) {
	h.t.Helper()
	body, err := h.service.Open(h.t.Context(), asset)
	if err != nil {
		h.t.Fatal(err)
	}
	defer body.Close()
	config, _, err := image.DecodeConfig(body)
	if err != nil {
		h.t.Fatal(err)
	}
	return config.Width, config.Height
}

func refusalDetail(t *testing.T, err error) string {
	t.Helper()
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrInvalidInput {
		t.Fatalf("error = %#v, want invalid input", err)
	}
	// Schema text is the requester's reason and may be returned over HTTP (#288).
	if !domainErr.UserFacing {
		t.Fatalf("Schema refusal %q not marked user-facing", domainErr.Detail)
	}
	return domainErr.Detail
}

// Without Schema text the historical fallback names the internal parameter; it
// stays diagnostic and is not marked for the requester (#288).
func TestRefusalWithoutSchemaTextIsNotUserFacing(t *testing.T) {
	root := t.TempDir()
	repository, err := sqlite.Open(filepath.Join(root, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	store, err := local.New(filepath.Join(root, "storage"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := inputassets.NewWithConfig(repository, store, inputassets.Config{Fitting: fitPolicy})
	if err != nil {
		t.Fatal(err)
	}
	h := fitHarness{t: t, repository: repository, service: service}
	source := h.upload(picture(t, 300, 200, 255, png.BestCompression))
	constraint := schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 50_000}
	_, err = h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: 1 << 20, ImageInput: &constraint}), referenceInput(source.ID))
	var domainErr *generation.Error
	if !errors.As(err, &domainErr) || domainErr.Code != generation.ErrInvalidInput || domainErr.UserFacing ||
		generation.UserFacingDetail(err) != "" || !strings.Contains(domainErr.Detail, "reference_assets") {
		t.Fatalf("fallback refusal = %#v", err)
	}
}

// Over max_bytes the image is re-encoded into a new asset of the same width
// and height within the limit; the source is kept and recorded, and a repeat
// reuses the same copy.
func TestFitCompressesOverLimitImageKeepingDimensions(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	source := h.upload(picture(t, 320, 200, 255, png.BestCompression))
	limit := source.Size / 2
	offering := referenceOffering(generation.Parameter{MaxBytes: limit})
	sent, err := h.fitAndResolve(offering, referenceInput(source.ID))
	if err != nil || len(sent) != 1 {
		t.Fatalf("sent=%v error=%v", sent, err)
	}
	if sent[0].ID == source.ID || sent[0].Size > limit || sent[0].MediaType != "image/jpeg" ||
		!strings.HasSuffix(sent[0].OriginalFilename, ".jpg") {
		t.Fatalf("sent = %+v, limit %d", sent[0], limit)
	}
	if width, height := h.dimensions(sent[0]); width != 320 || height != 200 {
		t.Fatalf("compressed to %dx%d, want 320x200", width, height)
	}
	derivation, found, err := h.repository.InputAssetDerivation(t.Context(), "owner-a", sent[0].ID)
	if err != nil || !found || derivation.SourceAssetID != source.ID || derivation.SourceSize != source.Size ||
		!strings.HasPrefix(derivation.Method, "jpeg-q") {
		t.Fatalf("derivation = %+v found=%v error=%v", derivation, found, err)
	}
	if original, err := h.repository.GetInputAsset(t.Context(), source.ID); err != nil || original.Size != source.Size {
		t.Fatalf("source changed: %+v %v", original, err)
	}
	again, err := h.fitAndResolve(offering, referenceInput(source.ID))
	if err != nil || again[0].ID != sent[0].ID {
		t.Fatalf("repeat fit = %v %v, want %s", again, err, sent[0].ID)
	}
	within, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: source.Size}), referenceInput(source.ID))
	if err != nil || within[0].ID != source.ID {
		t.Fatalf("image within its limit was re-encoded: %v %v", within, err)
	}
}

// The lossless step comes first: when PNG at best compression fits, pixels
// are kept exactly.
func TestFitPrefersLosslessPNG(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	raw := picture(t, 200, 200, 255, png.NoCompression)
	best := picture(t, 200, 200, 255, png.BestCompression)
	source := h.upload(raw)
	sent, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: int64(len(best))}), referenceInput(source.ID))
	if err != nil || sent[0].MediaType != "image/png" || sent[0].Size > int64(len(best)) {
		t.Fatalf("sent=%+v error=%v", sent, err)
	}
	derivation, _, _ := h.repository.InputAssetDerivation(t.Context(), "owner-a", sent[0].ID)
	if derivation.Method != imagefit.StepLosslessPNG {
		t.Fatalf("method = %q", derivation.Method)
	}
}

// When the lowest declared quality still does not fit, nothing is sent and
// the refusal names the size, the smallest compressed size and the limit.
func TestFitRefusesWhenLowestQualityStillTooLarge(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	source := h.upload(picture(t, 300, 300, 255, png.BestCompression))
	_, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: 1000}), referenceInput(source.ID))
	detail := refusalDetail(t, err)
	if !strings.HasPrefix(detail, "参考图 "+schema.SizeText(source.Size, true)+" -> ") ||
		!strings.HasSuffix(detail, " > "+schema.SizeText(1000, false)) {
		t.Fatalf("detail = %q", detail)
	}
}

// Transparency is never flattened into JPEG.
func TestFitKeepsTransparency(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	raw := picture(t, 160, 160, 100, png.NoCompression)
	best := picture(t, 160, 160, 100, png.BestCompression)
	source := h.upload(raw)
	sent, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: int64(len(best))}), referenceInput(source.ID))
	if err != nil || sent[0].MediaType != "image/png" {
		t.Fatalf("sent=%+v error=%v", sent, err)
	}
	body, _ := h.service.Open(t.Context(), sent[0])
	decoded, _, err := image.Decode(body)
	_ = body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, alpha := decoded.At(5, 5).RGBA(); alpha != 100*257 {
		t.Fatalf("alpha = %#x", alpha)
	}
	// Over the limit after the lossless step, the refusal says transparency is
	// why no lossy step ran, with the size, the smallest size reached and the limit.
	limit := int64(len(best)) / 3
	_, err = h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: limit}), referenceInput(source.ID))
	want := "参考图 alpha " + schema.SizeText(source.Size, true) + " -> " + schema.SizeText(int64(len(best)), true) +
		" > " + schema.SizeText(limit, false)
	if detail := refusalDetail(t, err); detail != want {
		t.Fatalf("detail = %q, want %q", detail, want)
	}
}

// cutout is a realistic alpha image: a fully transparent background around an
// opaque, noisy subject with a soft half-transparent rim.
func cutout(t *testing.T, width, height int) ([]byte, *image.NRGBA) {
	t.Helper()
	random := rand.New(rand.NewSource(271))
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	cx, cy, radius := width/2, height/2, min(width, height)/3
	for y := range height {
		for x := range width {
			d2 := (x-cx)*(x-cx) + (y-cy)*(y-cy)
			var alpha uint8
			switch {
			case d2 <= radius*radius:
				alpha = 255
			case d2 <= (radius+6)*(radius+6):
				alpha = 128
			}
			img.SetNRGBA(x, y, color.NRGBA{R: byte(random.Intn(256)), G: byte(random.Intn(256)), B: byte(x), A: alpha})
		}
	}
	var buffer bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes(), img
}

func flattenOffering(maxBytes int64, keepWhen ...schema.ParameterCondition) generation.Offering {
	offering := referenceOffering(generation.Parameter{MaxBytes: maxBytes, AlphaFlatten: &schema.AlphaFlatten{
		BackgroundColor: "#ffffff", MaxPixels: 16_777_216, KeepWhen: keepWhen,
	}})
	offering.Parameters = append(offering.Parameters, generation.Parameter{Name: "background", Type: "string"})
	return offering
}

// When the upstream flattens transparency anyway (alpha_flatten, no keep_when
// matched), an over-limit alpha image is composited over the declared colour
// first, exactly as the wire would, then compressed as JPEG at the same size.
func TestFitFlattensAlphaWhenUpstreamFlattensIt(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	body, img := cutout(t, 320, 240)
	source := h.upload(body)
	limit := source.Size / 3
	sent, err := h.fitAndResolve(flattenOffering(limit), referenceInput(source.ID))
	if err != nil || len(sent) != 1 || sent[0].ID == source.ID || sent[0].MediaType != "image/jpeg" || sent[0].Size > limit {
		t.Fatalf("sent=%+v error=%v (limit %d)", sent, err, limit)
	}
	if width, height := h.dimensions(sent[0]); width != 320 || height != 240 {
		t.Fatalf("compressed to %dx%d, want 320x240", width, height)
	}
	derivation, found, err := h.repository.InputAssetDerivation(t.Context(), "owner-a", sent[0].ID)
	if err != nil || !found || derivation.SourceAssetID != source.ID || derivation.SourceSize != source.Size ||
		!strings.HasPrefix(derivation.Method, imagefit.FlattenPrefix+"jpeg-q") {
		t.Fatalf("derivation = %+v found=%v error=%v", derivation, found, err)
	}
	opened, err := h.service.Open(t.Context(), sent[0])
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(opened)
	_ = opened.Close()
	if err != nil {
		t.Fatal(err)
	}
	// The transparent corner became the declared white, as on the wire.
	if r, g, b, _ := decoded.At(2, 2).RGBA(); r>>8 < 245 || g>>8 < 245 || b>>8 < 245 {
		t.Fatalf("corner = %d,%d,%d, want white", r>>8, g>>8, b>>8)
	}
	if _, _, _, a := img.At(2, 2).RGBA(); a != 0 {
		t.Fatal("fixture corner is not transparent")
	}
	again, err := h.fitAndResolve(flattenOffering(limit), referenceInput(source.ID))
	if err != nil || again[0].ID != sent[0].ID {
		t.Fatalf("repeat fit = %v %v, want %s", again, err, sent[0].ID)
	}
	// Another owner never sees this owner's derivation record.
	if _, found, err := h.repository.InputAssetDerivation(t.Context(), "owner-b", sent[0].ID); err != nil || found {
		t.Fatalf("other owner found=%v error=%v", found, err)
	}
}

// When the input keeps alpha for the upstream (keep_when matches, e.g. GPT
// Image with background=transparent), or the image is over the flattening
// pixel bound, transparency is kept and the refusal says so with numbers.
func TestFitKeepsAlphaWhenUpstreamTakesIt(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	body, _ := cutout(t, 320, 240)
	source := h.upload(body)
	limit := source.Size / 3
	keep := schema.ParameterCondition{Parameter: "background", Operator: "equals", Value: json.RawMessage(`"transparent"`)}
	input, _ := json.Marshal(map[string]any{"reference_assets": []generation.AssetID{source.ID}, "background": "transparent"})
	_, err := h.fitAndResolve(flattenOffering(limit, keep), input)
	if detail := refusalDetail(t, err); !strings.HasPrefix(detail, "参考图 alpha "+schema.SizeText(source.Size, true)+" -> ") ||
		!strings.HasSuffix(detail, " > "+schema.SizeText(limit, false)) {
		t.Fatalf("detail = %q", detail)
	}
	// background=auto does not match keep_when: the wire flattens, so fitting may too.
	input, _ = json.Marshal(map[string]any{"reference_assets": []generation.AssetID{source.ID}, "background": "auto"})
	if sent, err := h.fitAndResolve(flattenOffering(limit, keep), input); err != nil || sent[0].MediaType != "image/jpeg" {
		t.Fatalf("auto background: %+v %v", sent, err)
	}
	bounded := flattenOffering(limit)
	bounded.Parameters[0].AlphaFlatten.MaxPixels = 320*240 - 1
	_, err = h.fitAndResolve(bounded, referenceInput(source.ID))
	if detail := refusalDetail(t, err); !strings.HasPrefix(detail, "参考图 alpha ") {
		t.Fatalf("over flatten pixels: detail = %q", detail)
	}
}

// An opaque image under alpha_flatten keeps the historical derived asset ID:
// flattening changes nothing, so earlier copies are still reused.
func TestFitOpaqueImageIgnoresFlattenForItsID(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	source := h.upload(picture(t, 320, 200, 255, png.BestCompression))
	limit := source.Size / 2
	plain, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: limit}), referenceInput(source.ID))
	if err != nil {
		t.Fatal(err)
	}
	flattened, err := h.fitAndResolve(flattenOffering(limit), referenceInput(source.ID))
	if err != nil || flattened[0].ID != plain[0].ID {
		t.Fatalf("flatten offering = %v %v, want %s", flattened, err, plain[0].ID)
	}
	derivation, _, _ := h.repository.InputAssetDerivation(t.Context(), "owner-a", plain[0].ID)
	if strings.HasPrefix(derivation.Method, imagefit.FlattenPrefix) {
		t.Fatalf("opaque image flattened: %q", derivation.Method)
	}
}

// Dimension limits are never met by resizing: the image is refused with its
// dimensions and the limit, and no copy is made.
func TestImageInputLimitsRefuseWithoutResizing(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	for _, test := range []struct {
		name       string
		width      int
		height     int
		constraint schema.ImageInputConstraint
		want       string
	}{
		{"pixels", 300, 200, schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 50_000}, "参考图 300x200 > 50000"},
		{"too small", 14, 40, schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 36_000_000}, "参考图 14x40 < 15x15"},
		{"aspect ratio", 340, 20, schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 36_000_000, MaxAspectRatio: 16}, "参考图 340x20 ratio > 16"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := h.upload(picture(t, test.width, test.height, 255, png.BestCompression))
			constraint := test.constraint
			// Also over max_bytes: a copy would not fix the dimensions, so none is made.
			offering := referenceOffering(generation.Parameter{MaxBytes: source.Size - 1, ImageInput: &constraint})
			fitted, err := h.service.Fit(t.Context(), "owner-a", offering, referenceInput(source.ID))
			if err != nil || string(fitted) != string(referenceInput(source.ID)) {
				t.Fatalf("fit = %s %v", fitted, err)
			}
			_, err = h.service.Resolve(t.Context(), "owner-a", offering, fitted)
			if detail := refusalDetail(t, err); detail != test.want {
				t.Fatalf("detail = %q, want %q", detail, test.want)
			}
			// #329 note 99647: the refusal keeps its Schema template and values,
			// so HTTP writes it in the requester's language.
			english := map[string]string{"参考图": "Reference picture"}
			for _, template := range []string{fitMessages.TooManyPixels, fitMessages.TooSmall, fitMessages.AspectRatio} {
				english[template] = "EN " + template
			}
			localized := generation.UserFacingError(err).LocalizedDetail(func(text string) string {
				if translated, ok := english[text]; ok {
					return translated
				}
				return text
			})
			if want := "EN " + strings.Replace(test.want, "参考图", "Reference picture", 1); localized != want {
				t.Fatalf("localized detail = %q, want %q", localized, want)
			}
		})
	}
	ok := h.upload(picture(t, 320, 20, 255, png.BestCompression))
	constraint := schema.ImageInputConstraint{MinWidth: 15, MinHeight: 15, MaxPixels: 36_000_000, MaxAspectRatio: 16}
	if _, err := h.fitAndResolve(referenceOffering(generation.Parameter{MaxBytes: 1 << 20, ImageInput: &constraint}), referenceInput(ok.ID)); err != nil {
		t.Fatalf("16:1 image refused: %v", err)
	}
}

// A total bound after base64 encoding (Gemini's inline request) shares its
// budget among the images; within it nothing changes.
func TestTotalEncodedLimitSharesBudget(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	first := h.upload(picture(t, 200, 200, 255, png.BestCompression))
	second := h.upload(picture(t, 210, 200, 255, png.BestCompression))
	total := schema.Base64EncodedBytes(first.Size) + schema.Base64EncodedBytes(second.Size)
	within := referenceOffering(generation.Parameter{MaxBytes: 1 << 20, MaxTotalEncodedBytes: total})
	sent, err := h.fitAndResolve(within, referenceInput(first.ID, second.ID))
	if err != nil || sent[0].ID != first.ID || sent[1].ID != second.ID {
		t.Fatalf("within total: %v %v", sent, err)
	}
	tight := referenceOffering(generation.Parameter{MaxBytes: 1 << 20, MaxTotalEncodedBytes: total / 2})
	sent, err = h.fitAndResolve(tight, referenceInput(first.ID, second.ID))
	if err != nil || len(sent) != 2 {
		t.Fatalf("tight total: %v %v", sent, err)
	}
	if got := schema.Base64EncodedBytes(sent[0].Size) + schema.Base64EncodedBytes(sent[1].Size); got > total/2 {
		t.Fatalf("encoded total %d over %d", got, total/2)
	}
	if width, height := h.dimensions(sent[1]); width != 210 || height != 200 {
		t.Fatalf("compressed to %dx%d", width, height)
	}
	// Without fitting the same input is refused with the total and the limit.
	plain := newFitHarness(t, inputassets.Fitting{})
	a, b := plain.upload(picture(t, 200, 200, 255, png.BestCompression)), plain.upload(picture(t, 210, 200, 255, png.BestCompression))
	_, err = plain.fitAndResolve(tight, referenceInput(a.ID, b.ID))
	if detail := refusalDetail(t, err); !strings.HasPrefix(detail, "参考图 2 ") {
		t.Fatalf("detail = %q", detail)
	}
}

// Masks are exact pixels and are never compressed; without fitting an image
// over max_bytes is refused with its size and the limit.
func TestNoFittingRefusesWithSizeAndLimit(t *testing.T) {
	h := newFitHarness(t, fitPolicy)
	source := h.upload(picture(t, 100, 100, 255, png.BestCompression))
	mask := generation.Offering{Parameters: []generation.Parameter{{
		Name: "mask", Label: "蒙版", Type: "asset", Semantic: generation.SemanticMask,
		MaxBytes: source.Size - 1, MediaTypes: []string{"image/png"},
	}}}
	input := []byte(`{"mask":"` + string(source.ID) + `"}`)
	fitted, err := h.service.Fit(t.Context(), "owner-a", mask, input)
	if err != nil || string(fitted) != string(input) {
		t.Fatalf("mask fitted: %s %v", fitted, err)
	}
	_, err = h.service.Resolve(t.Context(), "owner-a", mask, fitted)
	want := "蒙版 " + schema.SizeText(source.Size, true) + " > " + schema.SizeText(source.Size-1, false)
	if detail := refusalDetail(t, err); detail != want {
		t.Fatalf("detail = %q, want %q", detail, want)
	}
}
