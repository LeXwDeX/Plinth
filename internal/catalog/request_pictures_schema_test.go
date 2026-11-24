package catalog_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// The default Catalog sends every picture of a chat model request as JPEG at
// a high quality, and the loader refuses a broken declaration.
func TestRequestPicturesDeclarationIsValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.Load(bytes.NewReader(source))
	if err != nil {
		t.Fatalf("default catalog: %v", err)
	}
	pictures := product.Application().Chat.Agent.RequestPictures
	if pictures == nil || pictures.Format != "jpeg" || pictures.Quality != 95 ||
		pictures.BackgroundColor != "#ffffff" || pictures.MaxPixels <= 0 {
		t.Fatalf("request_pictures = %+v", pictures)
	}
	declared := `"format": "jpeg",
          "quality": 95,
          "background_color": "#ffffff",
          "max_pixels": 16777216`
	if !bytes.Contains(source, []byte(declared)) {
		t.Fatal("the declaration moved; update the test")
	}
	for name, broken := range map[string]struct{ to, want string }{
		"quality zero":     {strings.Replace(declared, `"quality": 95`, `"quality": 0`, 1), "quality"},
		"quality over 100": {strings.Replace(declared, `"quality": 95`, `"quality": 101`, 1), "quality"},
		"unknown format":   {strings.Replace(declared, `"jpeg"`, `"webp"`, 1), "format"},
		"broken colour":    {strings.Replace(declared, `"#ffffff"`, `"white"`, 1), "background_color"},
		"zero pixels":      {strings.Replace(declared, `16777216`, `0`, 1), "max_pixels"},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := bytes.Replace(source, []byte(declared), []byte(broken.to), 1)
			_, err := catalog.Load(bytes.NewReader(mutated))
			if err == nil || !strings.Contains(err.Error(), broken.want) {
				t.Fatalf("err = %v, want it to name %s", err, broken.want)
			}
		})
	}
}
