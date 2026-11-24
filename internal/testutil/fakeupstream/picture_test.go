package fakeupstream_test

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	_ "image/png"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/imageinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
)

func decode(t *testing.T, data []byte) (image.Image, string) {
	t.Helper()
	picture, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("the fake picture does not decode: %v", err)
	}
	return picture, format
}

// I17: the picture has the requested pixel size, whatever the format and size.
func TestPictureHasTheRequestedSize(t *testing.T) {
	for _, tc := range []struct {
		format        string
		width, height int
	}{
		{"png", 64, 64}, {"png", 1536, 1152}, {"png", 1024, 640}, {"png", 3840, 2160},
		{"jpeg", 1024, 1024}, {"jpeg", 1376, 768}, {"jpeg", 4096, 4096}, {"jpeg", 5504, 3072},
	} {
		file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: tc.format, Width: tc.width, Height: tc.height})
		if err != nil {
			t.Fatal(err)
		}
		if file.MediaType != "image/"+tc.format {
			t.Fatalf("%s media type = %q", tc.format, file.MediaType)
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(file.Data))
		if err != nil || format != tc.format || config.Width != tc.width || config.Height != tc.height {
			t.Fatalf("%s %dx%d decodes as %s %dx%d (%v)", tc.format, tc.width, tc.height, format, config.Width, config.Height, err)
		}
		if len(file.Data) > 4<<20 {
			t.Fatalf("%s %dx%d weighs %d bytes: the fake picture must stay small", tc.format, tc.width, tc.height, len(file.Data))
		}
	}
	for _, size := range [][2]int{{0, 10}, {10, 0}, {-1, 5}, {7000, 7000}} {
		if _, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: size[0], Height: size[1]}); err == nil {
			t.Fatalf("size %v was accepted", size)
		}
	}
	if _, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "gif", Width: 8, Height: 8}); err == nil {
		t.Fatal("an unknown format was accepted")
	}
}

// The 64x64 PNG is the same picture as ImageFile("png"), so the checklists the
// fake stack passes at 64x64 keep passing at every other size.
func TestPictureKeepsTheSubjectOfImageFile(t *testing.T) {
	reference, err := fakeupstream.ImageFile("png")
	if err != nil {
		t.Fatal(err)
	}
	same, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 64, Height: 64})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := decode(t, reference.Data)
	got, _ := decode(t, same.Data)
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			if color.NRGBAModel.Convert(want.At(x, y)) != color.NRGBAModel.Convert(got.At(x, y)) {
				t.Fatalf("pixel %d,%d differs from ImageFile(png)", x, y)
			}
		}
	}
	for _, size := range [][2]int{{2048, 2048}, {1536, 1152}, {1376, 768}} {
		file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: size[0], Height: size[1]})
		if err != nil {
			t.Fatal(err)
		}
		report, err := imageinspect.Inspect(file.Data, imageinspect.Limits{MaxBytes: 4 << 20, MaxPixels: 8 << 20})
		if err != nil {
			t.Fatal(err)
		}
		if report.SubjectRatio < 0.05 || report.SubjectRatio > 0.95 || report.TouchesEdge || report.ComponentCount != 1 {
			t.Fatalf("%v report = %+v", size, report)
		}
		checks := report.Review(imageinspect.Checklist{
			RequirePlainBackground: true, MaxBackgroundSpread: 32,
			MinSubjectRatio: 0.05, MaxSubjectRatio: 0.95, RequireEdgeClear: true, MaxComponents: 1,
		})
		if imageinspect.Failed(checks) {
			t.Fatalf("%v fails the 3d_input checklist: %+v", size, checks)
		}
	}
}

func TestPictureIsDeterministicAndVariantsDiffer(t *testing.T) {
	spec := fakeupstream.PictureSpec{Format: "png", Width: 256, Height: 128}
	first, _ := fakeupstream.Picture(spec)
	again, _ := fakeupstream.Picture(spec)
	if !bytes.Equal(first.Data, again.Data) {
		t.Fatal("the same spec answered different bytes")
	}
	seen := map[string]int{}
	for variant := 0; variant < 10; variant++ {
		for _, format := range []string{"png", "jpeg"} {
			file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: format, Width: 256, Height: 128, Variant: variant})
			if err != nil {
				t.Fatal(err)
			}
			key := format + string(file.Data)
			if previous, dup := seen[key]; dup {
				t.Fatalf("%s variant %d repeats variant %d", format, variant, previous)
			}
			seen[key] = variant
		}
	}
}

