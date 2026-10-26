package catalog_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #285: the default Schema declares the thinking event and its status text,
// and the public projection carries both to the client.
func TestDefaultCatalogDeclaresThinkingStatus(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := taskCatalog.Application().Chat
	if chat.Stream.EventTypes.Thinking != "thinking" || chat.Presentation.Status.Thinking != "思考中…" {
		t.Fatalf("thinking = %q / %q", chat.Stream.EventTypes.Thinking, chat.Presentation.Status.Thinking)
	}
	public := taskCatalog.Application().Public().Chat
	if public.Stream.EventTypes.Thinking != "thinking" || public.Presentation.Status.Thinking != "思考中…" {
		t.Fatalf("public thinking = %q / %q", public.Stream.EventTypes.Thinking, public.Presentation.Status.Thinking)
	}
}

func TestThinkingStatusIsValidated(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	event := `"thinking": "thinking"`
	text := `"thinking": "思考中…"`
	for name, test := range map[string]struct{ old, new, detail string }{
		"event without text": {text, `"thinking": ""`, "presentation.status.thinking"},
		"text without event": {event, `"thinking": ""`, "stream.event_types.thinking"},
		"event reuses text":  {event, `"thinking": "text"`, "重名"},
	} {
		changed := bytes.Replace(source, []byte(test.old), []byte(test.new), 1)
		if bytes.Equal(changed, source) {
			t.Fatalf("%s: fixture %q not found", name, test.old)
		}
		_, err := catalog.Load(bytes.NewReader(changed))
		if err == nil || !strings.Contains(err.Error(), test.detail) {
			t.Errorf("%s: error = %v, want %q", name, err, test.detail)
		}
	}
	// Leaving both out turns the status off and stays valid.
	off := bytes.Replace(source, []byte(","+"\n          "+event), nil, 1)
	off = bytes.Replace(off, []byte(",\n          "+text), nil, 1)
	if bytes.Contains(off, []byte(text)) || bytes.Contains(off, []byte(event)) {
		t.Fatal("fixture for the optional status not found")
	}
	// Dropping the text drops its translation, which would otherwise point at nothing.
	var document map[string]any
	if err := json.Unmarshal(off, &document); err != nil {
		t.Fatal(err)
	}
	english := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)["fields"].(map[string]any)
	delete(english, "chat.presentation.status.thinking")
	off, _ = json.Marshal(document)
	if _, err := catalog.Load(bytes.NewReader(off)); err != nil {
		t.Fatalf("catalog without thinking status: %v", err)
	}
}
