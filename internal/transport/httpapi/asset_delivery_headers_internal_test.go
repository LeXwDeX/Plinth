package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #315: a product file without a Schema format name (the OBJ's MTL, a
// texture-sized text file an upstream returned) is an attachment with a safe
// <role>-<task>.<extension> name; a previewed picture stays inline; both are
// sandboxed.
func TestStoredAssetDeliveryHeaders(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{application: product.Application()}
	for _, testCase := range []struct {
		role, variant, mediaType, disposition string
	}{
		{"model", "mtl", "text/plain", `attachment; filename="model-task_1.mtl"; filename*=UTF-8''model-task_1.mtl`},
		{"report", "print.analysis", "application/json", `attachment; filename="report-task_1.json"; filename*=UTF-8''report-task_1.json`},
		{"texture", "texture.base_color", "image/png", "inline"},
		{"thumbnail", "primary", "image/png", "inline"},
		{"model", "x", "text/html", `attachment; filename="model-task_1.x"; filename*=UTF-8''model-task_1.x`},
	} {
		recorder := httptest.NewRecorder()
		server.setStoredAssetDeliveryHeaders(recorder, testCase.mediaType, storedAssetFilename(testCase.role, "task_1", testCase.variant, testCase.mediaType))
		header := recorder.Header()
		if header.Get("Content-Disposition") != testCase.disposition || header.Get("Content-Security-Policy") != "sandbox" || header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("%s/%s: %v", testCase.role, testCase.variant, header)
		}
	}
	for raw, want := range map[string]string{
		`../../etc/passwd`: "etc-passwd",
		"a\"b\r\nc.txt":    "a-b-c.txt",
		"":                 "download",
		"model-task_1.glb": "model-task_1.glb",
		"名字.png":           "png",
	} {
		if got := safeDownloadFilename(raw); got != want {
			t.Fatalf("safeDownloadFilename(%q) = %q; want %q", raw, got, want)
		}
	}
}