// I19: a transparent PNG has a clear background and an opaque subject.
func TestTransparentPictureHasAnAlphaChannel(t *testing.T) {
	file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 128, Height: 128, Transparent: true})
	if err != nil {
		t.Fatal(err)
	}
	picture, _ := decode(t, file.Data)
	if _, _, _, alpha := picture.At(2, 2).RGBA(); alpha != 0 {
		t.Fatalf("background alpha = %d, want 0", alpha)
	}
	if _, _, _, alpha := picture.At(64, 64).RGBA(); alpha != 0xffff {
		t.Fatalf("subject alpha = %d, want opaque", alpha)
	}
	if _, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "jpeg", Width: 8, Height: 8, Transparent: true}); err == nil {
		t.Fatal("a transparent JPEG was accepted")
	}
	opaque, _ := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 128, Height: 128})
	plain, _ := decode(t, opaque.Data)
	if _, _, _, alpha := plain.At(2, 2).RGBA(); alpha != 0xffff {
		t.Fatalf("an opaque picture has background alpha %d", alpha)
	}
}

func TestPadJPEGKeepsAValidPicture(t *testing.T) {
	file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "jpeg", Width: 64, Height: 48})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{len(file.Data) + 10, 70000, 200000, 1 << 20} {
		padded := fakeupstream.PadJPEG(file.Data, size)
		if len(padded) < size || (len(padded) > size+8 && size > len(file.Data)+10) {
			t.Fatalf("padded to %d bytes, want about %d", len(padded), size)
		}
		config, format, err := image.DecodeConfig(bytes.NewReader(padded))
		if err != nil || format != "jpeg" || config.Width != 64 || config.Height != 48 {
			t.Fatalf("padded JPEG (%d) does not decode: %v", size, err)
		}
		if _, _ = decode(t, padded); false {
			t.Fatal("unreachable")
		}
	}
	if got := fakeupstream.PadJPEG(file.Data, 3); !bytes.Equal(got, file.Data) {
		t.Fatal("a size below the picture must leave it alone")
	}
}

func TestParseSize(t *testing.T) {
	for value, want := range map[string][2]int{"1024x1024": {1024, 1024}, " 1536x1152 ": {1536, 1152}} {
		width, height, ok := fakeupstream.ParseSize(value)
		if !ok || width != want[0] || height != want[1] {
			t.Fatalf("%q = %d %d %v", value, width, height, ok)
		}
	}
	for _, value := range []string{"", "auto", "2K", "1024", "0x10", "10x0", "-4x4", "axb", "10x10x10"} {
		if _, _, ok := fakeupstream.ParseSize(value); ok {
			t.Fatalf("%q was accepted", value)
		}
	}
}

func TestPadPNGKeepsAValidPicture(t *testing.T) {
	file, err := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "png", Width: 64, Height: 48})
	if err != nil {
		t.Fatal(err)
	}
	for _, size := range []int{len(file.Data) + 40, 100000, 1 << 20} {
		padded := fakeupstream.PadFile(file, size).Data
		if len(padded) < size || len(padded) > size+16 {
			t.Fatalf("padded to %d bytes, want about %d", len(padded), size)
		}
		picture, format := decode(t, padded)
		if format != "png" || picture.Bounds().Dx() != 64 || picture.Bounds().Dy() != 48 {
			t.Fatalf("padded PNG (%d) decodes as %s %v", size, format, picture.Bounds())
		}
	}
	if got := fakeupstream.PadFile(file, 3).Data; !bytes.Equal(got, file.Data) {
		t.Fatal("a size below the picture must leave it alone")
	}
	jpegFile, _ := fakeupstream.Picture(fakeupstream.PictureSpec{Format: "jpeg", Width: 32, Height: 32})
	if padded := fakeupstream.PadFile(jpegFile, 200000); len(padded.Data) < 200000 || padded.MediaType != "image/jpeg" {
		t.Fatalf("a padded JPEG is %d bytes (%s)", len(padded.Data), padded.MediaType)
	}
}
