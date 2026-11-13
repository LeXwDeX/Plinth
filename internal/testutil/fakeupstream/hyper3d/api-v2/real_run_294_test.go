package hyper3dapiv2

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/modelinspect"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream"
	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/testkit"
)

// The one paid game-character run on dev-test (2026-11-13, #294 follow-up): the
// Rodin Gen-2.5 text-to-3D Offering with the game character purpose's request,
// session purpose game_character on pc_console. The service accepted it, took
// about 160 seconds, charged 1 credit and delivered a PBR GLB (one mesh, about
// 21,800 triangles for 10,000 quads, no skin, three embedded PNG images of
// 2048x2048) next to a WebP preview. The raw bodies were not captured, so these
// tests pin what the run showed and what the page's price table gives, not a
// recorded wire. What the fake does not reproduce (the fixed 72 triangles and the
// one 64x64 texture are the shared fixture's) is recorded in the /rodin endpoint
// note (fake.go, rodinRealRunNote) and is not asserted here.

// gameCharacterFields is that run's form: the Catalog's request template with
// the purpose's overrides for its platform (pc_console; a session without a
// platform and the phone class get 8000 faces since the budget follow-up), as the
// adapter sends it.
func gameCharacterFields() map[string]string {
	return map[string]string{
		"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "material": "PBR",
		"mesh_mode": "Quad", "quality": "high", "quality_override": "10000", "texture_mode": "high",
		"TAPose": "true", "quad_normal": "true", "prompt": "a game character standing in an A pose",
	}
}

// listed is one entry of a download list.
type listed struct{ name, url string }

func listOf(t *testing.T, f *testkit.Fixture, s submitted) []listed {
	t.Helper()
	var files []listed
	for _, item := range download(t, f, s) {
		entry := item.(map[string]any)
		files = append(files, listed{name: entry["name"].(string), url: entry["url"].(string)})
	}
	return files
}

func submitFields(t *testing.T, f *testkit.Fixture, fields map[string]string) (submitted, map[string]any) {
	t.Helper()
	status, _, body := f.Do("POST", Prefix+"/rodin", key, form(fields))
	if status != http.StatusCreated || body["error"] != nil {
		t.Fatalf("%v = %d %v", fields, status, body)
	}
	jobs, _ := testkit.Field(t, body, "jobs", "uuids").([]any)
	return submitted{
		uuid: testkit.Field(t, body, "uuid").(string), key: testkit.Field(t, body, "jobs", "subscription_key").(string), jobs: len(jobs),
	}, body
}

