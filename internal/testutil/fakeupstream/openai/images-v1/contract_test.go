package openaiimagesv1

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// pictureAt decodes the data[index] picture of a success body.
func pictureAt(t *testing.T, body map[string]any, index int) (image.Image, string) {
	t.Helper()
	encoded, _ := testkit.Field(t, body, "data", index, "b64_json").(string)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decoded, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("data[%d] does not decode: %v", index, err)
	}
	return decoded, format
}

func refused(t *testing.T, status int, body map[string]any, code, param string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d %v, want 400 %s on %s", status, body, code, param)
	}
	if got := testkit.Field(t, body, "error", "code"); got != code {
		t.Fatalf("error.code = %v, want %s: %v", got, code, body)
	}
	if got := testkit.Field(t, body, "error", "param"); got != param {
		t.Fatalf("error.param = %v, want %s: %v", got, param, body)
	}
	if got := testkit.Field(t, body, "error", "type"); got != "invalid_request_error" {
		t.Fatalf("error.type = %v, want invalid_request_error", got)
	}
}

// I17: the picture comes back at the requested size, and the response reports
// the size that was drawn.
func TestGenerationAnswersThePictureAtTheRequestedSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, size := range []string{"1024x1024", "1536x1152", "2048x2048", "3840x2160", "1024x640", "auto", ""} {
		request := map[string]any{"model": Model, "prompt": "a cube"}
		want := size
		if size != "" {
			request["size"] = size
		}
		if size == "auto" || size == "" {
			want = autoSize
		}
		status, body := generate(f, request)
		if status != http.StatusOK {
			t.Fatalf("size %q: %d %v", size, status, body)
		}
		var width, height int
		if _, err := fmt.Sscanf(want, "%dx%d", &width, &height); err != nil {
			t.Fatal(err)
		}
		decoded, format := pictureAt(t, body, 0)
		if format != "png" || decoded.Bounds().Dx() != width || decoded.Bounds().Dy() != height {
			t.Fatalf("size %q answered a %s %dx%d picture, want png %s", size, format, decoded.Bounds().Dx(), decoded.Bounds().Dy(), want)
		}
		if body["size"] != want {
			t.Fatalf("size %q: the response reports size %v, want %s", size, body["size"], want)
		}
	}
}

// I18: a size the official gpt-image-2 contract refuses is a 400 on size.
func TestGenerationRefusesSizesTheContractRefuses(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for size, why := range map[string]string{
		"1000x1000": "edges are not multiples of 16",
		"1024x1000": "one edge is not a multiple of 16",
		"4096x2048": "the longest edge exceeds 3840",
		"3840x3840": "the total exceeds 8294400 pixels",
		"3840x1024": "the ratio exceeds 3:1",
		"1024x3600": "the ratio exceeds 3:1",
		"512x512":   "the total is below 655360 pixels",
		"624x1024":  "the total is below 655360 pixels",
		"big":       "not a size",
		"2K":        "not a size",
		"1024*1024": "not a size",
		"0x1024":    "not a size",
	} {
		status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "size": size})
		refused(t, status, body, "invalid_value", "size")
		_ = why
	}
	// The edges of the contract are accepted.
	for _, size := range []string{"1024x1024", "1536x1024", "1024x1536", "3840x2160", "2160x3840", "1440x480", "640x1024", "1280x512"} {
		if status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "size": size}); status != http.StatusOK {
			t.Fatalf("size %s: %d %v", size, status, body)
		}
	}
	if status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "size": 1024}); status != http.StatusBadRequest ||
		testkit.Field(t, body, "error", "code") != "invalid_type" {
		t.Fatalf("a numeric size: %d %v", status, body)
	}
}

func TestGenerationRefusesUnknownEnumValues(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, tc := range []struct{ param, value string }{
		{"quality", "ultra"}, {"quality", "standard"}, {"output_format", "bmp"}, {"output_format", "gif"},
		{"background", "clear"}, {"moderation", "off"},
	} {
		status, body := generate(f, map[string]any{"model": Model, "prompt": "x", tc.param: tc.value})
		refused(t, status, body, "invalid_value", tc.param)
	}
	// Every Catalog option is a documented value.
	for _, tc := range []struct{ param, value string }{
		{"quality", "auto"}, {"quality", "low"}, {"quality", "medium"}, {"quality", "high"}, {"quality", "xhigh"},
		{"output_format", "png"}, {"output_format", "jpeg"}, {"output_format", "webp"},
		{"background", "auto"}, {"background", "opaque"}, {"background", "transparent"},
		{"moderation", "auto"}, {"moderation", "low"},
	} {
		if status, body := generate(f, map[string]any{"model": Model, "prompt": "x", tc.param: tc.value}); status != http.StatusOK {
			t.Fatalf("%s=%s: %d %v", tc.param, tc.value, status, body)
		}
	}
}

