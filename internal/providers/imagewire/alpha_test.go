package imagewire_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/imagewire"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// boardPNG is what the board uploads (#271): a red bitmap on the left, empty (alpha 0) canvas on
// the right, and one half-transparent pixel.
func boardPNG(t *testing.T, opaque bool) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			switch {
			case opaque || x < 2:
				picture.SetNRGBA(x, y, color.NRGBA{R: 0xff, A: 0xff})
			case x == 2 && y == 0:
				picture.SetNRGBA(x, y, color.NRGBA{B: 0xff, A: 0x80})
			default:
				picture.SetNRGBA(x, y, color.NRGBA{})
			}
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func jsonImageOffering(flatten *schema.AlphaFlatten) generation.Offering {
	encoded, _ := json.Marshal(generation.ImageProtocol{RequestTemplate: json.RawMessage(`{"model":"$model"}`)})
	return generation.Offering{
		ID: "configured/json", ModelID: "configured-model", ProviderOptions: encoded,
		Parameters: []generation.Parameter{
			{Name: "prompt", Type: "text"},
			{Name: "reference_assets", Type: "asset-array", WireName: "image", MaxBytes: 1 << 20, MediaTypes: []string{"image/*"}, AlphaFlatten: flatten},
		},
	}
}

func buildReference(t *testing.T, flatten *schema.AlphaFlatten, data []byte) ([]byte, string, error) {
	t.Helper()
	body, err := imagewire.Build(context.Background(), generation.StartRequest{
		Offering:    jsonImageOffering(flatten),
		Input:       json.RawMessage(`{"prompt":"p","reference_assets":["a"]}`),
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "a", MediaType: "image/png", OriginalFilename: "canvas.png"}}},
	}, &fixtureAssets{data: data})
	if err != nil {
		return nil, "", err
	}
	uri := body["image"].([]any)[0].(string)
	mediaType, encoded, _ := strings.Cut(strings.TrimPrefix(uri, "data:"), ";base64,")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return decoded, mediaType, nil
}

func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	picture, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	result := image.NewNRGBA(picture.Bounds())
	for y := picture.Bounds().Min.Y; y < picture.Bounds().Max.Y; y++ {
		for x := picture.Bounds().Min.X; x < picture.Bounds().Max.X; x++ {
			result.Set(x, y, picture.At(x, y))
		}
	}
	return result
}

func TestAlphaKeptWithoutDeclaration(t *testing.T) {
	source := boardPNG(t, false)
	sent, mediaType, err := buildReference(t, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sent, source) || mediaType != "image/png" {
		t.Fatalf("an Offering without alpha_flatten must receive the stored bytes, alpha included")
	}
	if decodeNRGBA(t, sent).NRGBAAt(3, 1).A != 0 {
		t.Fatalf("empty canvas must stay transparent")
	}
}

func TestAlphaFlattenCompositesOverDeclaredColour(t *testing.T) {
	sent, mediaType, err := buildReference(t, &schema.AlphaFlatten{BackgroundColor: "#ffffff", MaxPixels: 64}, boardPNG(t, false))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "image/png" {
		t.Fatalf("media type = %q", mediaType)
	}
	flat := decodeNRGBA(t, sent)
	if !flat.Opaque() {
		t.Fatalf("a flattened source must be fully opaque")
	}
	if got := flat.NRGBAAt(0, 0); got != (color.NRGBA{R: 0xff, A: 0xff}) {
		t.Fatalf("bitmap pixel = %#v, want the original red", got)
	}
	if got := flat.NRGBAAt(3, 1); got != (color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		t.Fatalf("empty canvas = %#v, want the declared white", got)
	}
	// Half-transparent blue over white: blended, not premultiplied to black.
	if got := flat.NRGBAAt(2, 0); got.B != 0xff || got.R < 0x7e || got.R > 0x80 || got.G != got.R {
		t.Fatalf("half-transparent pixel = %#v, want blue blended with white", got)
	}
}

