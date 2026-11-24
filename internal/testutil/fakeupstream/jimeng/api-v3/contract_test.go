package jimengapiv3

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg"
	"image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

func pictureConfig(t *testing.T, body map[string]any, index int) image.Config {
	t.Helper()
	encoded, _ := testkit.Field(t, body, "data", index, "b64_json").(string)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || format != "jpeg" {
		t.Fatalf("data[%d] is not a decodable JPEG: %s %v", index, format, err)
	}
	return config
}

func refusedParam(t *testing.T, status int, body map[string]any, param string) {
	t.Helper()
	if status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != "InvalidParameter" ||
		testkit.Field(t, body, "error", "type") != "BadRequest" || testkit.Field(t, body, "error", "param") != param {
		t.Fatalf("want 400 InvalidParameter on %s, got %d %v", param, status, body)
	}
	if message, _ := testkit.Field(t, body, "error", "message").(string); !strings.Contains(message, "`"+param+"`") {
		t.Fatalf("the message does not name the parameter %s: %q", param, message)
	}
}

// dataURI is a real PNG of the given size as a data URI.
func dataURI(t *testing.T, width, height int) string {
	t.Helper()
	picture := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			picture.SetNRGBA(x, y, color.NRGBA{R: 120, G: 80, B: 200, A: 255})
		}
	}
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, picture); err != nil {
		t.Fatal(err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buffer.Bytes())
}

func references(t *testing.T, count int) []string {
	t.Helper()
	one := dataURI(t, 32, 32)
	out := make([]string, count)
	for index := range out {
		out[index] = one
	}
	return out
}

// I17: the picture has the size the request names, and the answer reports it.
func TestPictureHasTheRequestedSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for size, want := range map[string][2]int{
		"2048x2048": {2048, 2048}, "2560x1440": {2560, 1440}, "4096x4096": {4096, 4096}, "5504x3040": {5504, 3040},
		"3750x1250": {3750, 1250}, "2K": {2048, 2048}, "4K": {4096, 4096}, "": {2048, 2048},
	} {
		request := body("a cube", "b64_json", nil)
		if size == "" {
			delete(request, "size")
		} else {
			request["size"] = size
		}
		status, decoded := generateRequest(f, request)
		if status != http.StatusOK {
			t.Fatalf("size %q: %d %v", size, status, decoded)
		}
		config := pictureConfig(t, decoded, 0)
		if config.Width != want[0] || config.Height != want[1] {
			t.Fatalf("size %q answered %dx%d, want %dx%d", size, config.Width, config.Height, want[0], want[1])
		}
		if got := testkit.Field(t, decoded, "data", 0, "size"); got != fmt.Sprintf("%dx%d", want[0], want[1]) {
			t.Fatalf("size %q: data[0].size = %v", size, got)
		}
	}
	// A url answer serves the same size.
	request := body("a cube", "url", map[string]any{"size": "2560x1440"})
	status, decoded := generateRequest(f, request)
	if status != http.StatusOK {
		t.Fatalf("url: %d %v", status, decoded)
	}
	code, _, data := f.Download(testkit.Field(t, decoded, "data", 0, "url").(string))
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if code != http.StatusOK || err != nil || config.Width != 2560 || config.Height != 1440 {
		t.Fatalf("the downloaded picture is %dx%d (%d, %v), want 2560x1440", config.Width, config.Height, code, err)
	}
}

// The fake's old 1x1 JPEG was below Plinth's own floor for a reference picture
// (the Offering declares image_input min 15x15, enforced by checkImageInput), so
// a Jimeng picture fed back as the next Jimeng reference was refused locally
// even though the fake served it. A picture at the real size passes the
// upstream's reference rules too.
func TestAPictureCanBeUsedAsTheNextReference(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, decoded := generateRequest(f, body("a cube", "b64_json", nil))
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, decoded)
	}
	reference := "data:image/jpeg;base64," + testkit.Field(t, decoded, "data", 0, "b64_json").(string)
	status, decoded = generateRequest(f, body("make it blue", "b64_json", map[string]any{"image": []string{reference}}))
	if status != http.StatusOK {
		t.Fatalf("a Jimeng picture as a reference: %d %v", status, decoded)
	}
}

