package localparts

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestTextureRefSerializesGLTFTexCoordField(t *testing.T) {
	raw, err := json.Marshal(textureRef{Index: 2, TexCoord: 1})
	if err != nil {
		t.Fatalf("marshal textureRef: %v", err)
	}
	if !bytes.Contains(raw, []byte(`"texCoord":1`)) || bytes.Contains(raw, []byte("tex_coord")) {
		t.Fatalf("textureRef JSON = %s, want the glTF texCoord key", raw)
	}
	var decoded textureRef
	if err := json.Unmarshal([]byte(`{"index":0,"texCoord":1}`), &decoded); err != nil {
		t.Fatalf("unmarshal glTF texture reference: %v", err)
	}
	if decoded.TexCoord != 1 {
		t.Fatalf("decoded texCoord = %d, want 1", decoded.TexCoord)
	}
}