func TestAlphaFlattenKeepsOpaqueBytesAndBoundsPixels(t *testing.T) {
	opaque := boardPNG(t, true)
	sent, _, err := buildReference(t, &schema.AlphaFlatten{BackgroundColor: "#ffffff", MaxPixels: 64}, opaque)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sent, opaque) {
		t.Fatalf("an opaque source must keep its stored bytes")
	}
	if _, _, err := buildReference(t, &schema.AlphaFlatten{BackgroundColor: "#ffffff", MaxPixels: 7}, boardPNG(t, false)); err == nil || !strings.Contains(err.Error(), "像素") {
		t.Fatalf("a source above max_pixels must fail before decoding, got %v", err)
	}
	if _, _, err := buildReference(t, &schema.AlphaFlatten{BackgroundColor: "#ffffff", MaxPixels: 64}, []byte("not an image")); err == nil {
		t.Fatalf("an undecodable source must not be sent unflattened")
	}
}

func TestAlphaFlattenMultipartRenamesThePart(t *testing.T) {
	offering := multipartOffering()
	for index := range offering.Parameters {
		if offering.Parameters[index].Name == "reference_assets" {
			offering.Parameters[index].MaxBytes = 1 << 20
			offering.Parameters[index].MediaTypes = []string{"image/png", "image/webp"}
			offering.Parameters[index].AlphaFlatten = &schema.AlphaFlatten{BackgroundColor: "#000000", MaxPixels: 64}
		}
	}
	reader, contentType, err := imagewire.EncodeMultipart(context.Background(), generation.StartRequest{
		Offering:    offering,
		Input:       json.RawMessage(`{"prompt":"p","reference_assets":["a"]}`),
		InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "a", MediaType: "image/webp", OriginalFilename: "board.webp"}}},
	}, &fixtureAssets{data: boardPNG(t, false)})
	if err != nil {
		t.Fatal(err)
	}
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		t.Fatal(err)
	}
	form := multipart.NewReader(reader, params["boundary"])
	for {
		part, err := form.NextPart()
		if err == io.EOF {
			t.Fatal("no image part")
		}
		if err != nil {
			t.Fatal(err)
		}
		if part.FormName() != "image[]" {
			continue
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "board.png" || part.Header.Get("Content-Type") != "image/png" {
			t.Fatalf("part = %q %q", part.FileName(), part.Header.Get("Content-Type"))
		}
		flat := decodeNRGBA(t, data)
		if !flat.Opaque() || flat.NRGBAAt(3, 1) != (color.NRGBA{A: 0xff}) {
			t.Fatalf("empty canvas = %#v, want the declared black", flat.NRGBAAt(3, 1))
		}
		return
	}
}

// GPT Image draws transparent input as black unless the output is asked to be transparent: the source
// is flattened for background=auto/opaque and keeps its alpha for background=transparent.
func TestAlphaFlattenKeepWhenFollowsTheNormalizedInput(t *testing.T) {
	offering := jsonImageOffering(&schema.AlphaFlatten{
		BackgroundColor: "#ffffff", MaxPixels: 64,
		KeepWhen: []schema.ParameterCondition{{Parameter: "background", Operator: "equals", Value: json.RawMessage(`"transparent"`)}},
	})
	offering.Parameters = append(offering.Parameters, generation.Parameter{Name: "background", Type: "select"})
	source := boardPNG(t, false)
	for background, keeps := range map[string]bool{"transparent": true, "auto": false, "opaque": false} {
		body, err := imagewire.Build(context.Background(), generation.StartRequest{
			Offering:    offering,
			Input:       json.RawMessage(`{"prompt":"p","reference_assets":["a"],"background":"` + background + `"}`),
			InputAssets: map[string][]generation.InputAsset{"reference_assets": {{ID: "a", MediaType: "image/png"}}},
		}, &fixtureAssets{data: source})
		if err != nil {
			t.Fatal(err)
		}
		uri := body["image"].([]any)[0].(string)
		sent, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(uri, "data:image/png;base64,"))
		if err != nil {
			t.Fatal(err)
		}
		if body["background"] != background {
			t.Fatalf("background = %v, want %s", body["background"], background)
		}
		if keeps != bytes.Equal(sent, source) || keeps == decodeNRGBA(t, sent).Opaque() {
			t.Fatalf("background=%s: kept alpha = %v, want %v", background, bytes.Equal(sent, source), keeps)
		}
	}
}