// I18 (constructed envelope): a size outside the documented bounds is a 400.
func TestSizeOutsideTheDocumentedBoundsIsRefused(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for size, why := range map[string]string{
		"1024x1024": "total below 3686400",
		"2048x1024": "total below 3686400",
		"1500x1500": "the documented invalid example",
		"4097x4096": "total above 16777216",
		"8192x4096": "total above 16777216",
		"9000x500":  "ratio above 16",
		"1x4000":    "ratio above 16",
		"big":       "not a size",
		"1K":        "not a resolution word of this model",
		"2k":        "the word is upper-case",
		"0x2048":    "not a size",
		"2048*2048": "not a size",
	} {
		status, decoded := generateRequest(f, body("a cube", "b64_json", map[string]any{"size": size}))
		refusedParam(t, status, decoded, "size")
		_ = why
	}
	// The bounds themselves are accepted: the minimum total, the maximum total,
	// and a ratio of exactly 16.
	for _, size := range []string{"2560x1440", "4096x4096", "1920x1920", "8192x512"} {
		if status, decoded := generateRequest(f, body("a cube", "b64_json", map[string]any{"size": size})); status != http.StatusOK {
			t.Fatalf("size %s: %d %v", size, status, decoded)
		}
	}
}

// #579: the official page (checked 2026-11-25) gives Seedream 4.0 a lower
// size floor than 4.5 (921600 pixels, 1280x720) and the word 1K. The fake
// picks the rules by the requested model, so a 4.0 request for 1024x1024 is
// drawn while the same size for 4.5 stays refused.
func TestSizeBoundsFollowTheRequestedModel(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	const seedream40 = "doubao-seedream-4-0-250828"
	for size, want := range map[string][2]int{
		"1024x1024": {1024, 1024},
		"1280x720":  {1280, 720},
		"1600x600":  {1600, 600},
		"1K":        {1024, 1024},
		"2K":        {2048, 2048},
		"4K":        {4096, 4096},
		"4096x4096": {4096, 4096},
	} {
		request := body("a cube", "b64_json", map[string]any{"model": seedream40, "size": size})
		status, decoded := generateRequest(f, request)
		if status != http.StatusOK {
			t.Fatalf("4.0 size %s: %d %v", size, status, decoded)
		}
		if got := testkit.Field(t, decoded, "data", 0, "size"); got != fmt.Sprintf("%dx%d", want[0], want[1]) {
			t.Fatalf("4.0 size %s answered %v", size, got)
		}
	}
	for _, size := range []string{"800x800", "1279x720", "4097x4096", "9000x500", "1k"} {
		request := body("a cube", "b64_json", map[string]any{"model": seedream40, "size": size})
		status, decoded := generateRequest(f, request)
		refusedParam(t, status, decoded, "size")
	}
	// The 4.5 Offering the Catalog sends keeps its higher floor and no 1K.
	for _, size := range []string{"1024x1024", "1K"} {
		status, decoded := generateRequest(f, body("a cube", "b64_json", map[string]any{"size": size}))
		refusedParam(t, status, decoded, "size")
	}
	// Both models accept at most 14 reference pictures.
	for _, model := range []string{seedream40, Model} {
		status, decoded := generateRequest(f, body("blend", "b64_json", map[string]any{"model": model, "image": references(t, 15)}))
		refusedParam(t, status, decoded, "image")
		if status, decoded = generateRequest(f, body("blend", "b64_json", map[string]any{"model": model, "image": references(t, 14)})); status != http.StatusOK {
			t.Fatalf("%s with 14 references: %d %v", model, status, decoded)
		}
	}
}

