package hyper3dapiv2

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// #303: what a Gen-2.5 generation delivers follows its request. The rules are
// the page's (https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5, read
// in full on 2026-11-16) anchored by the one paid run (2026-11-13: 10,000 quads
// became about 21,800 triangles and three 2048x2048 PNGs for texture_mode high)
// and by the 6144 base color of the display request (#188 acceptance). The rule
// tables are tested on their functions, which costs nothing; a representative
// subset goes through HTTP and is read back the way Plinth reads a model
// (modelinspect: the drawable triangles of the index buffer, the edge from each
// PNG's header), so a number asserted there is a number the product's own checks
// see.

// packEdges is a three-map pack of one edge.
func packEdges(edge int) [3]int { return [3]int{edge, edge, edge} }

// slotEdgesOf is the edge of the base color, metallic-roughness and normal slots
// of a model, 0 for a slot without a texture.
func slotEdgesOf(report modelinspect.Report) [3]int {
	index := map[string]int{"base_color": 0, "metallic_roughness": 1, "normal": 2}
	var edges [3]int
	for _, material := range report.Materials {
		for _, slot := range material.Slots {
			if position, ok := index[slot.Slot]; ok && slot.Present {
				edges[position] = max(slot.Width, slot.Height)
			}
		}
	}
	return edges
}

func readModel(t *testing.T, data []byte) modelinspect.Report {
	t.Helper()
	model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
	if err != nil {
		t.Fatalf("the delivered GLB cannot be read: %v", err)
	}
	return modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
}

// deliveredPBR submits a generation, waits for it and returns its PBR pack and
// the receipt's charge.
func deliveredPBR(t *testing.T, f *testkit.Fixture, fields map[string]string) ([]byte, float64) {
	t.Helper()
	s, accepted := submitFields(t, f, fields)
	consumed := testkit.Field(t, accepted, "consumed").(float64)
	poll(t, f, s)
	files := listOf(t, f, s)
	if len(files) == 0 || !strings.HasSuffix(files[0].name, "_pbr.glb") {
		t.Fatalf("download list %v does not start with the PBR pack", files)
	}
	status, contentType, data := f.Download(files[0].url)
	if status != http.StatusOK || contentType != "model/gltf-binary" || len(data) == 0 {
		t.Fatalf("%s = %d %q %d bytes", files[0].name, status, contentType, len(data))
	}
	return data, consumed
}

// base is a valid Gen-2.5 text form with the extra fields merged in; an empty
// value removes the field.
func base(extra map[string]string) map[string]string {
	fields := map[string]string{"tier": "Gen-2.5-Extreme-Low", "geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"}
	return withFields(fields, extra)
}

// small is a form with a Raw target of 2,000 faces, so a row about the pack's
// edges does not build a large mesh. A tierless form is Quad, which takes 2.18
// triangles per face.
func small(extra map[string]string) map[string]string {
	return withFields(base(map[string]string{"quality_override": "2000"}), extra)
}

func withFields(fields, extra map[string]string) map[string]string {
	for name, value := range extra {
		if value == "" {
			delete(fields, name)
			continue
		}
		fields[name] = value
	}
	return fields
}