// B2 (matrix): the official page limits quality xhigh and max to the 2.5
// snapshots, but the gateway answered 200 for both on 2.6-sunburst and the
// Catalog defaults to max. The fake must not refuse max until a paid real
// answer settles the conflict; this test pins that decision so a stricter fake
// cannot slip in unnoticed.
func TestQualityMaxIsNotRefusedUntilTheRealGatewayDecides(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, model := range []string{Model, "gpt-image-2.5-sunburst"} {
		status, body := generate(f, map[string]any{"model": model, "prompt": "x", "quality": "max"})
		if status != http.StatusOK || body["quality"] != "max" {
			t.Fatalf("%s quality=max: %d %v", model, status, body)
		}
	}
}

// I18: n is 1 to 10 and is never clamped silently.
func TestGenerationRefusesCandidateCountsOutsideOneToTen(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "n": 11})
	refused(t, status, body, "integer_above_max_value", "n")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "n": 0})
	refused(t, status, body, "integer_below_min_value", "n")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "n": -3})
	refused(t, status, body, "integer_below_min_value", "n")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "n": 1.5})
	refused(t, status, body, "invalid_type", "n")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "n": "2"})
	refused(t, status, body, "invalid_type", "n")
	for _, n := range []int{1, 2, 10} {
		status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "n": n})
		if status != http.StatusOK {
			t.Fatalf("n=%d: %d %v", n, status, body)
		}
		if data, _ := body["data"].([]any); len(data) != n {
			t.Fatalf("n=%d answered %d pictures", n, len(data))
		}
	}
}

// I18: output_compression is 0 to 100 and only for jpeg and webp.
func TestGenerationChecksOutputCompression(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "output_format": "jpeg", "output_compression": 101})
	refused(t, status, body, "integer_above_max_value", "output_compression")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "output_format": "webp", "output_compression": -1})
	refused(t, status, body, "integer_below_min_value", "output_compression")
	// The Catalog omits output_compression for png; sending it anyway is the
	// regression this refusal exists to catch (constructed: the real answer is
	// not recorded).
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "output_format": "png", "output_compression": 80})
	refused(t, status, body, "invalid_value", "output_compression")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "output_compression": 80})
	refused(t, status, body, "invalid_value", "output_compression")
	for _, format := range []string{"jpeg", "webp"} {
		if status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "output_format": format, "output_compression": 80}); status != http.StatusOK {
			t.Fatalf("%s with compression: %d %v", format, status, body)
		}
	}
}

// I18/I19: a transparent background cannot be a JPEG.
func TestTransparentBackgroundRefusesJPEG(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "background": "transparent", "output_format": "jpeg"})
	refused(t, status, body, "invalid_value", "background")
	for _, format := range []string{"png", "webp", ""} {
		request := map[string]any{"model": Model, "prompt": "x", "background": "transparent"}
		if format != "" {
			request["output_format"] = format
		}
		if status, body = generate(f, request); status != http.StatusOK {
			t.Fatalf("transparent %s: %d %v", format, status, body)
		}
	}
}

// I19: background=transparent answers a PNG with an alpha channel; the
// response reports what was drawn.
func TestTransparentBackgroundAnswersAnAlphaPNG(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "size": "1024x640", "background": "transparent", "output_format": "png"})
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	decoded, format := pictureAt(t, body, 0)
	if format != "png" || decoded.Bounds().Dx() != 1024 || decoded.Bounds().Dy() != 640 {
		t.Fatalf("picture = %s %v", format, decoded.Bounds())
	}
	if _, _, _, alpha := decoded.At(2, 2).RGBA(); alpha != 0 {
		t.Fatalf("background alpha = %d, want 0", alpha)
	}
	if _, _, _, alpha := decoded.At(512, 320).RGBA(); alpha != 0xffff {
		t.Fatalf("subject alpha = %d, want opaque", alpha)
	}
	if body["background"] != "transparent" || body["output_format"] != "png" {
		t.Fatalf("the response does not report the drawn background and format: %v", body)
	}
	// An opaque request is opaque and reports so.
	status, body = generate(f, map[string]any{"model": Model, "prompt": "x", "background": "auto"})
	if status != http.StatusOK || body["background"] != "opaque" {
		t.Fatalf("auto background: %d %v", status, body)
	}
	opaque, _ := pictureAt(t, body, 0)
	if _, _, _, alpha := opaque.At(2, 2).RGBA(); alpha != 0xffff {
		t.Fatalf("an auto background picture has alpha %d", alpha)
	}
}

