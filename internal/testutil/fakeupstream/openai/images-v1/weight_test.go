package openaiimagesv1

import (
	"bytes"
	"image"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #629: a generated picture can be as heavy as a real 4K answer, so using it
// as a later input crosses an Offering's max_bytes on a fake stack. The weight
// comes from the prompt (a deployed fake stack's only input), from the
// stack-wide control or from the key; without one the picture is unchanged.
func TestPictureWeightMakesAGeneratedPictureHeavy(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	request := func(prompt string) map[string]any {
		return map[string]any{"model": Model, "prompt": prompt, "size": "2048x2048"}
	}
	status, body := generate(f, request("a cube"))
	if status != http.StatusOK {
		t.Fatalf("plain: %d %v", status, body)
	}
	plain := picture(t, body)
	if len(plain) > 1<<20 {
		t.Fatalf("the default picture is %d bytes; the default must stay light", len(plain))
	}

	const weight = 4 << 20
	check := func(label string, data []byte) {
		t.Helper()
		config, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || format != "png" || config.Width != 2048 || config.Height != 2048 || len(data) < weight {
			t.Fatalf("%s: %s %dx%d %d bytes err=%v, want a 2048x2048 PNG of at least %d bytes",
				label, format, config.Width, config.Height, len(data), err, weight)
		}
		if err := testkit.DeliveryErr(t, data, "image", "", "image/png"); err != nil {
			t.Fatalf("%s: the heavy picture fails the delivery check: %v", label, err)
		}
	}
	status, body = generate(f, request("a cube [[fake:picture-bytes:4194304:noise]]"))
	if status != http.StatusOK {
		t.Fatalf("marker: %d %v", status, body)
	}
	check("prompt marker", picture(t, body))

	status, body = edit(f, editBody(t, "image[]", true), fakeupstream.PictureBytesHeader, "4194304")
	if status != http.StatusOK {
		t.Fatalf("edit: %d %v", status, body)
	}
	if data := picture(t, body); len(data) < weight {
		t.Fatalf("edit with the weight header: %d bytes", len(data))
	}

	if err := f.Fake.Env().SetPictureWeight(fakeupstream.PictureWeight{Bytes: weight, MinPixels: 2048 * 2048}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Fake.Env().SetPictureWeight(fakeupstream.PictureWeight{}) })
	status, body = generate(f, request("a cube"))
	if status != http.StatusOK {
		t.Fatalf("stack weight: %d %v", status, body)
	}
	check("stack weight", picture(t, body))
	small := request("a cube")
	small["size"] = "1024x1024"
	if status, body = generate(f, small); status != http.StatusOK || len(picture(t, body)) >= weight {
		t.Fatalf("a picture below min_pixels: %d, want it light", status)
	}
}
