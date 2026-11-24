package openaiimagesv1

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #582: the image-generation guide (checked 2026-11-25) says "The image to
// edit and mask must be of the same format and size". The fake compares the
// formats by the bytes, so the picture's declared Content-Type does not count.

func solidJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, width, height))
	for index := range picture.Pix {
		picture.Pix[index] = 200
	}
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, picture, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// labelledEdit is a multipart edit whose picture part declares contentType,
// whatever its bytes are.
func labelledEdit(t *testing.T, picture []byte, contentType string, mask []byte) testkit.Body {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	_ = writer.WriteField("model", Model)
	_ = writer.WriteField("prompt", "edit it")
	for _, part := range []struct {
		name, kind string
		data       []byte
	}{{"image[]", contentType, picture}, {"mask", "image/png", mask}} {
		header := make(textproto.MIMEHeader)
		header.Set("Content-Disposition", `form-data; name="`+part.name+`"; filename="`+part.name+`.png"`)
		header.Set("Content-Type", part.kind)
		file, err := writer.CreatePart(header)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(part.data)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return testkit.Body{ContentType: writer.FormDataContentType(), Data: buffer.Bytes()}
}

func TestMaskedEditRefusesAPictureOfAnotherFormat(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	mask := holedMask(t, 32, 32)
	// A JPEG picture labelled image/png is still a JPEG.
	status, body := edit(f, labelledEdit(t, solidJPEG(t, 32, 32), "image/png", mask))
	refused(t, status, body, "invalid_image", "mask")
	message, _ := testkit.Field(t, body, "error", "message").(string)
	if !strings.Contains(message, "same format") || !strings.Contains(message, "jpeg") || !strings.Contains(message, "png") {
		t.Fatalf("the format mismatch is not explained: %v", body)
	}
	webp, err := fakeupstream.ImageFile("webp")
	if err != nil {
		t.Fatal(err)
	}
	status, body = edit(f, labelledEdit(t, webp.Data, "image/webp", holedMask(t, 1, 1)))
	refused(t, status, body, "invalid_image", "mask")
	// A PNG picture labelled image/jpeg is a PNG: the edit is taken.
	picture := solidPNG(t, 32, 32, color.NRGBA{R: 200, A: 255})
	if status, body = edit(f, labelledEdit(t, picture, "image/jpeg", mask)); status != http.StatusOK {
		t.Fatalf("a PNG picture with a PNG mask: %d %v", status, body)
	}
	// The format rule is the mask's: an edit without a mask takes a JPEG.
	if status, body = edit(f, editParts(t, [][]byte{solidJPEG(t, 32, 32)}, nil, nil)); status != http.StatusOK {
		t.Fatalf("a JPEG edit without a mask: %d %v", status, body)
	}
}

func editJSON(f *testkit.Fixture, body map[string]any) (int, map[string]any) {
	f.T.Helper()
	status, _, decoded := f.Do("POST", Prefix+"/images/edits", key, body)
	return status, decoded
}

func dataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func reference(data []byte) map[string]any { return map[string]any{"image_url": dataURL(data)} }

// jsonEdit is the JSON body the reference lists for the same edit editParts
// builds as a multipart form.
func jsonEdit(pictures [][]byte, mask []byte, fields map[string]any) map[string]any {
	images := make([]any, len(pictures))
	for index, picture := range pictures {
		images[index] = reference(picture)
	}
	body := map[string]any{"model": Model, "prompt": "edit it", "images": images}
	if mask != nil {
		body["mask"] = reference(mask)
	}
	for name, value := range fields {
		body[name] = value
	}
	return body
}

// The JSON edit answers what the multipart edit answers for the same pictures:
// a masked edit composes the same bytes for every candidate.
func TestJSONEditAnswersWhatTheMultipartEditAnswers(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := solidPNG(t, 64, 48, color.NRGBA{R: 200, G: 40, B: 30, A: 255})
	mask := holedMask(t, 64, 48)
	statusForm, form := edit(f, editParts(t, [][]byte{source}, mask, map[string]string{"n": "2"}))
	statusJSON, viaJSON := editJSON(f, jsonEdit([][]byte{source}, mask, map[string]any{"n": 2}))
	if statusForm != http.StatusOK || statusJSON != http.StatusOK {
		t.Fatalf("multipart %d %v, JSON %d %v", statusForm, form, statusJSON, viaJSON)
	}
	for index := 0; index < 2; index++ {
		if testkit.Field(t, form, "data", index, "b64_json") != testkit.Field(t, viaJSON, "data", index, "b64_json") {
			t.Fatalf("candidate %d differs between the multipart and the JSON edit", index)
		}
	}
	if viaJSON["size"] != "64x48" || viaJSON["output_format"] != "png" {
		t.Fatalf("JSON edit reports size %v format %v", viaJSON["size"], viaJSON["output_format"])
	}
	// An unmasked JSON edit with the model left out (the default applies).
	body := jsonEdit([][]byte{source}, nil, map[string]any{"size": "1024x1024"})
	delete(body, "model")
	status, answer := editJSON(f, body)
	if status != http.StatusOK {
		t.Fatalf("JSON edit without model: %d %v", status, answer)
	}
	if decoded, _ := pictureAt(t, answer, 0); decoded.Bounds().Dx() != 1024 {
		t.Fatalf("JSON edit answers %v", decoded.Bounds())
	}
}

// An image_url may be an http(s) address the fake can fetch.
func TestJSONEditFetchesAnImageURL(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := solidPNG(t, 32, 32, color.NRGBA{G: 200, A: 255})
	mask := holedMask(t, 32, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/source.png":
			_, _ = w.Write(source)
		case "/mask.png":
			_, _ = w.Write(mask)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	body := map[string]any{"model": Model, "prompt": "edit it",
		"images": []any{map[string]any{"image_url": server.URL + "/source.png"}},
		"mask":   map[string]any{"image_url": server.URL + "/mask.png"}}
	status, answer := editJSON(f, body)
	if status != http.StatusOK || answer["size"] != "32x32" {
		t.Fatalf("fetched JSON edit: %d %v", status, answer)
	}
	body["images"] = []any{map[string]any{"image_url": server.URL + "/missing.png"}}
	status, answer = editJSON(f, body)
	refused(t, status, answer, "invalid_value", "images[0].image_url")
}

// Every rule of the multipart edit applies to the JSON edit, and the JSON
// shapes of the reference are checked.
func TestJSONEditRefusals(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	source := solidPNG(t, 32, 32, color.NRGBA{R: 200, A: 255})
	mask := holedMask(t, 32, 32)
	many := make([][]byte, 17)
	for index := range many {
		many[index] = source
	}
	with := func(change func(map[string]any)) map[string]any {
		body := jsonEdit([][]byte{source}, mask, nil)
		change(body)
		return body
	}
	for name, test := range map[string]struct {
		body        map[string]any
		code, param string
	}{
		"no images":             {with(func(b map[string]any) { delete(b, "images") }), "missing_required_parameter", "images"},
		"images is no array":    {with(func(b map[string]any) { b["images"] = reference(source) }), "invalid_type", "images"},
		"empty images":          {with(func(b map[string]any) { b["images"] = []any{} }), "empty_array", "images"},
		"seventeen images":      {jsonEdit(many, nil, nil), "array_above_max_length", "images"},
		"an entry is no object": {with(func(b map[string]any) { b["images"] = []any{dataURL(source)} }), "invalid_type", "images[0]"},
		"an empty entry":        {with(func(b map[string]any) { b["images"] = []any{map[string]any{}} }), "missing_required_parameter", "images[0]"},
		"a file_id":             {with(func(b map[string]any) { b["images"] = []any{map[string]any{"file_id": "file-abc"}} }), "invalid_value", "images[0].file_id"},
		"not a data URL":        {with(func(b map[string]any) { b["images"] = []any{map[string]any{"image_url": "data:image/png,raw"}} }), "invalid_value", "images[0].image_url"},
		"not a URL":             {with(func(b map[string]any) { b["images"] = []any{map[string]any{"image_url": "ftp://host/a.png"}} }), "invalid_value", "images[0].image_url"},
		"mask with both":        {with(func(b map[string]any) { b["mask"] = map[string]any{"image_url": dataURL(mask), "file_id": "file-abc"} }), "invalid_value", "mask"},
		"mask with neither":     {with(func(b map[string]any) { b["mask"] = map[string]any{} }), "missing_required_parameter", "mask"},
		"mask file_id":          {with(func(b map[string]any) { b["mask"] = map[string]any{"file_id": "file-abc"} }), "invalid_value", "mask.file_id"},
		"mask of another size":  {jsonEdit([][]byte{source}, holedMask(t, 16, 16), nil), "invalid_image", "mask"},
		"a JPEG picture":        {jsonEdit([][]byte{solidJPEG(t, 32, 32)}, mask, nil), "invalid_image", "mask"},
		"n as a string":         {jsonEdit([][]byte{source}, nil, map[string]any{"n": "2"}), "invalid_type", "n"},
		"n above ten":           {jsonEdit([][]byte{source}, nil, map[string]any{"n": 11}), "integer_above_max_value", "n"},
		"a size off the grid":   {jsonEdit([][]byte{source}, nil, map[string]any{"size": "1000x1000"}), "invalid_value", "size"},
		"an unknown quality":    {jsonEdit([][]byte{source}, nil, map[string]any{"quality": "ultra"}), "invalid_value", "quality"},
		"no prompt":             {with(func(b map[string]any) { delete(b, "prompt") }), "missing_required_parameter", "prompt"},
	} {
		status, body := editJSON(f, test.body)
		if status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != test.code || testkit.Field(t, body, "error", "param") != test.param {
			t.Errorf("%s: %d %v, want 400 %s on %s", name, status, body, test.code, test.param)
		}
	}
	// Sixteen entries are taken.
	if status, body := editJSON(f, jsonEdit(many[:16], nil, nil)); status != http.StatusOK {
		t.Fatalf("16 JSON images: %d %v", status, body)
	}
	// A JSON body that does not parse.
	status, body := edit(f, testkit.Body{ContentType: "application/json", Data: []byte(`{"prompt":`)})
	object, _ := body["error"].(map[string]any)
	if status != http.StatusBadRequest || object["code"] != nil || object["param"] != nil ||
		!strings.Contains(object["message"].(string), "could not parse the JSON body") {
		t.Fatalf("unparsable JSON: %d %v", status, body)
	}
}

// The 50MB limit applies to a JSON picture as to a multipart one.
func TestJSONEditRefusesAMaskOf50MB(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	big := bytes.Repeat([]byte{0}, maxEditFileBytes)
	status, body := editJSON(f, jsonEdit([][]byte{solidPNG(t, 8, 8, color.NRGBA{A: 255})}, big, nil))
	refused(t, status, body, "image_file_too_large", "mask")
}
