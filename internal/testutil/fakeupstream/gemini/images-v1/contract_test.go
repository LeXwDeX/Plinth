package geminiimagesv1

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

func withConfig(request map[string]any, config map[string]any) map[string]any {
	if config == nil {
		delete(request, "eca_image_config")
	} else {
		request["eca_image_config"] = config
	}
	return request
}

func references(count int) map[string]any {
	content := []any{map[string]any{"type": "text", "text": "make it blue"}}
	for index := 0; index < count; index++ {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,AAAA"}})
	}
	return requestBody(content)
}

func decodedPicture(t *testing.T, body map[string]any) (config image.Config, format string, size int) {
	t.Helper()
	_, data := picture(t, body)
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the picture does not decode: %v", err)
	}
	return config, format, len(data)
}

// I17: the picture has the size the aspect ratio and image_size name, in the
// format the recorded answers use (JPEG for text-to-image, PNG with a reference).
func TestPictureHasTheSizeOfItsAspectRatioAndImageSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, tc := range []struct {
		ratio, size   string
		width, height int
	}{
		{"1:1", "1K", 1024, 1024}, {"1:1", "2K", 2048, 2048}, {"1:1", "4K", 4096, 4096},
		{"16:9", "1K", 1376, 768}, {"16:9", "4K", 5504, 3072}, {"3:4", "1K", 896, 1200},
		{"9:16", "2K", 1536, 2752}, {"21:9", "1K", 1584, 672}, {"2:3", "1K", 848, 1264},
	} {
		request := withConfig(textRequest(), map[string]any{"aspect_ratio": tc.ratio, "image_size": tc.size})
		status, body := complete(f, request)
		if status != http.StatusOK {
			t.Fatalf("%s %s: %d %v", tc.ratio, tc.size, status, body)
		}
		config, format, _ := decodedPicture(t, body)
		if format != "jpeg" || config.Width != tc.width || config.Height != tc.height {
			t.Fatalf("%s %s: %s %dx%d, want jpeg %dx%d", tc.ratio, tc.size, format, config.Width, config.Height, tc.width, tc.height)
		}
	}
	// With a reference picture the answer is a PNG of the same table.
	status, body := complete(f, withConfig(references(2), map[string]any{"aspect_ratio": "3:4", "image_size": "1K"}))
	if status != http.StatusOK {
		t.Fatalf("edit: %d %v", status, body)
	}
	mediaType, _ := picture(t, body)
	config, format, _ := decodedPicture(t, body)
	if format != "png" || mediaType != "image/png" || config.Width != 896 || config.Height != 1200 {
		t.Fatalf("edit answered %s %s %dx%d, want png 896x1200", mediaType, format, config.Width, config.Height)
	}
	// No eca_image_config at all is the documented default: 1:1 at 1K.
	status, body = complete(f, withConfig(textRequest(), nil))
	if status != http.StatusOK {
		t.Fatalf("default: %d %v", status, body)
	}
	if config, _, _ := decodedPicture(t, body); config.Width != 1024 || config.Height != 1024 {
		t.Fatalf("the default picture is %dx%d, want 1024x1024", config.Width, config.Height)
	}
	// The text-to-image data URI says JPEG.
	status, body = complete(f, textRequest())
	if mediaType, _ := picture(t, body); status != http.StatusOK || mediaType != "image/jpeg" {
		t.Fatalf("text-to-image data URI is %s (%d)", mediaType, status)
	}
}

func TestEveryPictureSizeStaysWithinThePictureBound(t *testing.T) {
	for ratio, sizes := range pictureSizes {
		for index, dimensions := range sizes {
			pixels := dimensions[0] * dimensions[1]
			if pixels > fakeupstream.MaxPicturePixels {
				t.Fatalf("%s %s is %d pixels, over the fake picture bound", ratio, imageSizes[index], pixels)
			}
		}
	}
	if len(pictureSizes) != 10 || len(imageSizes) != 3 {
		t.Fatalf("the documented sets are ten ratios and three sizes, got %d and %d", len(pictureSizes), len(imageSizes))
	}
}