// The face count a form targets and the triangles it becomes: quality_override
// beats quality, the preset follows the topology (and, without a Gen-2.5 tier,
// the Gen-1/1.5 page), a triangle mesh has one triangle per face and a Quad mesh
// 2.18. This is the rule before the fixture's ceiling.
func TestDeliveredTrianglesFollowThePresetsAndTheTopology(t *testing.T) {
	for name, tc := range map[string]struct {
		fields map[string]string
		want   int
	}{
		// The paid run, and the same character as the phone and the prop budgets ask for it.
		"the game-character run (real)":  {gameCharacterFields(), 21800},
		"the game character on a phone":  {withFields(gameCharacterFields(), map[string]string{"quality_override": "8000"}), 17440},
		"the game prop on a PC":          {withFields(gameCharacterFields(), map[string]string{"quality_override": "4000"}), 8720},
		"the game prop on a phone":       {withFields(gameCharacterFields(), map[string]string{"quality_override": "3200"}), 6976},
		"the lowest Quad face count":     {base(map[string]string{"mesh_mode": "Quad", "quality_override": "500"}), 1090},
		"the lowest Raw face count":      {base(map[string]string{"quality_override": "500"}), 500},
		"a Raw count":                    {base(map[string]string{"quality_override": "30000", "quality": "high"}), 30000},
		"a Quad count at its ceiling":    {base(map[string]string{"mesh_mode": "Quad", "quality_override": "200000"}), 436000},
		"the display request":            {base(map[string]string{"tier": "Gen-2.5-Extreme-High", "quality": "high", "quality_override": "2000000"}), 2000000},
		"Raw preset high":                {base(map[string]string{"quality": "high"}), 1000000},
		"Raw preset medium":              {base(map[string]string{"quality": "medium"}), 500000},
		"Raw preset low":                 {base(map[string]string{"quality": "low"}), 60000},
		"Raw preset extra-low":           {base(map[string]string{"quality": "extra-low"}), 20000},
		"no quality is medium, Raw":      {base(nil), 500000},
		"Quad preset high":               {base(map[string]string{"mesh_mode": "Quad", "quality": "high"}), 109000},
		"Quad preset medium":             {base(map[string]string{"mesh_mode": "Quad", "quality": "medium"}), 39240},
		"Quad preset low":                {base(map[string]string{"mesh_mode": "Quad", "quality": "low"}), 17440},
		"Quad preset extra-low":          {base(map[string]string{"mesh_mode": "Quad", "quality": "extra-low"}), 8720},
		"Quad with no quality":           {base(map[string]string{"mesh_mode": "Quad"}), 39240},
		"quality_override beats quality": {base(map[string]string{"mesh_mode": "Quad", "quality": "high", "quality_override": "8000"}), 17440},
		// A form without a Gen-2.5 tier is a Gen-1/1.5 asset: Quad by default and
		// the same four presets in both topologies (the Gen-1/1.5 page).
		"a form without a tier":      {base(map[string]string{"tier": ""}), 39240},
		"a tierless Raw high preset": {base(map[string]string{"tier": "", "mesh_mode": "Raw", "quality": "high"}), 50000},
		"a tierless Raw low preset":  {base(map[string]string{"tier": "", "mesh_mode": "Raw", "quality": "low"}), 8000},
		"a tierless Quad low preset": {base(map[string]string{"tier": "", "mesh_mode": "Quad", "quality": "low"}), 17440},
	} {
		t.Run(name, func(t *testing.T) {
			if got := deliveredTriangles(tc.fields); got != tc.want {
				t.Errorf("deliveredTriangles = %d, want %d", got, tc.want)
			}
		})
	}
}

