package catalog_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #279: the game-relevant knobs of Rodin Gen-2.5 are Schema parameters. The
// ranges below are the ones of the official page
// (https://docs.hyper3d.ai/en/api-specification/rodin-gen2-5, read
// 2026-11-12): quality_override 500 to 2000000 on the Extreme-High tier with an
// additional 200000 in Quad mode, seed 0 to 65535, TAPose and quad_normal
// booleans. One real run (2026-11-13, #294 follow-up) then showed the 2K texture
// and the charge of 1 credit under Quad and the triangles a Quad face gets (the
// output_triangles factor, #304); the other ranges are still the page's.

var rodinOfferingIDs = map[schema.Capability]schema.OfferingID{
	schema.TextTo3D:  "hyper3d/rodin-gen-2.5/text-to-3d",
	schema.ImageTo3D: "hyper3d/rodin-gen-2.5/image-to-3d",
}

func rodinParameters(offering schema.Offering) map[string]schema.Parameter {
	parameters := map[string]schema.Parameter{}
	for _, parameter := range offering.Parameters {
		parameters[parameter.Name] = parameter
	}
	return parameters
}

// rodinInput is a minimal valid input of the Offering with the extra values.
func rodinInput(t *testing.T, capability schema.Capability, extra string) json.RawMessage {
	t.Helper()
	input := map[string]any{"prompt": "a small robot", "authorize_extra_credits": true}
	if capability == schema.ImageTo3D {
		input["reference_assets"] = []string{"reference"}
	}
	if extra != "" {
		var values map[string]any
		if err := json.Unmarshal([]byte(extra), &values); err != nil {
			t.Fatal(err)
		}
		for name, value := range values {
			input[name] = value
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestHyper3DRodinGameParametersAreDeclaredInTheSchema(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for capability, id := range rodinOfferingIDs {
		offering, ok := product.Get(id)
		if !ok {
			t.Fatalf("missing %s", id)
		}
		parameters := rodinParameters(offering)
		number := func(value *float64) float64 {
			if value == nil {
				t.Fatalf("%s: a bound is missing", id)
			}
			return *value
		}

		mesh := parameters["mesh_mode"]
		if mesh.Type != "select" || string(mesh.Default) != `"Raw"` || len(mesh.Options) != 2 ||
			mesh.Options[0].Value != "Raw" || mesh.Options[1].Value != "Quad" ||
			mesh.AdjustmentPolicy != "advised" || mesh.Presentation == nil || mesh.Presentation.Surface != schema.SurfaceReadOnly {
			t.Fatalf("%s mesh_mode: %+v", id, mesh)
		}

		count := parameters["quality_override"]
		if count.Type != "number" || !count.Integer || string(count.Default) != "2000000" ||
			number(count.Minimum) != 500 || number(count.Maximum) != 2000000 || count.Semantic != schema.SemanticPolycount ||
			count.AdjustmentPolicy != "advised" || len(count.MaximumWhen) != 1 {
			t.Fatalf("%s quality_override: %+v", id, count)
		}
		quad := count.MaximumWhen[0]
		var mode string
		if quad.When.Parameter != "mesh_mode" || quad.When.Operator != "equals" || len(quad.AlsoWhen) != 0 ||
			json.Unmarshal(quad.When.Value, &mode) != nil || mode != "Quad" || quad.Maximum != 200000 {
			t.Fatalf("%s quality_override Quad ceiling: %+v", id, quad)
		}

		// The texture effort: the display default is the highest (the old
		// template's value); the other documented mode that is exposed is the
		// tier's own default, 2K and without the extra charge. Lower efforts have
		// the same 2K and the same price (they would only lower the quality) and
		// legacy has no documented resolution, so neither is a choice.
		texture := parameters["texture_mode"]
		if texture.Type != "select" || string(texture.Default) != `"extreme-high"` || len(texture.Options) != 2 ||
			texture.Options[0].Value != "high" || texture.Options[1].Value != "extreme-high" ||
			texture.Semantic != schema.SemanticTextureSize || texture.AdjustmentPolicy != "advised" ||
			texture.Presentation == nil || texture.Presentation.Surface != schema.SurfaceReadOnly {
			t.Fatalf("%s texture_mode: %+v", id, texture)
		}
		// The extreme texture charge follows it (official pricing: only
		// extreme-high adds 2.0); the geometry charge and the base are unconditional.
		var charged []string
		for _, component := range offering.NativePricing.Components {
			if component.When == nil {
				continue
			}
			var mode string
			if component.When.Parameter != "texture_mode" || component.When.Operator != "equals" ||
				json.Unmarshal(component.When.Value, &mode) != nil || mode != "extreme-high" || len(component.WhenAll) != 0 {
				t.Fatalf("%s price condition of %s: %+v", id, component.ID, component)
			}
			charged = append(charged, component.ID)
		}
		if len(charged) != 1 || charged[0] != "extreme_texture" {
			t.Fatalf("%s conditional price components = %v, want only extreme_texture", id, charged)
		}

		for _, name := range []string{"TAPose", "quad_normal"} {
			flag := parameters[name]
			if flag.Type != "boolean" || string(flag.Default) != "false" || flag.Presentation == nil ||
				flag.Presentation.Surface != schema.SurfaceProvider || flag.AdjustmentPolicy != "" ||
				len(flag.OmitWhen) != 1 || flag.OmitWhen[0].Parameter != name || flag.OmitWhen[0].Operator != "falsy" {
				t.Fatalf("%s %s: %+v", id, name, flag)
			}
		}

		seed := parameters["seed"]
		if seed.Type != "number" || !seed.Integer || len(seed.Default) != 0 || seed.Required ||
			number(seed.Minimum) != 0 || number(seed.Maximum) != 65535 || seed.Presentation == nil ||
			seed.Presentation.Surface != schema.SurfaceProvider {
			t.Fatalf("%s seed: %+v", id, seed)
		}

		// What stays closed: the tier, format and the other protocol fields are
		// not public parameters (bbox_condition is a deliberate non-goal, see
		// docs/provider-schema-architecture.md).
		for _, name := range []string{"tier", "quality", "geometry_file_format", "material", "bbox_condition", "uhd_texture", "addons"} {
			if _, declared := parameters[name]; declared {
				t.Fatalf("%s must not expose %s", id, name)
			}
		}
		if _, _, err := product.Normalize(capability, id, rodinInput(t, capability, `{"tier":"Gen-2.5-Low"}`)); err == nil {
			t.Fatalf("%s accepted the closed tier parameter", id)
		}

		// The recorded display wire is still the template: the parameters take
		// over its two fields in place.
		var options struct {
			RequestTemplate map[string]any `json:"request_template"`
		}
		if err := json.Unmarshal(offering.ProviderOptions, &options); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{
			"tier": "Gen-2.5-Extreme-High", "geometry_file_format": "glb", "mesh_mode": "Raw", "quality": "high",
			"quality_override": float64(2000000), "material": "PBR", "texture_mode": "extreme-high",
		}
		if len(options.RequestTemplate) != len(want) {
			t.Fatalf("%s request_template = %v", id, options.RequestTemplate)
		}
		for name, value := range want {
			if options.RequestTemplate[name] != value {
				t.Fatalf("%s request_template[%s] = %v, want %v", id, name, options.RequestTemplate[name], value)
			}
		}
	}
}

func TestHyper3DRodinInputsFollowTheDocumentedRanges(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for capability, id := range rodinOfferingIDs {
		normalize := func(values string) (map[string]any, error) {
			_, normalized, err := product.Normalize(capability, id, rodinInput(t, capability, values))
			if err != nil {
				return nil, err
			}
			var decoded map[string]any
			if err := json.Unmarshal(normalized, &decoded); err != nil {
				t.Fatal(err)
			}
			return decoded, nil
		}

		// The display profile: the defaults are the old template's values and
		// nothing else is switched on.
		display, err := normalize("")
		if err != nil {
			t.Fatalf("%s display: %v", id, err)
		}
		for name, want := range map[string]any{
			"mesh_mode": "Raw", "quality_override": float64(2000000), "texture_mode": "extreme-high",
			"TAPose": false, "quad_normal": false,
		} {
			if display[name] != want {
				t.Fatalf("%s display %s = %v, want %v", id, name, display[name], want)
			}
		}
		if _, present := display["seed"]; present {
			t.Fatalf("%s display carries a seed: %v", id, display)
		}

		// Quad on its own: the Schema default follows the combination (#139), so
		// the highest legal Quad count is used rather than the Raw default.
		quadOnly, err := normalize(`{"mesh_mode":"Quad"}`)
		if err != nil || quadOnly["quality_override"] != float64(200000) {
			t.Fatalf("%s Quad alone = %v (%v), want the 200000 ceiling", id, quadOnly["quality_override"], err)
		}

		for name, values := range map[string]string{
			"a game character":      `{"mesh_mode":"Quad","quality_override":10000,"texture_mode":"high","TAPose":true,"quad_normal":true,"seed":42}`,
			"the high texture":      `{"texture_mode":"high"}`,
			"the display texture":   `{"texture_mode":"extreme-high"}`,
			"the Quad ceiling":      `{"mesh_mode":"Quad","quality_override":200000}`,
			"the lowest count":      `{"mesh_mode":"Quad","quality_override":500}`,
			"the Raw display count": `{"mesh_mode":"Raw","quality_override":2000000}`,
			"a Raw count past Quad": `{"mesh_mode":"Raw","quality_override":250000}`,
			"seed zero":             `{"seed":0}`,
			"the highest seed":      `{"seed":65535}`,
		} {
			if _, err := normalize(values); err != nil {
				t.Fatalf("%s rejected %s: %v", id, name, err)
			}
		}

		for name, values := range map[string]string{
			"Quad above its ceiling":  `{"mesh_mode":"Quad","quality_override":200001}`,
			"Quad at the Raw display": `{"mesh_mode":"Quad","quality_override":2000000}`,
			"below the lowest count":  `{"quality_override":499}`,
			"above the highest count": `{"quality_override":2000001}`,
			"a fractional count":      `{"quality_override":1500.5}`,
			"a count that is a word":  `{"quality_override":"many"}`,
			// The other documented efforts are the same 2K at the same price and
			// legacy has no documented resolution: none of them is a choice.
			"a lower texture effort":          `{"texture_mode":"medium"}`,
			"the lowest texture effort":       `{"texture_mode":"extreme-low"}`,
			"the legacy texture pipeline":     `{"texture_mode":"legacy"}`,
			"a texture mode that is a number": `{"texture_mode":6}`,
			"a seed above the range":          `{"seed":65536}`,
			"a negative seed":                 `{"seed":-1}`,
			"a fractional seed":               `{"seed":1.5}`,
			"an unknown topology":             `{"mesh_mode":"quad"}`,
			"a topology that is a number":     `{"mesh_mode":1}`,
			"a pose switch that is a word":    `{"TAPose":"yes"}`,
			"a bake switch that is a word":    `{"quad_normal":"true"}`,
		} {
			if _, err := normalize(values); err == nil {
				t.Fatalf("%s accepted %s", id, name)
			}
		}

		// The refusal names the Quad ceiling, not a generic failure.
		_, err = normalize(`{"mesh_mode":"Quad","quality_override":200001}`)
		if err == nil || !strings.Contains(err.Error(), "200000") {
			t.Fatalf("%s Quad refusal = %v, want the 200000 ceiling named", id, err)
		}
	}
}

// A Rodin Quad face is about 2.18 triangles in the delivered GLB: the one real run
// (2026-11-13, dev-test, #294 follow-up; mesh_mode Quad, quality_override 10000,
// texture_mode high) delivered a GLB of about 21,800 triangles. The pages do not say
// how a Quad face counts toward quality_override, so this is one sample at one size.
// The Schema declares it once, as 2.2 triangles per Quad face and 1 per Raw face on
// both Offerings (output_triangles, #304), with the sample beside it in the
// topology parameter's description. The game purposes keep explicit Quad counts
// (the product's choice, taken from the sample's scale with a tenth of each budget
// left free); the budget measures and fits a face count with the declared factor,
// which checks those counts when the Catalog loads and converges a count nobody
// gave, and a new sample changes the number in that one place.
func TestRodinOfferingsDeclareTheQuadFactorWithItsSample(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range rodinOfferingIDs {
		offering, _ := product.Get(id)
		estimate := offering.OutputTriangles
		if estimate == nil || estimate.CountParameter != "quality_override" || estimate.TopologyParameter != "mesh_mode" ||
			len(estimate.TrianglesPerFace) != 2 || estimate.TrianglesPerFace["Raw"] != 1 || estimate.TrianglesPerFace["Quad"] != 2.2 {
			t.Fatalf("%s output_triangles = %+v, want Quad 2.2 and Raw 1 on quality_override by mesh_mode", id, estimate)
		}
		if offering.Domain != schema.DomainModel3D {
			t.Fatalf("%s is in the %s domain", id, offering.Domain)
		}
		// The declared factor reaches the same estimate a processing step gets: the
		// real sample (10,000 Quad faces) is 22,000 triangles, the display request
		// (two million Raw faces) two million.
		if got, ok := offering.EstimateOutputTriangles([]byte(`{"mesh_mode":"Quad","quality_override":10000}`)); !ok || got != 22000 {
			t.Fatalf("%s estimates the sample as %d triangles (%v)", id, got, ok)
		}
		if got, ok := offering.EstimateOutputTriangles([]byte(`{}`)); !ok || got != 2000000 {
			t.Fatalf("%s estimates the display request as %d triangles (%v)", id, got, ok)
		}
		// The sample is written beside the number, in both languages, and says it is
		// the only one.
		description := rodinParameters(offering)["mesh_mode"].Description
		for _, want := range []string{"2.2", "2026-11-13", "21,800", "一个样本"} {
			if !strings.Contains(description, want) {
				t.Fatalf("%s mesh_mode description %q does not state %q", id, description, want)
			}
		}
		english := texts.Text("en", description)
		for _, want := range []string{"2.2", "2026-11-13", "21,800", "one sample"} {
			if !strings.Contains(english, want) {
				t.Fatalf("%s English mesh_mode description %q does not state %q", id, english, want)
			}
		}
	}
}

// The game purposes recommend Rodin after the cheaper Meshy and P2 choices (the
// third choice; the only entry after it is the Meshy 7.1 one-step remesh of
// #276, which Meshy itself ranks below Smart Topology for low-poly game assets)
// and carry an explicit Quad count (#294 follow-up). The counts are the product's
// choice, set from the one real sample (10,000 faces): the phone, web and XR
// platforms and a session without a platform share the lower count, and PC and
// console keep the count the run had, so no purpose borrows a large budget to
// enlarge a count nobody measured. The Offering's declared factor (#304) holds
// every count to its platform's triangle budget and, like the Meshy quad targets,
// to nine tenths of it, so a ratio that varies still fits.
const rodinQuadBudgetSharePercent = 90

func TestGameProfilesRecommendRodinWithBudgetedQuadOverrides(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	limit, bounded := usage.BudgetLimit(schema.SemanticPolycount)
	if !bounded {
		t.Fatal("the polycount limit is not declared")
	}
	profiles := map[string]catalog.ChatModel3DUsageProfile{}
	for _, profile := range usage.Profiles {
		profiles[profile.ID] = profile
	}
	for id, want := range map[string]struct {
		pose bool
		// counts are the Quad face counts by target platform option id; "" is a
		// session without a platform.
		counts map[string]float64
	}{
		"game_character": {pose: true, counts: map[string]float64{
			"": 8000, "mobile": 8000, "web": 8000, "xr": 8000, "pc_console": 10000,
		}},
		"game_prop": {counts: map[string]float64{
			"": 3200, "mobile": 3200, "web": 3200, "xr": 3200, "pc_console": 4000,
		}},
	} {
		profile, ok := profiles[id]
		if !ok {
			t.Fatalf("missing profile %s", id)
		}
		for capability, offeringID := range rodinOfferingIDs {
			recommended := profile.RecommendedOfferings[string(capability)]
			if len(recommended) < 3 || recommended[2] != string(offeringID) || len(recommended) > 4 ||
				(len(recommended) == 4 && !strings.HasPrefix(recommended[3], "meshy/meshy-7.1/")) {
				t.Fatalf("%s %s recommends %v, want Rodin third and at most Meshy 7.1 after it", id, capability, recommended)
			}
			offering, _ := product.Get(offeringID)
			count := rodinParameters(offering)["quality_override"]
			if offering.OutputTriangles == nil {
				t.Fatalf("%s declares no output_triangles to hold the purposes' counts to", offeringID)
			}
			factor, declared := offering.OutputTriangles.Factor("Quad")
			if !declared {
				t.Fatalf("%s declares no Quad factor to hold the purposes' counts to", offeringID)
			}
			for platform, wantCount := range want.counts {
				resolved, _ := usage.ForPlatform(profile, platform)
				override := resolved.ParameterOverrides[string(offeringID)]
				var mode string
				var got float64
				var quadNormal, pose bool
				if json.Unmarshal(override["mesh_mode"], &mode) != nil || mode != "Quad" ||
					json.Unmarshal(override["quality_override"], &got) != nil || got != wantCount ||
					json.Unmarshal(override["quad_normal"], &quadNormal) != nil || !quadNormal {
					t.Fatalf("%s on %q: %s override = %v, want Quad at %v faces", id, platform, offeringID, override, wantCount)
				}
				if raw, set := override["TAPose"]; want.pose {
					if !set || json.Unmarshal(raw, &pose) != nil || !pose {
						t.Fatalf("%s on %q: %s must switch TAPose on: %v", id, platform, offeringID, override)
					}
				} else if set {
					t.Fatalf("%s on %q: %s must not set TAPose on a prop: %v", id, platform, offeringID, override)
				}
				for name := range override {
					switch name {
					case "mesh_mode", "quality_override", "texture_mode", "TAPose", "quad_normal":
					default:
						t.Fatalf("%s on %q: %s overrides the unexpected %s", id, platform, offeringID, name)
					}
				}
				// The texture is the highest exposed mode whose documented resolution
				// fits the budget this platform resolves to (2K against 2048 on the
				// phone class, still 2K against the 4096 of PC and console: the 6K
				// display mode never fits a game budget up to 4096), so Rodin's result
				// passes the purpose's texture check instead of failing it.
				textureLimit, bounded := usage.BudgetLimit(schema.SemanticTextureSize)
				textureMaximum, declared := textureLimit.Maximum(resolved.Budget)
				if !bounded || !declared {
					t.Fatalf("%s on %q declares no texture budget to fit", id, platform)
				}
				texture := rodinParameters(offering)["texture_mode"]
				highest, highestAmount := "", 0.0
				for _, option := range texture.Options {
					if amount, known := textureLimit.OptionValues[option.Value]; known && amount <= textureMaximum && amount > highestAmount {
						highest, highestAmount = option.Value, amount
					}
				}
				var chosen string
				if json.Unmarshal(override["texture_mode"], &chosen) != nil || chosen != "high" || chosen != highest {
					t.Fatalf("%s on %q: %s texture_mode = %s, want %q, the highest mode within %v", id, platform, offeringID,
						override["texture_mode"], highest, textureMaximum)
				}
				if measured, ok := textureLimit.Measure(offering, texture, override["texture_mode"], nil); !ok || measured > textureMaximum {
					t.Fatalf("%s on %q: %s texture is %v pixels against the %v budget", id, platform, offeringID, measured, textureMaximum)
				}
				// A Quad face is the declared 2.2 triangles in the GLB: the count must fit
				// the budget the purpose resolves to on this platform once converted, and
				// leave a tenth of it for the ratio to vary. The budget measures it the same
				// way (the rule the Catalog applies when it loads).
				maximum, declared := limit.Maximum(resolved.Budget)
				if !declared {
					t.Fatalf("%s on %q declares no triangle budget", id, platform)
				}
				triangles, measured := limit.Measure(offering, count, override["quality_override"], override)
				if !measured || triangles != schema.TrianglesForFaces(got, factor) {
					t.Fatalf("%s on %q: %s budget measures %v quads as %v (%v), want the declared factor's %v", id, platform, offeringID,
						got, triangles, measured, schema.TrianglesForFaces(got, factor))
				}
				minimum := 0.0
				if resolved.Budget.MinTriangles != nil {
					minimum = float64(*resolved.Budget.MinTriangles)
				}
				if triangles > maximum || triangles < minimum {
					t.Fatalf("%s on %q: %s %v quads are about %v triangles, outside the budget %v-%v", id, platform, offeringID,
						got, triangles, minimum, maximum)
				}
				if triangles*100 > maximum*rodinQuadBudgetSharePercent {
					t.Fatalf("%s on %q: %s %v quads are about %v triangles, leaving less than a tenth of the %v budget for the quad ratio to vary",
						id, platform, offeringID, got, triangles, maximum)
				}
				if _, _, err := product.Normalize(capability, offeringID, rodinInput(t, capability, overrideJSON(t, override))); err != nil {
					t.Fatalf("%s on %q: %s override does not freeze: %v", id, platform, offeringID, err)
				}
			}
		}
		for _, offeringID := range profile.OverridesNotNeeded {
			if strings.HasPrefix(offeringID, "hyper3d/") {
				t.Fatalf("%s lists %s as needing no override", id, offeringID)
			}
		}
	}
	// The display purpose is unchanged: Rodin stays recommended without an override.
	render := profiles["render"]
	if len(render.ParameterOverrides) != 0 {
		t.Fatalf("render must stay without overrides: %v", render.ParameterOverrides)
	}
}

// usageProfileDocument is the decoded model3d_usage profile of a Catalog document.
func usageProfileDocument(t *testing.T, document map[string]any, id string) map[string]any {
	t.Helper()
	usage := document["application"].(map[string]any)["chat"].(map[string]any)["model3d_usage"].(map[string]any)
	for _, raw := range usage["profiles"].([]any) {
		if profile := raw.(map[string]any); profile["id"] == id {
			return profile
		}
	}
	t.Fatalf("profile %q missing", id)
	return nil
}

// A purpose's explicit Quad count is held to the budget at load time (#304): the
// loader converts it with the factor the Offering declares, on every platform the
// count applies to, and refuses a count whose triangles are over the budget. The
// boundaries are the most Quad faces the declared 2.2 lets a budget hold: 20000
// triangles hold 9090 (9090 x 2.2 = 19998), 8000 hold 3636, 100000 hold 45454, 40000
// hold 18181, and one face more breaks the budget.
func TestRodinGameCountsOverTheirBudgetFailToLoad(t *testing.T) {
	set := func(profileID, platform, capability, count string) func(document map[string]any) {
		return func(document map[string]any) {
			profile := usageProfileDocument(t, document, profileID)
			overrides := profile["parameter_overrides"].(map[string]any)
			if platform != "" {
				overrides = nil
				for _, raw := range profile["platforms"].([]any) {
					variant := raw.(map[string]any)
					for _, option := range variant["ids"].([]any) {
						if option == platform {
							overrides = variant["parameter_overrides"].(map[string]any)
						}
					}
				}
				if overrides == nil {
					t.Fatalf("%s declares no overrides for the %s platform", profileID, platform)
				}
			}
			overrides["hyper3d/rodin-gen-2.5/"+capability].(map[string]any)["quality_override"] = json.Number(count)
		}
	}
	for _, test := range []struct {
		name, profile, platform, count string
		loads                          bool
	}{
		{"a character on a phone at the most the budget holds", "game_character", "", "9090", true},
		{"a character on a phone, one face more", "game_character", "", "9091", false},
		{"a character on PC and console at the most", "game_character", "pc_console", "45454", true},
		{"a character on PC and console, one face more", "game_character", "pc_console", "45455", false},
		{"a prop on a phone at the most", "game_prop", "", "3636", true},
		{"a prop on a phone, one face more", "game_prop", "", "3637", false},
		{"a prop on PC and console at the most", "game_prop", "pc_console", "18181", true},
		{"a prop on PC and console, one face more", "game_prop", "pc_console", "18182", false},
	} {
		for _, capability := range []string{"text-to-3d", "image-to-3d"} {
			t.Run(test.name+" ("+capability+")", func(t *testing.T) {
				_, err := loadMutatedCatalog(t, set(test.profile, test.platform, capability, test.count))
				switch {
				case test.loads && err != nil:
					t.Fatalf("a count inside the budget was refused: %v", err)
				case !test.loads && (err == nil || !strings.Contains(err.Error(), "超出用途预算")):
					t.Fatalf("a count over the budget loaded or failed for another reason: %v", err)
				}
			})
		}
	}
}

// The explicit counts follow the declared factor (#304): a new sample that raises
// the ratio makes counts chosen for 2.2 too large for the budgets they were chosen
// under, and the Catalog refuses to load until they are chosen again. At 2.5 the
// phone budgets are exactly filled (8000 x 2.5 = 20000, 3200 x 2.5 = 8000), a hair
// more and they are not.
func TestRodinFactorChangeIsHeldAgainstTheExplicitCounts(t *testing.T) {
	withQuadFactor := func(factor string) error {
		_, err := loadMutatedCatalog(t, func(document map[string]any) {
			for _, capability := range []string{"text-to-3d", "image-to-3d"} {
				estimate := catalogOffering(t, document, "hyper3d/rodin-gen-2.5/"+capability)["output_triangles"].(map[string]any)
				estimate["triangles_per_face"].(map[string]any)["Quad"] = json.Number(factor)
			}
		})
		return err
	}
	for _, factor := range []string{"2.2", "2.18", "2.5"} {
		if err := withQuadFactor(factor); err != nil {
			t.Fatalf("a factor of %s was refused: %v", factor, err)
		}
	}
	if err := withQuadFactor("2.51"); err == nil || !strings.Contains(err.Error(), "超出用途预算") {
		t.Fatalf("explicit counts over the budget under a factor of 2.51 loaded or failed for another reason: %v", err)
	}
}

func overrideJSON(t *testing.T, override map[string]json.RawMessage) string {
	t.Helper()
	encoded, err := json.Marshal(override)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// quality_override carries the polycount semantic, so a purpose with a lower
// triangle budget than the display default fits it the way it fits Tripo's face
// limit and Meshy's target count (#129), without an override. A Raw count is a
// triangle count; a Quad count is fitted in triangles with the factor the
// Offering declares (#304), whichever purpose the budget belongs to.
func TestRodinFaceCountFitsAPurposeBudget(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	limit, bounded := usage.BudgetLimit(schema.SemanticPolycount)
	if !bounded {
		t.Fatal("the polycount limit is not declared")
	}
	quad := map[string]json.RawMessage{"mesh_mode": json.RawMessage(`"Quad"`)}
	for _, id := range rodinOfferingIDs {
		offering, _ := product.Get(id)
		parameter := rodinParameters(offering)["quality_override"]
		value, fitted := limit.Fit(offering, parameter, parameter.Default, nil, 100000)
		if !fitted || string(value) != "100000" {
			t.Fatalf("%s default fitted to a 100000 budget = %s (%v)", id, value, fitted)
		}
		if _, fitted := limit.Fit(offering, parameter, parameter.Default, nil, 2000000); fitted {
			t.Fatalf("%s display default does not fit the display budget", id)
		}
		// The Quad ceiling (200000 faces, 440000 triangles) against budgets of every
		// purpose the Schema declares: the web and AR purpose's 100000 holds 45454
		// Quad faces, a 2000000 display budget holds all of them.
		for budget, want := range map[float64]string{100000: "45454", 50000: "22727", 20000: "9090", 8000: "3636", 440000: "", 2000000: ""} {
			value, fitted := limit.Fit(offering, parameter, json.RawMessage(`200000`), quad, budget)
			if want == "" {
				if fitted {
					t.Fatalf("%s Quad ceiling fitted to a %v budget it fits: %s", id, budget, value)
				}
				continue
			}
			if !fitted || string(value) != want {
				t.Fatalf("%s Quad ceiling fitted to a %v budget = %s (%v), want %s", id, budget, value, fitted, want)
			}
		}
	}
}

// The texture effort is a purpose-budget parameter too (#279). The documented
// resolutions (read 2026-11-12): the high mode is 2K and extreme-high is 6K
// without HighPack, so a game purpose's 2048 texture budget takes high, the
// budget of a print or high-end purpose (4096) still takes high, and only a
// budget of 6144 or more keeps the display mode. K is 1024 pixels, as in the
// 6144-pixel and 12288-pixel maps measured on Rodin results (see
// docs/provider-schema-architecture.md).
func TestRodinTextureModeFitsAPurposeBudget(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	usage := product.Application().Chat.Model3DUsage
	limit, bounded := usage.BudgetLimit(schema.SemanticTextureSize)
	if !bounded {
		t.Fatal("the texture-size limit is not declared")
	}
	for _, id := range rodinOfferingIDs {
		offering, _ := product.Get(id)
		parameter := rodinParameters(offering)["texture_mode"]
		for name, amount := range map[string]float64{"high": 2048, "extreme-high": 6144} {
			if got, measured := limit.Measure(offering, parameter, json.RawMessage(`"`+name+`"`), nil); !measured || got != amount {
				t.Fatalf("%s %s measures %v (%v), want %v", id, name, got, measured, amount)
			}
		}
		for budget, want := range map[float64]string{2048: `"high"`, 4096: `"high"`, 6143: `"high"`, 1024: `"high"`} {
			value, fitted := limit.Fit(offering, parameter, parameter.Default, nil, budget)
			if !fitted || string(value) != want {
				t.Fatalf("%s display texture fitted to a %v budget = %s (%v), want %s", id, budget, value, fitted, want)
			}
		}
		for _, budget := range []float64{6144, 8192} {
			if value, fitted := limit.Fit(offering, parameter, parameter.Default, nil, budget); fitted {
				t.Fatalf("%s display texture changed to %s under a %v budget it fits", id, value, budget)
			}
		}
	}
}

// The quote follows the texture effort as the official pricing does (read
// 2026-11-12): base 0.5, the Extreme-High tier +0.5 and texture_mode extreme-high
// +2.0, with parameters not listed adding nothing. The display request keeps its
// quote of 3 (the old unconditional price); a 2K texture is 1. The price of the
// non-default modes is from the page and not yet confirmed by a real receipt.
func TestRodinQuoteFollowsTheTextureMode(t *testing.T) {
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for capability, id := range rodinOfferingIDs {
		offering, _ := product.Get(id)
		quote := func(values string) *schema.NativeQuote {
			t.Helper()
			_, normalized, err := product.Normalize(capability, id, rodinInput(t, capability, values))
			if err != nil {
				t.Fatalf("%s %s: %v", id, values, err)
			}
			quote, err := offering.EvaluateNativeQuote(normalized)
			if err != nil {
				t.Fatalf("%s %s: %v", id, values, err)
			}
			return quote
		}
		for name, test := range map[string]struct {
			values, total, extra string
			components           []string
		}{
			"the display request":         {``, "3", "2.5", []string{"extreme_geometry", "extreme_texture"}},
			"the extreme texture":         {`{"texture_mode":"extreme-high"}`, "3", "2.5", []string{"extreme_geometry", "extreme_texture"}},
			"the high texture":            {`{"texture_mode":"high"}`, "1", "0.5", []string{"extreme_geometry"}},
			"Quad leaves the price":       {`{"mesh_mode":"Quad","TAPose":true,"quad_normal":true,"seed":1}`, "3", "2.5", []string{"extreme_geometry", "extreme_texture"}},
			"a game purpose's override":   {`{"mesh_mode":"Quad","quality_override":10000,"texture_mode":"high","TAPose":true,"quad_normal":true}`, "1", "0.5", []string{"extreme_geometry"}},
			"a lower count leaves it too": {`{"quality_override":60000}`, "3", "2.5", []string{"extreme_geometry", "extreme_texture"}},
		} {
			got := quote(test.values)
			var components []string
			for _, component := range got.Components {
				components = append(components, component.ID)
			}
			if got.BaseAmount != "0.5" || got.TotalAmount != test.total || got.ExtraAmount != test.extra ||
				strings.Join(components, ",") != strings.Join(test.components, ",") {
				t.Fatalf("%s %s quote = %+v, want total %s extra %s %v", id, name, got, test.total, test.extra, test.components)
			}
		}

		// The preview and upper estimates are the display price, as before.
		if preview, err := offering.DefaultNativeQuote(nil); err != nil || preview.TotalAmount != "3" || preview.ExtraAmount != "2.5" {
			t.Fatalf("%s preview quote = %+v (%v)", id, preview, err)
		}
		if upper, err := offering.UpperNativeQuote(); err != nil || upper.TotalAmount != "3" {
			t.Fatalf("%s upper quote = %+v (%v)", id, upper, err)
		}
		// An input that does not name the texture mode is not priced: the quote
		// is evaluated on the normalized input, which always carries it.
		if _, err := offering.EvaluateNativeQuote(rodinInput(t, capability, "")); err == nil {
			t.Fatalf("%s priced an input without the texture mode", id)
		}
		// Every quote still has an extra part, so the consent flag stays required
		// for the 2K texture too.
		withoutConsent := `{"prompt":"a small robot","texture_mode":"high"}`
		if capability == schema.ImageTo3D {
			withoutConsent = `{"prompt":"a small robot","reference_assets":["reference"],"texture_mode":"high"}`
		}
		if _, _, err := product.Normalize(capability, id, json.RawMessage(withoutConsent)); err == nil {
			t.Fatalf("%s accepted the 2K texture without the extra-price consent", id)
		}
	}
}