func TestEnumValuesAreChecked(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, decoded := generateRequest(f, body("a cube", "base64", nil))
	refusedParam(t, status, decoded, "response_format")
	status, decoded = generateRequest(f, body("a cube", "b64_json", map[string]any{"sequential_image_generation": "always"}))
	refusedParam(t, status, decoded, "sequential_image_generation")
	for _, format := range []string{"url", "b64_json", ""} {
		request := body("a cube", format, nil)
		if format == "" {
			delete(request, "response_format")
		}
		if status, decoded = generateRequest(f, request); status != http.StatusOK {
			t.Fatalf("response_format %q: %d %v", format, status, decoded)
		}
	}
}

// I18: max_images is 1 to 15 and is never clamped silently.
func TestMaxImagesOutsideTheRangeIsRefused(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	const param = "sequential_image_generation_options.max_images"
	for _, value := range []int{0, -1, 16, 99} {
		request := body("a cube", "b64_json", map[string]any{
			"sequential_image_generation":         "auto",
			"sequential_image_generation_options": map[string]any{"max_images": value},
		})
		status, decoded := generateRequest(f, request)
		refusedParam(t, status, decoded, param)
	}
	// Without max_images the documented default applies.
	if status, decoded := generateRequest(f, sequential("a cube", "b64_json", 0)); status != http.StatusOK {
		t.Fatalf("no max_images: %d %v", status, decoded)
	}
	// max_images is for group generation only: a disabled request ignores it.
	request := body("a cube", "b64_json", map[string]any{"sequential_image_generation_options": map[string]any{"max_images": 99}})
	if status, decoded := generateRequest(f, request); status != http.StatusOK {
		t.Fatalf("disabled with max_images 99: %d %v", status, decoded)
	}
}

// I18: at most 14 reference pictures, and references plus generated pictures
// total at most 15.
func TestReferenceImageCountsFollowTheDocumentedLimits(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, decoded := generateRequest(f, body("blend", "b64_json", map[string]any{"image": references(t, 14)}))
	if status != http.StatusOK {
		t.Fatalf("14 references: %d %v", status, decoded)
	}
	status, decoded = generateRequest(f, body("blend", "b64_json", map[string]any{"image": references(t, 15)}))
	refusedParam(t, status, decoded, "image")
	// 14 references leave room for one generated picture.
	request := sequential("blend", "b64_json", 2)
	request["image"] = references(t, 14)
	status, decoded = generateRequest(f, request)
	refusedParam(t, status, decoded, "sequential_image_generation_options.max_images")
	request = sequential("blend", "b64_json", 1)
	request["image"] = references(t, 14)
	if status, decoded = generateRequest(f, request); status != http.StatusOK {
		t.Fatalf("14 references and 1 generated: %d %v", status, decoded)
	}
	request = sequential("blend", "b64_json", 13)
	request["image"] = references(t, 2)
	if status, decoded = generateRequest(f, request); status != http.StatusOK {
		t.Fatalf("2 references and 13 generated: %d %v", status, decoded)
	}
	request = sequential("blend", "b64_json", 14)
	request["image"] = references(t, 2)
	status, decoded = generateRequest(f, request)
	refusedParam(t, status, decoded, "sequential_image_generation_options.max_images")
	// Without max_images the documented default (15) shrinks to the room left.
	request = sequential("blend", "b64_json", 0)
	request["image"] = references(t, 3)
	status, decoded = generateRequest(f, request)
	if items, _ := decoded["data"].([]any); status != http.StatusOK || len(items) != 12 {
		t.Fatalf("3 references and the default max_images answered %d pictures (%d), want 12", len(items), status)
	}
	// A single picture goes as a plain string too.
	if status, decoded = generateRequest(f, body("blend", "b64_json", map[string]any{"image": references(t, 1)[0]})); status != http.StatusOK {
		t.Fatalf("a single string image: %d %v", status, decoded)
	}
}