// The page's texture resolution table (texture_mode with addons and
// uhd_texture): 2K below extreme-high, 4K with uhd_texture or HighPack, 8K with
// both; 6K for extreme-high, 12K with HighPack. The two maps beside the base color
// follow the observed packs. An omitted texture_mode is the default of the tier.
func TestTextureEdgesFollowThePagesResolutionTable(t *testing.T) {
	for name, tc := range map[string]struct {
		fields map[string]string
		want   [3]int
	}{
		"extreme-low is 2K":                   {map[string]string{"texture_mode": "extreme-low"}, packEdges(2048)},
		"low is 2K":                           {map[string]string{"texture_mode": "low"}, packEdges(2048)},
		"medium is 2K":                        {map[string]string{"texture_mode": "medium"}, packEdges(2048)},
		"high is 2K":                          {map[string]string{"texture_mode": "high"}, packEdges(2048)},
		"legacy is 2K":                        {map[string]string{"texture_mode": "legacy"}, packEdges(2048)},
		"high with uhd_texture is 4K":         {map[string]string{"texture_mode": "high", "uhd_texture": "true"}, packEdges(4096)},
		"high with uhd_texture off is 2K":     {map[string]string{"texture_mode": "high", "uhd_texture": "false"}, packEdges(2048)},
		"high with HighPack is 4K":            {map[string]string{"texture_mode": "high", "addons": "HighPack"}, packEdges(4096)},
		"HighPack as addons[]":                {map[string]string{"texture_mode": "high", "addons[]": "HighPack"}, packEdges(4096)},
		"HighPack as addons[0]":               {map[string]string{"texture_mode": "high", "addons[0]": "HighPack"}, packEdges(4096)},
		"an add-on that is not HighPack":      {map[string]string{"texture_mode": "high", "addons": "Other"}, packEdges(2048)},
		"high with both is 8K":                {map[string]string{"texture_mode": "high", "uhd_texture": "true", "addons": "HighPack"}, [3]int{8192, 4096, 4096}},
		"extreme-high is 6K":                  {map[string]string{"texture_mode": "extreme-high"}, [3]int{6144, 2048, 2048}},
		"extreme-high with uhd_texture is 6K": {map[string]string{"texture_mode": "extreme-high", "uhd_texture": "true"}, [3]int{6144, 2048, 2048}},
		"extreme-high with HighPack is 12K":   {map[string]string{"texture_mode": "extreme-high", "addons": "HighPack"}, [3]int{12288, 4096, 4096}},
		"extreme-high with both is still 12K": {map[string]string{"texture_mode": "extreme-high", "uhd_texture": "true", "addons": "HighPack"}, [3]int{12288, 4096, 4096}},
		"the default of Gen-2.5-Extreme-Low":  {map[string]string{"tier": "Gen-2.5-Extreme-Low"}, packEdges(2048)},
		"the default of Gen-2.5-Low":          {map[string]string{"tier": "Gen-2.5-Low"}, packEdges(2048)},
		"the default of Gen-2.5-Medium":       {map[string]string{"tier": "Gen-2.5-Medium"}, packEdges(2048)},
		"the default of Gen-2.5-High":         {map[string]string{"tier": "Gen-2.5-High"}, packEdges(2048)},
		"the default of Gen-2.5-Extreme-High": {map[string]string{"tier": "Gen-2.5-Extreme-High"}, packEdges(2048)},
		"a form without a tier is 2K":         {map[string]string{}, packEdges(2048)},
		"a form without a tier with HighPack": {map[string]string{"addons": "HighPack"}, packEdges(4096)},
		"the display request (extreme-high)":  {map[string]string{"tier": "Gen-2.5-Extreme-High", "texture_mode": "extreme-high"}, [3]int{6144, 2048, 2048}},
		"the game character (high, the paid)": {gameCharacterFields(), packEdges(2048)},
	} {
		t.Run(name, func(t *testing.T) {
			edges := textureEdges(tc.fields)
			if len(edges) != 3 || [3]int(edges) != tc.want {
				t.Errorf("textureEdges = %v, want %v", edges, tc.want)
			}
		})
	}
}

