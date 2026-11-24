package mattingapiv1

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

// I21: the service's result is the source picture with its background taken
// away: an RGBA PNG of the source's own size whose background is transparent.
// The fake used to answer one opaque white 64x64 picture whatever was uploaded.
// Constructed (see Constructed()): the committed live captures keep no artifact
// bytes, only that the answer was a PNG.
func TestDownloadIsAnRGBAPictureOfTheSourceSizeWithTransparency(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	pictures := []testPicture{{"wide.png", 300, 200}, {"small.png", 64, 64}, {"tall.jpg", 17, 91}, {"odd.png", 5, 3}}
	final := finishedBatch(t, f, pictures...)
	for index, picture := range pictures {
		header, data := downloaded(t, f, final, index)
		if header.Get("Content-Type") != "image/png" || header.Get("Content-Length") != fmt.Sprint(len(data)) {
			t.Fatalf("%s: headers %v", picture.name, header)
		}
		config, err := png.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", picture.name, err)
		}
		if config.Width != picture.width || config.Height != picture.height {
			t.Fatalf("%s: delivered %dx%d, want the source's %dx%d", picture.name, config.Width, config.Height, picture.width, picture.height)
		}
		if config.ColorModel != color.NRGBAModel {
			t.Fatalf("%s: color model %v is not RGBA with alpha", picture.name, config.ColorModel)
		}
		decoded, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", picture.name, err)
		}
		transparent, opaque := alphaCounts(decoded)
		if picture.width >= 16 && picture.height >= 16 && (transparent == 0 || opaque == 0) {
			t.Fatalf("%s: %d transparent and %d opaque pixels, a matte has both", picture.name, transparent, opaque)
		}
	}
}

func alphaCounts(picture image.Image) (transparent, opaque int) {
	bounds := picture.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			switch _, _, _, alpha := picture.At(x, y).RGBA(); alpha {
			case 0:
				transparent++
			case 0xffff:
				opaque++
			}
		}
	}
	return transparent, opaque
}

// A file whose header the fake cannot read keeps the old fixed size, still with
// an alpha channel: the service always reads the size, so what it does with a
// file that is not a picture was never observed (constructed).
func TestDownloadOfAFileThatIsNotAPictureIsTheFixedSizeRGBA(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	status, created := submit(f, t) // "png bytes", which no decoder reads
	if status != http.StatusOK {
		t.Fatalf("create: %d %v", status, created)
	}
	final := pollUntil(f, t, created["batch_id"].(string), "SUCCESS")
	_, data := downloaded(t, f, final, 0)
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width != 64 || config.Height != 64 || config.ColorModel != color.NRGBAModel {
		t.Fatalf("fallback picture = %+v, %v", config, err)
	}
}

// The picture is a function of the source size alone, so two downloads of the
// same size are the same bytes (a retry of a download is not a different file).
func TestDownloadBytesAreDeterministicPerSize(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	final := finishedBatch(t, f, testPicture{"a.png", 40, 30}, testPicture{"b.png", 40, 30}, testPicture{"c.png", 30, 40})
	_, first := downloaded(t, f, final, 0)
	_, second := downloaded(t, f, final, 1)
	_, third := downloaded(t, f, final, 2)
	if !bytes.Equal(first, second) || bytes.Equal(first, third) {
		t.Fatalf("same size same bytes = %v, different size differs = %v", bytes.Equal(first, second), !bytes.Equal(first, third))
	}
}

// A source larger than the fake is willing to draw is answered scaled down to
// the cap, keeping the ratio (a documented deviation: the service keeps the
// size; the fake bounds its own memory).
func TestDownloadOfAHugeSourceIsCappedKeepingTheRatio(t *testing.T) {
	width, height := scaledToCap(8192, 4096)
	if width != maxEchoSide || height != maxEchoSide/2 {
		t.Fatalf("scaled = %dx%d", width, height)
	}
	if width, height := scaledToCap(300, 200); width != 300 || height != 200 {
		t.Fatalf("a small source changed: %dx%d", width, height)
	}
}
