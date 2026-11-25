package jimengapiv3

import (
	"bytes"
	"encoding/base64"
	"image"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #629: Seedream's pictures can be as heavy as real 4K answers, inline and
// behind the signed URL, so a generated picture used as a later input crosses
// an Offering's max_bytes on a fake stack. Without a weight they stay light.
func TestPictureWeightMakesAGeneratedPictureHeavy(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	const weight = 4 << 20
	check := func(label string, data []byte, heavy bool) {
		t.Helper()
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "jpeg" || config.Width != 2048 || config.Height != 2048 {
			t.Fatalf("%s: %s %dx%d err=%v, want a 2048x2048 JPEG", label, format, config.Width, config.Height, err)
		}
		if heavy != (len(data) >= weight) {
			t.Fatalf("%s: %d bytes, heavy=%v wanted (weight %d)", label, len(data), heavy, weight)
		}
		if err := testkit.DeliveryErr(t, data, "image", "", "image/jpeg"); err != nil {
			t.Fatalf("%s: delivery check: %v", label, err)
		}
	}
	inline := func(prompt string) []byte {
		t.Helper()
		status, answer := generateRequest(f, body(prompt, "b64_json", nil))
		if status != http.StatusOK {
			t.Fatalf("%q: %d %v", prompt, status, answer)
		}
		data, err := base64.StdEncoding.DecodeString(testkit.Field(t, answer, "data", 0, "b64_json").(string))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	check("default", inline("a cube"), false)
	check("prompt marker", inline("a cube [[fake:picture-bytes:4194304]]"), true)

	status, answer := generateRequest(f, body("a cube [[fake:picture-bytes:4194304:noise]]", "url", nil))
	if status != http.StatusOK {
		t.Fatalf("url: %d %v", status, answer)
	}
	code, contentType, data := f.Download(testkit.Field(t, answer, "data", 0, "url").(string))
	if code != http.StatusOK || contentType != "image/jpeg" {
		t.Fatalf("download: %d %s", code, contentType)
	}
	check("signed URL", data, true)

	if err := f.Fake.Env().SetPictureWeight(fakeupstream.PictureWeight{Bytes: weight}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Fake.Env().SetPictureWeight(fakeupstream.PictureWeight{}) })
	check("stack weight", inline("a cube"), true)
}