// I19: output_format decides the bytes of the picture.
func TestOutputFormatDecidesThePictureBytes(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for format, magic := range map[string]string{"png": "\x89PNG", "jpeg": "\xff\xd8\xff", "webp": "RIFF"} {
		status, body := generate(f, map[string]any{"model": Model, "prompt": "x", "size": "1024x640", "output_format": format})
		if status != http.StatusOK {
			t.Fatalf("%s: %d %v", format, status, body)
		}
		encoded, _ := testkit.Field(t, body, "data", 0, "b64_json").(string)
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || !bytes.HasPrefix(data, []byte(magic)) {
			t.Fatalf("%s picture starts with % x", format, data[:min(len(data), 4)])
		}
		if body["output_format"] != format {
			t.Fatalf("%s: the response reports output_format %v", format, body["output_format"])
		}
		if format == "jpeg" {
			decoded, kind := pictureAt(t, body, 0)
			if kind != "jpeg" || decoded.Bounds().Dx() != 1024 || decoded.Bounds().Dy() != 640 {
				t.Fatalf("jpeg picture = %s %v", kind, decoded.Bounds())
			}
		}
	}
}

func TestEmptyPromptAnswersTheRecordedEmptyStringError(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model})
	refused(t, status, body, "empty_string", "prompt")
	status, body = generate(f, map[string]any{"model": Model, "prompt": "   "})
	refused(t, status, body, "empty_string", "prompt")
}

func TestGenerationRefusesAPromptOverTheLimit(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, body := generate(f, map[string]any{"model": Model, "prompt": string(bytes.Repeat([]byte("a"), maxPromptLength+1))})
	refused(t, status, body, "string_above_max_length", "prompt")
	if status, body = generate(f, map[string]any{"model": Model, "prompt": string(bytes.Repeat([]byte("a"), maxPromptLength))}); status != http.StatusOK {
		t.Fatalf("a prompt at the limit: %d %v", status, body)
	}
}

