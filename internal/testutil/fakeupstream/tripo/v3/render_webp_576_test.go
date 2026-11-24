package tripov3

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/align"
)

// #576: a text-to-3D task at Plinth's production defaults (texture at extreme
// quality, detailed geometry) delivers its render as WebP bytes, as the real
// task of 2026-11-24 did, served as binary/octet-stream. The bytes are the
// extended WebP with metadata that the old generic address scan refused, and
// Plinth's delivery check now admits them as a raster.
func TestProductionDefaultTextRenderIsAWebP576(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	body := textBody()
	body["texture"], body["pbr"] = true, true
	body["texture_quality"], body["geometry_quality"] = "extreme", "detailed"
	id := f.create("/generation/text-to-model", body, key)
	_, done := f.finish(id, key)
	renderURL := f.outputURL(done, "rendered_image_url")
	if !strings.HasSuffix(strings.Split(renderURL, "?")[0], "/rendered_image.webp") {
		t.Fatalf("rendered_image_url = %s, want a .webp file", renderURL)
	}
	status, contentType, data := f.download(renderURL)
	if status != http.StatusOK || contentType != ArtifactContentType || align.DetectFormat(data) != "webp" {
		t.Fatalf("render: %d %q %s, want 200 %s webp", status, contentType, align.DetectFormat(data), ArtifactContentType)
	}
	want, err := fakeupstream.RenderWebP()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, want.Data) {
		t.Fatal("the render is not the fixed WebP render")
	}
	path := filepath.Join(t.TempDir(), "render")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := local.ValidateDeliveredFile(file, "thumbnail", "rendered_image", "image/webp"); err != nil {
		t.Fatalf("Plinth's delivery check refuses the render: %v", err)
	}

	// A request off those defaults keeps the PNG render of 2026-10-29.
	plain := f.create("/generation/text-to-model", textBody(), key)
	_, plainDone := f.finish(plain, key)
	_, _, render := f.download(f.outputURL(plainDone, "rendered_image_url"))
	if align.DetectFormat(render) != "png" {
		t.Fatalf("the plain text task's render is %s, want png", align.DetectFormat(render))
	}
}