// I18 (constructed): a ratio or size outside the documented sets, or more than
// 14 references, is a 400. The real gateway may ignore these instead; the real
// round settles it.
func TestOutOfContractImageConfigIsRefused(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for name, config := range map[string]map[string]any{
		"aspect 7:1":             {"aspect_ratio": "7:1", "image_size": "1K"},
		"aspect 1:4":             {"aspect_ratio": "1:4", "image_size": "1K"},
		"aspect auto":            {"aspect_ratio": "auto", "image_size": "1K"},
		"aspect 16-9":            {"aspect_ratio": "16-9", "image_size": "1K"},
		"lower-case size 1k":     {"aspect_ratio": "1:1", "image_size": "1k"},
		"lower-case size 4k":     {"aspect_ratio": "1:1", "image_size": "4k"},
		"half-size 512":          {"aspect_ratio": "1:1", "image_size": "512"},
		"size 3K":                {"aspect_ratio": "1:1", "image_size": "3K"},
		"non-string aspect":      {"aspect_ratio": 1, "image_size": "1K"},
		"non-string size":        {"aspect_ratio": "1:1", "image_size": 1024},
		"empty aspect":           {"aspect_ratio": "", "image_size": "1K"},
		"empty size":             {"aspect_ratio": "1:1", "image_size": ""},
		"size without the unit":  {"aspect_ratio": "1:1", "image_size": "1"},
		"aspect with whitespace": {"aspect_ratio": " 1:1", "image_size": "1K"},
	} {
		status, body := complete(f, withConfig(textRequest(), config))
		if status != http.StatusBadRequest || testkit.Field(t, body, "error", "code") != "invalid_argument" {
			t.Fatalf("%s: %d %v", name, status, body)
		}
	}
	if status, body := complete(f, references(14)); status != http.StatusOK {
		t.Fatalf("14 references: %d %v", status, body)
	}
	status, body := complete(f, references(15))
	if status != http.StatusBadRequest || !strings.Contains(fmt.Sprint(testkit.Field(t, body, "error", "message")), "14") {
		t.Fatalf("15 references: %d %v", status, body)
	}
	// Every Catalog option of both parameters is in the documented sets.
	for _, ratio := range []string{"1:1", "2:3", "3:2", "3:4", "4:3", "4:5", "5:4", "9:16", "16:9", "21:9"} {
		for _, size := range []string{"1K", "2K", "4K"} {
			if status, body := complete(f, withConfig(textRequest(), map[string]any{"aspect_ratio": ratio, "image_size": size})); status != http.StatusOK {
				t.Fatalf("%s %s: %d %v", ratio, size, status, body)
			}
		}
	}
}

// m7: a 4K answer can be made as heavy as a real one.
func TestResponseWeightSelectorPadsThePicture(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	_, plain := complete(f, textRequest())
	_, _, plainSize := decodedPicture(t, plain)
	const weight = 3 << 20
	status, body := complete(f, textRequest(), ResponseBytesHeader, fmt.Sprint(weight))
	if status != http.StatusOK {
		t.Fatalf("%d %v", status, body)
	}
	config, format, size := decodedPicture(t, body)
	if format != "jpeg" || config.Width != 1024 || size < weight || size > weight+16 || size <= plainSize {
		t.Fatalf("padded picture = %s %dx%d %d bytes (plain %d), want a JPEG of about %d bytes", format, config.Width, config.Height, size, plainSize, weight)
	}
	// The same weight through the API key, for a stack that cannot add headers.
	status, _, raw := f.Raw("POST", Prefix+"/chat/completions", "fake-response-bytes-2097152", textRequest())
	if status != http.StatusOK || len(raw) < 2<<20 {
		t.Fatalf("key selector: %d, %d bytes", status, len(raw))
	}
	// The weight is capped, and garbage is ignored.
	if got := requestedWeight(mustRequest(t, "999999999999")); got != MaxResponseBytes {
		t.Fatalf("requested weight = %d, want the cap %d", got, MaxResponseBytes)
	}
	for _, garbage := range []string{"heavy", "-5", ""} {
		if got := requestedWeight(mustRequest(t, garbage)); got != 0 {
			t.Fatalf("weight %q = %d, want 0", garbage, got)
		}
	}
	// It also pads an edit's PNG.
	status, body = complete(f, references(1), ResponseBytesHeader, fmt.Sprint(weight))
	if _, format, size := decodedPicture(t, body); status != http.StatusOK || format != "png" || size < weight {
		t.Fatalf("padded edit: %d %s %d bytes", status, format, size)
	}
	if encoded := fmt.Sprint(testkit.Field(t, body, "choices", 0, "message", "content", 0, "image_url", "url")); !strings.HasPrefix(encoded, "data:image/png;base64,") {
		t.Fatalf("padded edit URI starts %.30s", encoded)
	} else if _, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, "data:image/png;base64,")); err != nil {
		t.Fatal(err)
	}
}

func mustRequest(t *testing.T, header string) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "http://fake.test/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(ResponseBytesHeader, header)
	return request
}
