package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #272: the artboard snapshot claim carries its visible layer count under the
// Schema's member name; it stays an untrusted number the service bounds.
func TestChatAttachmentRefReadsTheCanvasLayerCount(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{application: product.Application()}
	names := server.application.Chat.API.Attachment
	item := map[string]json.RawMessage{
		names.Kind:         json.RawMessage(`"image"`),
		names.URL:          json.RawMessage(`"/api/v1/input-assets/snapshot"`),
		names.CanvasLayers: json.RawMessage(`3`),
	}
	if ref := server.chatAttachmentRef(item); ref.CanvasLayers != 3 || ref.URL != "/api/v1/input-assets/snapshot" {
		t.Fatalf("ref = %+v", ref)
	}
	item[names.CanvasLayers] = json.RawMessage(`"many"`)
	if ref := server.chatAttachmentRef(item); ref.CanvasLayers != 0 {
		t.Fatalf("a malformed count was kept: %+v", ref)
	}
}
