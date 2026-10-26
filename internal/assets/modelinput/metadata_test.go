package modelinput_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"strconv"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/modelinput"
	"github.com/LeXwDeX/Plinth/internal/assets/modelinput/modelinputtest"
)

func TestMetadataDataURICannotBypassImageBudget(t *testing.T) {
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, 2048, 2048))); err != nil {
		t.Fatal(err)
	}
	d, b := modelinputtest.Document(modelinputtest.Options{})
	d["extras"] = map[string]any{"thumbnail": "data:image/png;base64," + base64.StdEncoding.EncodeToString(encoded.Bytes())}
	raw := modelinputtest.GLB(d, b)
	p := policy()
	p.Limits.MaxImagePixels = 1
	p.Limits.MaxTotalImagePixels = 1
	p.Limits.MaxWork = 4*int64(len(raw)) + 1000
	expectKind(t, raw, p, modelinput.Unsupported)
}

func TestDataURIRejectedInEveryJSONPosition(t *testing.T) {
	for _, location := range []string{"nested_value", "object_key", "name", "generator", "escaped"} {
		t.Run(location, func(t *testing.T) {
			d, b := modelinputtest.Document(modelinputtest.Options{})
			const uri = "DaTa:image/png;base64,iVBORw0KGgo="
			switch location {
			case "nested_value":
				d["extras"] = map[string]any{"nested": []any{map[string]any{"thumbnail": uri}}}
			case "object_key":
				d["extras"] = map[string]any{uri: "metadata"}
			case "name":
				d["nodes"].([]any)[0].(map[string]any)["name"] = uri
			case "generator":
				d["asset"].(map[string]any)["generator"] = uri
			case "escaped":
				d["extras"] = map[string]any{"thumbnail": uri}
			}
			raw, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			if location == "escaped" {
				raw = bytes.Replace(raw, []byte("DaTa:"), []byte(`\u0044aTa\u003a`), 1)
			}
			expectKind(t, modelinputtest.Frame(raw, b), policy(), modelinput.Unsupported)
		})
	}
	d, b := modelinputtest.Document(modelinputtest.Options{})
	d["extras"] = map[string]any{"description": "ordinary metadata", "nested": []any{1, "thumbnail"}}
	if _, err := validate(t, modelinputtest.GLB(d, b), policy()); err != nil {
		t.Fatalf("safe metadata rejected: %v", err)
	}
}

func TestAttributeSetsMustStartAtZeroAndRemainConsecutive(t *testing.T) {
	for _, semantic := range []string{"TEXCOORD_", "COLOR_"} {
		for _, sets := range [][]string{{"1"}, {"0", "2"}, {"2147483647"}} {
			t.Run(semantic+sets[len(sets)-1], func(t *testing.T) {
				d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
				attrs := d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
				id := attrs["TEXCOORD_0"]
				if semantic == "TEXCOORD_" {
					delete(attrs, "TEXCOORD_0")
					d["materials"].([]any)[0].(map[string]any)["pbrMetallicRoughness"].(map[string]any)["baseColorTexture"].(map[string]any)["texCoord"] = mustSetIndex(t, sets[len(sets)-1])
				} else {
					id = attrs["POSITION"]
				}
				for _, set := range sets {
					attrs[semantic+set] = id
				}
				expectKind(t, modelinputtest.GLB(d, b), policy(), modelinput.Invalid)
			})
		}
	}
	d, b := modelinputtest.Document(modelinputtest.Options{Textured: true})
	attrs := d["meshes"].([]any)[0].(map[string]any)["primitives"].([]any)[0].(map[string]any)["attributes"].(map[string]any)
	attrs["TEXCOORD_1"] = attrs["TEXCOORD_0"]
	attrs["COLOR_0"] = attrs["POSITION"]
	attrs["COLOR_1"] = attrs["POSITION"]
	if _, err := validate(t, modelinputtest.GLB(d, b), policy()); err != nil {
		t.Fatalf("consecutive sets rejected: %v", err)
	}
}

func mustSetIndex(t *testing.T, raw string) int {
	t.Helper()
	n, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
