package hyper3dapiv2

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"path"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

// #568: the Rodin chain of the 1.9 real round on dev-test (2026-11-24), five
// requests through Plinth. Its generation receipts, status polls and download
// lists match the committed samples in structure, so they are not committed
// again. The two processing requests sent the Catalog's High forms, and the
// file they served is the evidence kept here (testdata/evidence-568-processing-high,
// the Bang on a Rodin task and the Texture of an uploaded GLB: final polls,
// download lists and the file GETs; the creates were over the recorder's 1 MiB
// limit or refused by its sanitizer, and the list names are placeholders, so the
// served name comes from the file's path). It is evidence, not a template:
// sampleFS does not embed it.

//go:embed testdata/evidence-568-processing-high.candidate.json
var processingHighEvidence []byte

// servedNames maps the task uuid of each download in the evidence to the file
// name its GET served.
func servedNames(t *testing.T) map[string]string {
	t.Helper()
	var exchanges []wirefixture.Exchange
	if err := json.Unmarshal(processingHighEvidence, &exchanges); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	for _, exchange := range exchanges {
		if exchange.Request.Method == http.MethodGet {
			// /assets/api/<task uuid>/pack/glb/<name>
			file := exchange.Request.Path
			names[path.Base(path.Dir(path.Dir(path.Dir(file))))] = path.Base(file)
		}
	}
	return names
}

// The Catalog's request templates of hyper3d/bang/rodin-task and
// hyper3d/texture/local-model; the real round sent them unchanged.
var (
	catalogBangFields    = map[string]string{"geometry_file_format": "glb", "material": "PBR", "resolution": "High", "mesh_mode": "Raw", "quality_override": "2000000", "strength": "5"}
	catalogTextureFields = map[string]string{"geometry_file_format": "glb", "material": "PBR", "resolution": "High", "texture_mode": "extreme-high", "hd_texture": "true"}
)

// Both High processing requests served one file, base_high_pbr.glb, and the
// Texture one carried a 12288 base color (the round's L5 report, #568). The fake
// names both packs after their resolution and sizes the Texture pack from its
// form.
func TestRealRoundHighProcessingFilesAndTextureEdge(t *testing.T) {
	served := servedNames(t)
	if served["id-27"] != "base_high_pbr.glb" || served["id-48"] != "base_high_pbr.glb" {
		t.Fatalf("the evidence served %v", served)
	}
	f := newFixture(t, fakeupstream.Config{})
	source, _ := submitFields(t, f, gameCharacterFields())
	poll(t, f, source)

	bangFields := map[string]string{"asset_id": source.uuid}
	for k, v := range catalogBangFields {
		bangFields[k] = v
	}
	bang := submit(t, f, "/bang", form(bangFields))
	poll(t, f, bang)
	texture := submit(t, f, textureRoute, form(catalogTextureFields, "model", "image"))
	poll(t, f, texture)
	for name, task := range map[string]submitted{"bang": bang, "texture": texture} {
		list := listOf(t, f, task)
		if len(list) != 1 || list[0].name != "base_high_pbr.glb" {
			t.Errorf("%s lists %v, the real round served one base_high_pbr.glb", name, list)
		}
	}
	list := listOf(t, f, texture)
	status, contentType, data := f.Download(list[0].url)
	if status != http.StatusOK || contentType != "model/gltf-binary" {
		t.Fatalf("texture file = %d %q", status, contentType)
	}
	if edges := slotEdgesOf(readModel(t, data)); edges != [3]int{textureEdge12K, textureEdge4K, textureEdge4K} {
		t.Errorf("the Catalog's Texture form delivers slot edges %v, want a 12288 base color beside two 4096 maps", edges)
	}
	// A Basic Texture keeps the 2026-10-29 name.
	basic := submit(t, f, textureRoute, form(map[string]string{"material": "PBR", "resolution": "Basic", "texture_mode": "extreme-low"}, "model", "image"))
	poll(t, f, basic)
	if list := listOf(t, f, basic); len(list) != 1 || list[0].name != "base_basic_pbr.glb" {
		t.Errorf("a Basic Texture lists %v", list)
	}
}