// editParts builds a multipart edit. pictures are the reference pictures (sent
// as image[]), mask is the optional mask, fields the extra text fields.
func editParts(t *testing.T, pictures [][]byte, mask []byte, fields map[string]string) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("model", Model)
	_ = writer.WriteField("prompt", "edit it")
	for name, value := range fields {
		_ = writer.WriteField(name, value)
	}
	part := func(name string, data []byte) {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+name+`"; filename="`+name+`.png"`)
		header.Set("Content-Type", "image/png")
		file, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(data)
	}
	for _, picture := range pictures {
		part("image[]", picture)
	}
	if mask != nil {
		part("mask", mask)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func solidPNG(t *testing.T, width, height int, fill color.NRGBA) []byte {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, fill)
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// holedMask is an alpha mask whose middle square is transparent (editable).
func holedMask(t *testing.T, width, height int) []byte {
	t.Helper()
	mask := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			if x >= width/4 && x < width*3/4 && y >= height/4 && y < height*3/4 {
				continue
			}
			mask.SetNRGBA(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, mask); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// I18: an edit takes at most 16 pictures.
func TestEditRefusesMoreThanSixteenPictures(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	picture := solidPNG(t, 8, 8, color.NRGBA{R: 200, A: 255})
	many := func(count int) [][]byte {
		out := make([][]byte, count)
		for index := range out {
			out[index] = picture
		}
		return out
	}
	status, body := edit(f, editParts(t, many(17), nil, nil))
	refused(t, status, body, "array_above_max_length", "image")
	if status, body = edit(f, editParts(t, many(16), nil, nil)); status != http.StatusOK {
		t.Fatalf("16 pictures: %d %v", status, body)
	}
}

func TestEditValidatesItsTextFieldsAsStrings(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	picture := solidPNG(t, 8, 8, color.NRGBA{R: 200, A: 255})
	status, body := edit(f, editParts(t, [][]byte{picture}, nil, map[string]string{"n": "11"}))
	refused(t, status, body, "integer_above_max_value", "n")
	status, body = edit(f, editParts(t, [][]byte{picture}, nil, map[string]string{"n": "two"}))
	refused(t, status, body, "invalid_type", "n")
	status, body = edit(f, editParts(t, [][]byte{picture}, nil, map[string]string{"size": "1000x1000"}))
	refused(t, status, body, "invalid_value", "size")
	status, body = edit(f, editParts(t, [][]byte{picture}, nil, map[string]string{"n": "3", "size": "1024x1024", "quality": "low"}))
	if status != http.StatusOK {
		t.Fatalf("a legal edit: %d %v", status, body)
	}
	if data, _ := body["data"].([]any); len(data) != 3 {
		t.Fatalf("n=3 answered %d pictures", len(data))
	}
	decoded, _ := pictureAt(t, body, 0)
	if decoded.Bounds().Dx() != 1024 || decoded.Bounds().Dy() != 1024 {
		t.Fatalf("an unmasked edit answers %v, want 1024x1024", decoded.Bounds())
	}
}

// I18: the mask is a PNG with an alpha channel and the picture's size.
func TestEditRefusesMasksTheContractRefuses(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	picture := solidPNG(t, 32, 32, color.NRGBA{R: 200, G: 40, B: 30, A: 255})
	status, body := edit(f, editParts(t, [][]byte{picture}, holedMask(t, 16, 16), nil))
	refused(t, status, body, "invalid_image", "mask")
	if message := testkit.Field(t, body, "error", "message"); message == nil || !bytes.Contains([]byte(message.(string)), []byte("16x16")) {
		t.Fatalf("the size mismatch is not explained: %v", body)
	}
	// An opaque RGB mask has no alpha channel.
	opaque := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for index := range opaque.Pix {
		opaque.Pix[index] = 255
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, opaque); err != nil {
		t.Fatal(err)
	}
	status, body = edit(f, editParts(t, [][]byte{picture}, buffer.Bytes(), nil))
	refused(t, status, body, "invalid_image", "mask")
	// The mask applies to the first picture: its size is the one that counts.
	other := solidPNG(t, 16, 16, color.NRGBA{G: 200, A: 255})
	if status, body = edit(f, editParts(t, [][]byte{picture, other}, holedMask(t, 32, 32), nil)); status != http.StatusOK {
		t.Fatalf("a mask of the first picture's size: %d %v", status, body)
	}
	status, body = edit(f, editParts(t, [][]byte{other, picture}, holedMask(t, 32, 32), nil))
	refused(t, status, body, "invalid_image", "mask")
}

// I19: a masked edit with n>1 really composes every candidate: the pixels
// outside the mask stay those of the picture, the candidates differ, and the
// size is the picture's, not a placeholder's.
func TestMaskedEditWithSeveralCandidatesComposesEveryOne(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := solidPNG(t, 64, 48, color.NRGBA{R: 200, G: 40, B: 30, A: 255})
	status, body := edit(f, editParts(t, [][]byte{source}, holedMask(t, 64, 48), map[string]string{"n": "3"}))
	if status != http.StatusOK {
		t.Fatalf("masked edit: %d %v", status, body)
	}
	data, _ := body["data"].([]any)
	if len(data) != 3 {
		t.Fatalf("answered %d candidates, want 3", len(data))
	}
	seen := map[string]bool{}
	for index := range data {
		item, _ := data[index].(map[string]any)
		encoded, _ := item["b64_json"].(string)
		if seen[encoded] {
			t.Fatalf("candidate %d repeats an earlier one", index)
		}
		seen[encoded] = true
		decoded, _ := pictureAt(t, body, index)
		if decoded.Bounds().Dx() != 64 || decoded.Bounds().Dy() != 48 {
			t.Fatalf("candidate %d is %v, want the picture's 64x48", index, decoded.Bounds())
		}
		for y := 0; y < 48; y++ {
			for x := 0; x < 64; x++ {
				if x >= 16 && x < 48 && y >= 12 && y < 36 {
					continue
				}
				got := color.NRGBAModel.Convert(decoded.At(x, y)).(color.NRGBA)
				if got.R != 200 || got.G != 40 || got.B != 30 {
					t.Fatalf("candidate %d pixel %d,%d outside the mask changed: %+v", index, x, y, got)
				}
			}
		}
	}
	if body["size"] != "64x48" {
		t.Fatalf("the response reports size %v, want 64x48", body["size"])
	}
}

func TestMaskedEditEncodesTheRequestedFormat(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := solidPNG(t, 64, 48, color.NRGBA{R: 200, G: 40, B: 30, A: 255})
	status, body := edit(f, editParts(t, [][]byte{source}, holedMask(t, 64, 48), map[string]string{"output_format": "jpeg"}))
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	if _, format := pictureAt(t, body, 0); format != "jpeg" {
		t.Fatalf("a masked jpeg edit answered %s", format)
	}
}

// The numbers of the fake's size rule are the Catalog's: both come from the
// official contract, and the schemaimages contract test compares them to the
// shipped Offering. This guards the fake's own copy of the rule.
func TestSizeRuleConstantsAreTheOfficialContract(t *testing.T) {
	if sizeMultiple != 16 || sizeMaxEdge != 3840 || sizeMaxRatio != 3 || sizeMinPixels != 655360 || sizeMaxPixels != 8294400 ||
		maxCandidates != 10 || maxReferences != 16 {
		t.Fatal("the fake's gpt-image-2 limits drifted from the official contract")
	}
}