// I18: the documented limits of one reference picture.
func TestReferenceImageDimensionsFollowTheDocumentedLimits(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	send := func(image ...string) (int, map[string]any) {
		return generateRequest(f, body("blend", "b64_json", map[string]any{"image": image}))
	}
	// Each side must be greater than 14 pixels: 15 is the floor.
	if status, decoded := send(dataURI(t, 15, 15)); status != http.StatusOK {
		t.Fatalf("15x15: %d %v", status, decoded)
	}
	for _, size := range [][2]int{{14, 14}, {14, 200}, {200, 14}, {1, 1}} {
		status, decoded := send(dataURI(t, size[0], size[1]))
		refusedParam(t, status, decoded, "image")
	}
	// Aspect ratio 1/16 to 16.
	if status, decoded := send(dataURI(t, 320, 20)); status != http.StatusOK {
		t.Fatalf("ratio 16: %d %v", status, decoded)
	}
	status, decoded := send(dataURI(t, 340, 20))
	refusedParam(t, status, decoded, "image")
	// Only a URL or a data URI.
	status, decoded = send("AAAA")
	refusedParam(t, status, decoded, "image")
	status, decoded = send("data:image/png;base64,@@@@")
	refusedParam(t, status, decoded, "image")
	status, decoded = send("data:image/png;base64,iVBORw0KGgo=")
	refusedParam(t, status, decoded, "image")
	if status, decoded = send("https://example.test/picture.png"); status != http.StatusOK {
		t.Fatalf("a URL: %d %v", status, decoded)
	}
	status, decoded = generateRequest(f, body("blend", "b64_json", map[string]any{"image": 7}))
	refusedParam(t, status, decoded, "image")
}

// A sequential answer carries distinct pictures at the requested size.
func TestSequentialPicturesAreDistinctAndSized(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	request := sequential("a cube", "b64_json", 3)
	request["size"] = "2560x1440"
	status, decoded := generateRequest(f, request)
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, decoded)
	}
	seen := map[string]bool{}
	for index := 0; index < 3; index++ {
		config := pictureConfig(t, decoded, index)
		if config.Width != 2560 || config.Height != 1440 {
			t.Fatalf("picture %d is %dx%d", index, config.Width, config.Height)
		}
		encoded := testkit.Field(t, decoded, "data", index, "b64_json").(string)
		if seen[encoded] {
			t.Fatalf("picture %d repeats an earlier one", index)
		}
		seen[encoded] = true
	}
}

// m7: a group answer may carry fewer pictures than max_images, and a rate-limit
// code tells the caller when to retry.
func TestGroupAnswerMayStopEarlyAndRateLimitCodesCarryRetryAfter(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, decoded := generateRequest(f, sequential("a cube", "b64_json", 4), GeneratedImagesHeader, "2")
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, decoded)
	}
	if items, _ := decoded["data"].([]any); len(items) != 2 {
		t.Fatalf("data has %d pictures, want 2: %v", len(items), decoded)
	}
	if generated := testkit.Field(t, decoded, "usage", "generated_images"); generated != float64(2) {
		t.Fatalf("usage.generated_images = %v, want 2", generated)
	}
	// The header never raises the count, and it is ignored without group generation.
	_, decoded = generateRequest(f, sequential("a cube", "b64_json", 2), GeneratedImagesHeader, "9")
	if items, _ := decoded["data"].([]any); len(items) != 2 {
		t.Fatalf("the header raised the count to %d", len(items))
	}
	_, decoded = generateRequest(f, body("a cube", "b64_json", nil), GeneratedImagesHeader, "2")
	if items, _ := decoded["data"].([]any); len(items) != 1 {
		t.Fatalf("a single request answered %d pictures", len(items))
	}
	for code, want := range map[string]bool{
		"RequestBurstTooFast": true, "RateLimitExceeded.EndpointRPMExceeded": true, "ServerOverloaded": true,
		"QuotaExceeded": false, "SetLimitExceeded": false, "InvalidParameter": false, "AccessDenied": false,
	} {
		_, header, _ := f.Raw("POST", Prefix+"/images/generations", key, body("a cube", "b64_json", nil), UpstreamCodeHeader, code)
		if got := header.Get("Retry-After") != ""; got != want {
			t.Fatalf("%s: Retry-After present = %v, want %v", code, got, want)
		}
	}
}