// What a representative subset of requests delivers through HTTP, read back like
// the product reads a model: the triangles, the three textures' edges and the
// receipt's charge. Counts above the fixture's 16-bit ceiling are held at it.
func TestGenerationDeliversTheModelTheRequestImplies(t *testing.T) {
	const ceiling = fakeupstream.MaxSizedModelTriangles
	f := newFixture(t, fakeupstream.Config{})
	// The delivery check decodes every pixel, which is most of its cost, so it runs
	// once for each kind of file (see the signature below).
	checked := map[string]bool{}
	for name, tc := range map[string]struct {
		fields    map[string]string
		triangles int
		edges     [3]int
		consumed  float64
	}{
		// The paid run, and the same character as the phone budget asks for it.
		"the game-character run (real)":  {gameCharacterFields(), 21800, packEdges(2048), 1},
		"the game character on a phone":  {withFields(gameCharacterFields(), map[string]string{"quality_override": "8000"}), 17440, packEdges(2048), 1},
		"the game prop on a phone":       {withFields(gameCharacterFields(), map[string]string{"quality_override": "3200", "TAPose": "false"}), 6976, packEdges(2048), 1},
		"the lowest Quad face count":     {base(map[string]string{"mesh_mode": "Quad", "quality_override": "500"}), 1090, packEdges(2048), 0.5},
		"a Raw count under the ceiling":  {base(map[string]string{"quality_override": "30000", "quality": "high"}), 30000, packEdges(2048), 0.5},
		"Quad preset low":                {base(map[string]string{"mesh_mode": "Quad", "quality": "low"}), 17440, packEdges(2048), 0.5},
		"quality_override beats quality": {base(map[string]string{"mesh_mode": "Quad", "quality": "high", "quality_override": "8000"}), 17440, packEdges(2048), 0.5},
		"the Quad ceiling":               {base(map[string]string{"mesh_mode": "Quad", "quality_override": "200000"}), ceiling, packEdges(2048), 0.5},
		"no quality is medium, Raw":      {base(nil), ceiling, packEdges(2048), 0.5},
		// A form without a Gen-2.5 tier is a Gen-1/1.5 asset: Quad by default.
		"a form without a tier":       {small(map[string]string{"tier": ""}), 4360, packEdges(2048), 0.5},
		"high with uhd_texture is 4K": {small(map[string]string{"texture_mode": "high", "uhd_texture": "true"}), 2000, packEdges(4096), 0.5},
		// The display request: a 2,000,000-face target the fixture cannot carry,
		// and the 6K base color of the extreme-high mode beside two 2K maps.
		"the display request": {base(map[string]string{
			"tier": "Gen-2.5-Extreme-High", "mesh_mode": "Raw", "quality": "high", "quality_override": "2000000", "texture_mode": "extreme-high",
		}), ceiling, [3]int{6144, 2048, 2048}, 3},
		// The texture modes on a small mesh: the rows are about the pack's edges.
		"legacy is 2K":                 {small(map[string]string{"texture_mode": "legacy"}), 2000, packEdges(2048), 0.5},
		"extreme-high is 6K and +2":    {small(map[string]string{"texture_mode": "extreme-high"}), 2000, [3]int{6144, 2048, 2048}, 2.5},
		"the default of a tier (high)": {small(map[string]string{"tier": "Gen-2.5-Extreme-High"}), 2000, packEdges(2048), 1},
	} {
		t.Run(name, func(t *testing.T) {
			data, consumed := deliveredPBR(t, f, tc.fields)
			report := readModel(t, data)
			if report.Triangles != int64(tc.triangles) {
				t.Errorf("the model carries %d triangles, want %d", report.Triangles, tc.triangles)
			}
			if got := slotEdgesOf(report); got != tc.edges {
				t.Errorf("texture edges %v, want %v", got, tc.edges)
			}
			if report.TextureCount != 3 || report.Meshes != 1 || len(report.Skins) != 0 || !report.UV.AllHaveUV {
				t.Errorf("%d textures, %d meshes, %d skins, all UV %v", report.TextureCount, report.Meshes, len(report.Skins), report.UV.AllHaveUV)
			}
			if consumed != tc.consumed {
				t.Errorf("consumed %v, want %v", consumed, tc.consumed)
			}
			// Plinth's delivery check accepts what the fake serves: a small and a
			// ceiling-sized mesh with the 2K pack, and the display request's 6K pack.
			// The other packs differ only in an edge and are read by header only.
			size := "small"
			if tc.triangles == ceiling {
				size = "ceiling"
			}
			signature := fmt.Sprint(size, tc.edges)
			if (tc.edges[0] <= 2048 || name == "the display request") && !checked[signature] {
				checked[signature] = true
				if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
					t.Errorf("the delivery check refuses the GLB: %v", err)
				}
			}
		})
	}
}

// The bytes the download list's URL serves are DeliveredModel's, so a test of
// another package (the usage check of inspect_model) can ask for the model of a
// request without a round trip, and sees the file the fake would serve.
func TestDeliveredModelIsTheFileTheFakeServes(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for _, fields := range []map[string]string{gameCharacterFields(), small(map[string]string{"texture_mode": "extreme-high"})} {
		served, _ := deliveredPBR(t, f, fields)
		file, err := DeliveredModel(fields)
		if err != nil {
			t.Fatal(err)
		}
		if file.MediaType != "model/gltf-binary" || !bytes.Equal(served, file.Data) {
			t.Errorf("%v: DeliveredModel (%d bytes) is not what the URL serves (%d bytes)", fields, len(file.Data), len(served))
		}
	}
}