// The run's request is accepted, charged what the run was charged, goes through
// the sub-job flow, and ends with the list the Offering's download_files declares
// a model and a preview from: a PBR GLB and a WebP, plus the extra mesh the
// Extreme-High tier also returns. Every file the fake serves carries the media
// type the delivery slots declare.
func TestGameCharacterRunIsAcceptedAndDeliversTheShapeOfTheRealRun(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	s, accepted := submitFields(t, f, gameCharacterFields())
	if got := testkit.Field(t, accepted, "consumed").(float64); got != 1 {
		t.Fatalf("consumed = %v, want the 1 credit the run was charged (0.5 base + 0.5 Extreme-High)", got)
	}
	if s.jobs != 6 {
		t.Fatalf("sub-jobs = %d, want the sample's 6", s.jobs)
	}
	if flow := strings.Join(poll(t, f, s), ">"); !strings.HasPrefix(flow, "Waiting>Generating") || !strings.HasSuffix(flow, "Done") {
		t.Fatalf("flow = %s", flow)
	}

	files := listOf(t, f, s)
	var names []string
	for _, file := range files {
		names = append(names, file.name)
	}
	if len(files) != 3 || !strings.HasSuffix(names[0], "_pbr.glb") || names[1] != "preview.webp" || names[2] != extraMeshName {
		t.Fatalf("download list = %v, want the PBR pack, the preview and the Extreme-High extra mesh", names)
	}
	for index, want := range []string{"model/gltf-binary", "image/webp", "model/gltf-binary"} {
		status, contentType, data := f.Download(files[index].url)
		if status != http.StatusOK || contentType != want || len(data) == 0 {
			t.Fatalf("%s = %d %q %d bytes, want %s", files[index].name, status, contentType, len(data), want)
		}
		switch index {
		case 0:
			if err := testkit.DeliveryErr(t, data, "model", "glb", "model/gltf-binary"); err != nil {
				t.Fatalf("the PBR GLB is refused by the delivery check: %v", err)
			}
			model, err := modelinspect.Parse(bytes.NewReader(data), int64(len(data)), modelinspect.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			report := modelinspect.Inspect(model, modelinspect.DefaultInspectOptions())
			// What the run's file and the fixture share: one mesh, no skin, a
			// textured PBR material whose images are PNG. The texture edge and the
			// triangle count are the fixture's (see the endpoint note).
			if report.Meshes != 1 || len(report.Skins) != 0 || report.TextureCount < 1 {
				t.Fatalf("GLB meshes %d skins %d textures %d", report.Meshes, len(report.Skins), report.TextureCount)
			}
			for _, material := range report.Materials {
				for _, slot := range material.Slots {
					if slot.Present && slot.MIME != "image/png" {
						t.Fatalf("slot %s is %q, want image/png", slot.Slot, slot.MIME)
					}
				}
			}
		case 1:
			if err := testkit.DeliveryErr(t, data, "thumbnail", "primary", "image/webp"); err != nil {
				t.Fatalf("the preview is refused by the delivery check: %v", err)
			}
		}
	}
}

// The receipt's consumed follows the Gen-2.5 page's price table (checked on the
// day of the run): 0.5 for the generation, +0.5 for the Extreme-High tier, +2.0
// for texture_mode=extreme-high, additive, and nothing for a parameter the
// table does not list. Three rows are real charges: the cheapest request of the
// alignment run (0.5), the display request of #188 (3) and the game-character
// run (1). The others are the table applied.
func TestSubmissionChargeFollowsTheDocumentedPriceTable(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	base := func(extra map[string]string) map[string]string {
		fields := map[string]string{"geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"}
		for name, value := range extra {
			fields[name] = value
		}
		return fields
	}
	for name, test := range map[string]struct {
		fields map[string]string
		want   float64
	}{
		"the cheapest request (real)":                  {base(map[string]string{"tier": "Gen-2.5-Extreme-Low", "texture_mode": "extreme-low", "mesh_mode": "Raw", "quality": "medium"}), 0.5},
		"the display request (real, #188)":             {base(map[string]string{"tier": "Gen-2.5-Extreme-High", "texture_mode": "extreme-high", "mesh_mode": "Raw", "quality": "high", "quality_override": "2000000"}), 3},
		"the game-character run (real)":                {gameCharacterFields(), 1},
		"Extreme-High with the tier's default texture": {base(map[string]string{"tier": "Gen-2.5-Extreme-High"}), 1},
		"a middle tier with its default texture":       {base(map[string]string{"tier": "Gen-2.5-Medium"}), 0.5},
		"a cheap tier with the 6K texture":             {base(map[string]string{"tier": "Gen-2.5-Low", "texture_mode": "extreme-high"}), 2.5},
		"Quad, the switches and a seed add nothing":    {base(map[string]string{"tier": "Gen-2.5-High", "mesh_mode": "Quad", "TAPose": "true", "quad_normal": "true", "seed": "7"}), 0.5},
		"an omitted tier is not the Gen-2.5 table":     {base(nil), 0.5},
	} {
		t.Run(name, func(t *testing.T) {
			_, accepted := submitFields(t, f, test.fields)
			if got := testkit.Field(t, accepted, "consumed").(float64); got != test.want {
				t.Fatalf("consumed = %v, want %v", got, test.want)
			}
		})
	}
}

// Only the Extreme-High tier returns the additional mesh (the page: "an
// additional mesh with up to 10M of faces"); every other tier lists the PBR pack
// and the preview.
func TestOnlyTheExtremeHighTierListsTheExtraMesh(t *testing.T) {
	f := newFixture(t, fakeupstream.Config{})
	for tier, want := range map[string]int{
		"Gen-2.5-Extreme-Low": 2, "Gen-2.5-Low": 2, "Gen-2.5-Medium": 2, "Gen-2.5-High": 2, "Gen-2.5-Extreme-High": 3,
	} {
		t.Run(tier, func(t *testing.T) {
			s, _ := submitFields(t, f, map[string]string{"tier": tier, "geometry_file_format": "glb", "material": "PBR", "prompt": "a robot"})
			poll(t, f, s)
			files := listOf(t, f, s)
			if len(files) != want {
				t.Fatalf("%s lists %d files, want %d: %v", tier, len(files), want, files)
			}
			if has := files[len(files)-1].name == extraMeshName; has != (want == 3) {
				t.Fatalf("%s: extra mesh present = %v", tier, has)
			}
		})
	}
}

// The run and its limits travel with the endpoint table (and so with the
// alignment report): the date, the request, the charge, the delivered file, and
// what the fixed files do not reproduce of it.
func TestRodinEndpointNoteRecordsTheRealRunAndItsLimits(t *testing.T) {
	for _, endpoint := range endpoints {
		if endpoint.Method != http.MethodPost || endpoint.Path != "/rodin" {
			continue
		}
		for _, want := range []string{
			realRun, "mesh_mode=Quad", "quality_override=10000", "texture_mode=high", "TAPose=true", "quad_normal=true",
			"charged 1 credit", "9,437,496 bytes", "21,800 triangles", "2048x2048", "Not modeled", "6144 base color",
			"\"tier is required\" callout is advice",
		} {
			if !strings.Contains(endpoint.Note, want) {
				t.Errorf("the /rodin note does not record %q", want)
			}
		}
		return
	}
	t.Fatal("no /rodin endpoint")
}
