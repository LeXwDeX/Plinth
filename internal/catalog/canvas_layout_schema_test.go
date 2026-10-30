package catalog_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// replaceStringValue replaces the first JSON string value of key in source.
func replaceStringValue(t *testing.T, source []byte, key, value string) []byte {
	t.Helper()
	marker := []byte(`"` + key + `": "`)
	start := bytes.Index(source, marker)
	if start < 0 {
		t.Fatalf("missing key %q", key)
	}
	start += len(marker)
	end := start
	for end < len(source) && !(source[end] == '"' && source[end-1] != '\\') {
		end++
	}
	changed := append([]byte(nil), source[:start]...)
	changed = append(changed, value...)
	return append(changed, source[end:]...)
}

// #272: the texts that tell the Agent a picture is the artboard layout, and
// the notes about the canvas output size, are Schema declarations the catalog
// checks; a broken one is refused at load time instead of reaching the Agent.
func TestCanvasLayoutTextsAreRequired(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(bytes.NewReader(source)); err != nil {
		t.Fatalf("default catalog: %v", err)
	}
	for _, test := range []struct {
		key, value, want string
	}{
		{"canvas_layout_template", "画板版面 {layers}", "画板版面文案"},
		{"canvas_layers_template", "，含图层", "画板版面文案"},
		{"selection_canvas_template", "，是用户在画板上摆好的版面", "画板版面文案"},
		{"selection_canvas_note", " ", "画板版面文案"},
		{"image.canvas", `\n[图片 句柄 {handle}：画板版面]`, "画板版面文案"},
		{"canvas_output_size_template", "输出尺寸设为 {size}。", "canvas_output_size_template"},
		{"canvas_output_size_nearest_template", "所选模型给不了画板尺寸 {canvas}。", "canvas_output_size_nearest_template"},
		{"canvas_layers", "", "api.attachment.canvas_layers"},
		{"canvas_image_detail", "", "context.canvas_image_detail"},
	} {
		t.Run(test.key, func(t *testing.T) {
			changed := replaceStringValue(t, source, test.key, test.value)
			_, err := catalog.Load(bytes.NewReader(changed))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want one naming %q", err, test.want)
			}
		})
	}
	changed := bytes.Replace(source, []byte(`"maximum_canvas_layers": 1000`), []byte(`"maximum_canvas_layers": 0`), 1)
	if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), "maximum_canvas_layers") {
		t.Fatalf("a zero layer bound: %v", err)
	}
	// Only a tool that binds the snapshot (or whose single reference sets
	// it, #296) has its size set and reported.
	changed = bytes.Replace(source, []byte(`"name": "refine_image",
          "canvas_source": "bind",
          "reference_size": "single",`), []byte(`"name": "refine_image",
          "canvas_source": "refuse",`), 1)
	if bytes.Equal(changed, source) {
		t.Fatal("missing refine_image canvas_source fixture")
	}
	if _, err := catalog.Load(bytes.NewReader(changed)); err == nil || !strings.Contains(err.Error(), "size_note") {
		t.Fatalf("size_note on a tool that refuses the canvas: %v", err)
	}
}

// #296: the rule that a single reference sets the output size is declared per
// tool, and its notes to the Agent are Schema texts the catalog checks.
func TestReferenceSizeIsDeclaredWithItsNotes(t *testing.T) {
	source, err := os.ReadFile("default_catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		key, value, want string
	}{
		{"reference_output_size_template", "输出尺寸设为 {size}。", "reference_output_size_template"},
		{"reference_output_size_nearest_template", "参考图 {reference}。", "reference_output_size_nearest_template"},
		{"reference_size", "always", "reference_size"},
	} {
		t.Run(test.key, func(t *testing.T) {
			changed := replaceStringValue(t, source, test.key, test.value)
			_, err := catalog.Load(bytes.NewReader(changed))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v; want one naming %q", err, test.want)
			}
		})
	}
}