// Only the Gen-2.5 generation's PBR pack follows the request. The Extreme-High
// tier's extra mesh, the preview and the Bang and Texture outputs stay the
// plain fixture: they are not what the issue's real run measured.
func TestOnlyTheGenerationPBRPackFollowsTheRequest(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	plain, err := fakeupstream.ModelFile("glb")
	if err != nil {
		t.Fatal(err)
	}
	// The extra mesh of the Extreme-High tier is the fixed GLB under its own name.
	s, _ := submitFields(t, f, gameCharacterFields())
	poll(t, f, s)
	files := listOf(t, f, s)
	if len(files) != 3 || files[2].name != extraMeshName {
		t.Fatalf("download list = %v", files)
	}
	if _, _, data := f.Download(files[2].url); !bytes.Equal(data, plain.Data) {
		t.Errorf("the extra mesh (%d bytes) is not the plain fixture (%d bytes)", len(data), len(plain.Data))
	}
	// Bang and Texture deliver the plain fixture whatever the generation was.
	bang := submit(t, f, "/bang", form(map[string]string{"asset_id": s.uuid, "material": "PBR", "resolution": "High"}))
	poll(t, f, bang)
	texture := submit(t, f, "/rodin_texture_only", form(map[string]string{"material": "PBR", "texture_mode": "extreme-high"}, "model", "image"))
	poll(t, f, texture)
	for name, task := range map[string]submitted{"bang": bang, "texture": texture} {
		list := listOf(t, f, task)
		if len(list) != 1 {
			t.Fatalf("%s lists %v", name, list)
		}
		if _, _, data := f.Download(list[0].url); !bytes.Equal(data, plain.Data) {
			t.Errorf("%s delivered %d bytes, want the plain fixture's %d", name, len(data), len(plain.Data))
		}
	}
}

// The Texture endpoint's receipt: the page's price table lists 0.5 for the base
// texture generation and 2.0 for texture_mode=extreme-high and, unlike the
// Gen-2.5 page, does not say they add up; the real receipts (#187, the Texture
// Offering sends extreme-high at Basic and at High resolution) were 2, not 2.5.
// The page's default texture_mode is high, which is the base charge.
func TestTextureChargeIsTwoForTheExtremeHighMode(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	consumed := func(route string, fields map[string]string, files ...string) float64 {
		t.Helper()
		status, _, body := f.Do("POST", Prefix+route, key, form(fields, files...))
		if status != http.StatusCreated || body["error"] != nil {
			t.Fatalf("%s %v = %d %v", route, fields, status, body)
		}
		return testkit.Field(t, body, "consumed").(float64)
	}
	for name, tc := range map[string]struct {
		fields map[string]string
		want   float64
	}{
		"the Offering's request (extreme-high, High, real)": {map[string]string{"material": "PBR", "resolution": "High", "texture_mode": "extreme-high", "hd_texture": "true"}, 2},
		"extreme-high at Basic (real)":                      {map[string]string{"material": "PBR", "resolution": "Basic", "texture_mode": "extreme-high"}, 2},
		"the default texture mode (high)":                   {map[string]string{"material": "PBR"}, 0.5},
		"high is the base charge":                           {map[string]string{"material": "PBR", "texture_mode": "high"}, 0.5},
		"the alignment request (extreme-low)":               {map[string]string{"material": "PBR", "resolution": "Basic", "texture_mode": "extreme-low"}, 0.5},
		"legacy is the base charge":                         {map[string]string{"material": "PBR", "texture_mode": "legacy"}, 0.5},
	} {
		t.Run(name, func(t *testing.T) {
			if got := consumed("/rodin_texture_only", tc.fields, "model", "image"); got != tc.want {
				t.Errorf("texture consumed %v, want %v", got, tc.want)
			}
		})
	}
	// Bang keeps its 0.5 (the page and every real receipt).
	generation := submit(t, f, "/rodin", textForm())
	poll(t, f, generation)
	if got := consumed("/bang", map[string]string{"asset_id": generation.uuid, "material": "PBR", "resolution": "High"}); got != 0.5 {
		t.Errorf("bang consumed %v, want 0.5", got)
	}
}

// The Texture note records the price that the receipt follows and why it is not
// the Gen-2.5 sum, and the file route's note says which file follows a request.
func TestTextureAndFileEndpointNotesRecordTheirRules(t *testing.T) {
	notes := map[string]string{}
	for _, endpoint := range endpoints {
		notes[endpoint.Method+" "+endpoint.Path] = endpoint.Note
	}
	for endpoint, wants := range map[string][]string{
		"POST /rodin_texture_only": {
			"consumed is 2 for texture_mode=extreme-high", "#187", "0.5", "without saying they add up", formDocs, "plain fixture",
		},
		"GET /files/{task_uuid}/{name}": {"PBR pack of a Gen-2.5 generation", "follows its request"},
	} {
		for _, want := range wants {
			if !strings.Contains(notes[endpoint], want) {
				t.Errorf("the %s note does not record %q: %s", endpoint, want, notes[endpoint])
			}
		}
	}
}
