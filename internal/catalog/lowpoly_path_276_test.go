package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/catalog/catalogtest"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/schema"
)

// #276: the way from a high-detail model to a game low-poly model that keeps its
// textures. Tripo's baked decimation takes the high-detail Tripo v3.1 results
// (the official page, read 2026-11-12, lists text, image and multiview
// generation tasks as the task input); the game purposes declare that route and
// the one-step Meshy 7.1 remesh; a source the decimation does not take is
// refused with the route to take instead.

const (
	decimateTriangleID = "tripo/geometry/decimate-triangle"
	decimateQuadID     = "tripo/geometry/decimate-quad"
)

func shippedProduct(t *testing.T) *catalog.Catalog {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return product
}

func shippedPurpose(t *testing.T, product *catalog.Catalog, id string) catalog.ChatModel3DUsageProfile {
	t.Helper()
	for _, profile := range product.Application().Chat.Model3DUsage.Profiles {
		if profile.ID == id {
			return profile
		}
	}
	t.Fatalf("purpose %q is not declared", id)
	return catalog.ChatModel3DUsageProfile{}
}

func sourceTaskParameter(t *testing.T, offering generation.Offering) generation.Parameter {
	t.Helper()
	for _, parameter := range offering.Parameters {
		if parameter.Semantic == schema.SemanticSourceTask {
			return parameter
		}
	}
	t.Fatalf("%s declares no source task", offering.ID)
	return generation.Parameter{}
}

// The decimation page lists text_to_model, image_to_model and multiview_to_model
// tasks as the task input and names no model version of the source, so the
// multiview result of Tripo v3.1 is a source. It says nothing about a P-series
// task, so P2 stays out (the official page is silent, not clear); every other
// service stays out too.
func TestDecimationTakesMultiviewResultsButNotP2OrOtherServices(t *testing.T) {
	product := shippedProduct(t)
	for _, id := range []string{decimateTriangleID, decimateQuadID} {
		decimate, ok := product.Get(generation.OfferingID(id))
		if !ok {
			t.Fatalf("%s is not in the Catalog", id)
		}
		for source, accepted := range map[string]bool{
			"tripo/tripo-v3.1-full/text-to-3d":       true,
			"tripo/tripo-v3.1-full/image-to-3d":      true,
			"tripo/tripo-v3.1-multiview/image-to-3d": true,
			"tripo/tripo-p2/text-to-3d":              false,
			"tripo/tripo-p2/image-to-3d":             false,
			"tripo/tripo-p2-multiview/image-to-3d":   false,
			"meshy/meshy-7.1/text-to-3d":             false,
			"meshy/meshy-t2.5/image-to-3d":           false,
			"hyper3d/rodin-gen-2.5/image-to-3d":      false,
		} {
			sourceOffering, ok := product.Get(generation.OfferingID(source))
			if !ok {
				t.Fatalf("%s is not in the Catalog", source)
			}
			_, err := generation.BindProjectedModelSource(decimate, sourceOffering)
			if (err == nil) != accepted {
				t.Errorf("%s as a source of %s: accepted = %v, want %v (%v)", source, id, err == nil, accepted, err)
			}
		}
		// Task capabilities already covered the multiview Offering (an image-to-3d
		// Offering with views), so only the exact Offering list moved.
		parameter := sourceTaskParameter(t, decimate)
		if !slices.Equal(parameter.TaskCapabilities, []generation.Capability{"text-to-3d", "image-to-3d"}) {
			t.Errorf("%s task capabilities = %v", id, parameter.TaskCapabilities)
		}
		// The disabled quad Offering has no Offering description of its own.
		descriptions := []string{parameter.Description}
		if decimate.Enabled {
			descriptions = append(descriptions, decimate.Description)
		}
		for _, description := range descriptions {
			if !strings.Contains(description, "多视图") || !strings.Contains(description, "P2") {
				t.Errorf("%s: the copy must say that multiview results are taken and P2 results are not: %q", id, description)
			}
		}
	}
}

// Conclusion of the #276 re-check, kept as a test so it is not lost. The
// Retopology page (read 2026-11-12) now has one format line, under v2.0: "outputs
// GLB; FBX when quad=true". For v3.0, the model the Offering uses, it still states
// no output format. The one real v3.0 quad=true task (#185) delivered a binary
// FBX, which agrees with the v2.0 line but is not a v3.0 contract, and the
// Offering's required model slot is a GLB. Redefining the contract around an FBX
// would rest on one observation plus a line about another model version, so the
// quad decimation stays disabled until the page states what v3.0 delivers.
func TestDecimateQuadStaysDisabledUntilTheOutputFormatIsStated(t *testing.T) {
	product := shippedProduct(t)
	quad, ok := product.Get(decimateQuadID)
	if !ok || quad.Enabled {
		t.Fatalf("decimate-quad = %+v, want it declared and disabled", quad)
	}
	glbRequired := false
	for _, output := range quad.Outputs {
		if output.Role == "model" && output.Variant == "glb" && output.Minimum != nil && *output.Minimum == 1 {
			glbRequired = true
		}
		if output.Variant == "fbx" && output.Role == "model" {
			t.Errorf("the quad decimation declares a native FBX model slot: %+v", output)
		}
	}
	if !glbRequired {
		t.Error("the quad decimation no longer requires its GLB; re-check the official page before changing the contract")
	}
	if triangle, ok := product.Get(decimateTriangleID); !ok || !triangle.Enabled || triangle.Default {
		t.Fatalf("decimate-triangle = %+v, want it enabled and not the default", triangle)
	}
}

// A source the baked decimation does not take is refused before anything is
// paid, and the refusal says what to do instead (the route is Schema copy on the
// source parameter; the kernel test for the whole refusal is in
// internal/generation and internal/app).
func TestDecimationSourceDeclaresTheRouteToTakeInstead(t *testing.T) {
	product := shippedProduct(t)
	texts, err := catalog.NewTextLocalizer(product.Application())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{decimateTriangleID, decimateQuadID} {
		decimate, _ := product.Get(generation.OfferingID(id))
		alternative := sourceTaskParameter(t, decimate).TaskSourceAlternative
		for _, want := range []string{"Tripo v3.1", "带烘焙的减面", "Meshy", "重拓扑", "换贴图", "P2"} {
			if !strings.Contains(alternative, want) {
				t.Errorf("%s: the route %q does not say %q", id, alternative, want)
			}
		}
		english := texts.Text("en", alternative)
		for _, want := range []string{"Tripo v3.1", "Meshy remesh", "retexturing", "P2"} {
			if !strings.Contains(english, want) {
				t.Errorf("%s: the English route %q does not say %q", id, english, want)
			}
		}
		if strings.ContainsAny(english, "{}") || english == alternative {
			t.Errorf("%s: the route has no English text: %q", id, english)
		}
	}
	// The refusal template has a place for it, in both languages, and only the
	// two decimations declare one: the route differs from step to step.
	application := product.Application()
	if !strings.Contains(application.Runtime.GenerationMessages.InputSourceTaskUnsupported, "{alternative}") {
		t.Errorf("the refusal template has no {alternative}: %q", application.Runtime.GenerationMessages.InputSourceTaskUnsupported)
	}
	englishTemplate := application.Localization.Bundles["en"].Fields["runtime.generation_messages.input_source_task_unsupported"]
	if !strings.Contains(englishTemplate, "{alternative}") {
		t.Errorf("the English refusal template has no {alternative}: %q", englishTemplate)
	}
	for _, offering := range product.List("") {
		for _, parameter := range offering.Parameters {
			declares := parameter.TaskSourceAlternative != ""
			isDecimation := offering.ID == decimateTriangleID || offering.ID == decimateQuadID
			if declares && !(isDecimation && parameter.Name == "source_task") {
				t.Errorf("%s declares a route for %s", offering.ID, parameter.Name)
			}
		}
	}
}

func TestTaskSourceAlternativeIsValidated(t *testing.T) {
	// A test changes the copy, so its translation moves with it: Load rejects a
	// missing or an unused translation before it reaches the check under test.
	setAlternative := func(document map[string]any, offering, parameter, text string) {
		for _, raw := range catalogOffering(t, document, offering)["parameters"].([]any) {
			if entry := raw.(map[string]any); entry["name"] == parameter {
				entry["task_source_alternative"] = text
			}
		}
		bundle := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)
		translation := "Use another route."
		if strings.Contains(text, "{label}") {
			translation += " {label}"
		}
		bundle["offering_texts"].(map[string]any)[text] = translation
		catalogtest.DropUnusedOfferingTexts(document)
	}
	for name, tc := range map[string]struct {
		mutate func(document map[string]any)
		wantOK bool
	}{
		"a plain route on a task source": {func(document map[string]any) {
			setAlternative(document, decimateTriangleID, "source_task", "改用别的服务。")
		}, true},
		"on a parameter that is not a task": {func(document map[string]any) {
			setAlternative(document, decimateTriangleID, "face_limit", "改用别的服务。")
		}, false},
		"with a placeholder of the refusal template": {func(document map[string]any) {
			setAlternative(document, decimateTriangleID, "source_task", "改用 {label} 以外的服务。")
		}, false},
		"with surrounding white space": {func(document map[string]any) {
			setAlternative(document, decimateTriangleID, "source_task", " 改用别的服务。")
		}, false},
		"longer than a sentence or two": {func(document map[string]any) {
			setAlternative(document, decimateTriangleID, "source_task", strings.Repeat("改用别的服务。", 80))
		}, false},
		"without a place in the refusal template": {func(document map[string]any) {
			plain := "来源任务 {label} 不满足这一步的来源要求；这一步没有提交，也没有扣积分。请重新选择来源。"
			plainEnglish := "Source {label} does not meet this step's source requirements. This step was not submitted and no credits were spent. Select another source."
			messages := document["application"].(map[string]any)["runtime"].(map[string]any)["generation_messages"].(map[string]any)
			messages["input_source_task_unsupported"] = plain
			bundle := document["application"].(map[string]any)["localization"].(map[string]any)["bundles"].(map[string]any)["en"].(map[string]any)
			bundle["fields"].(map[string]any)["runtime.generation_messages.input_source_task_unsupported"] = plainEnglish
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadMutatedCatalog(t, tc.mutate)
			if tc.wantOK && err != nil {
				t.Fatalf("a valid route was refused: %v", err)
			}
			if !tc.wantOK {
				if err == nil {
					t.Fatal("an invalid route was accepted")
				}
				if !strings.Contains(err.Error(), "task_source_alternative") && !strings.Contains(err.Error(), "{alternative}") {
					t.Fatalf("the refusal does not name the field: %v", err)
				}
			}
		})
	}
}

// A quad remesh of Meshy 7.1 delivered about 2.25 triangles per face of the
// target (observed 2026-11-12, #294): target_polycount 10000 with topology quad
// gave a GLB of 22540 triangles, from an FBX of 11401 faces that were 98% quads.
// The pages do not say how a quad counts toward the target, so this is one real
// sample at one size, kept in percent so the arithmetic stays in integers. The
// shipped quad targets keep a tenth of the budget free for the ratio to vary.
const (
	meshyQuadTrianglesPerTargetPercent = 225
	meshyQuadBudgetSharePercent        = 90
)

// Meshy 7.1 in the game purposes generates the low-poly model in one step: the
// automatic remesh, the topology and the target face count are Schema overrides
// of the purpose, per target platform, and their face counts fit the budget they
// are resolved against. The Text to 3D and Image to 3D pages (read 2026-11-12)
// document should_remesh, topology (quad or triangle) and target_polycount
// (100-300000 for a remeshed standard model); a quad target counts for about 2.25
// triangles per face (see meshyQuadTrianglesPerTargetPercent), a triangle target
// for one.
func TestGameProfilesGenerateMeshy71InOneRemeshedStep(t *testing.T) {
	product := shippedProduct(t)
	usage := product.Application().Chat.Model3DUsage
	if !usage.OverrideCoverageRequired {
		t.Fatal("the shipped Catalog must keep override_coverage_required on")
	}
	type expectation struct {
		topology string
		pose     string
		targets  map[string]int // by platform option id; "" is a session without a platform
	}
	for purpose, want := range map[string]expectation{
		"game_character": {"quad", "a-pose", map[string]int{"": 8000, "mobile": 8000, "web": 8000, "xr": 8000, "pc_console": 40000}},
		"game_prop":      {"triangle", "", map[string]int{"": 8000, "mobile": 8000, "web": 8000, "xr": 8000, "pc_console": 8000}},
	} {
		profile := shippedPurpose(t, product, purpose)
		for shape, id := range map[string]string{"text-to-3d": "meshy/meshy-7.1/text-to-3d", "image-to-3d": "meshy/meshy-7.1/image-to-3d"} {
			recommended := profile.RecommendedOfferings[shape]
			// Meshy's own guidance for low-poly game assets is Smart Topology, and
			// the texture of a remeshed task has not been checked against a real
			// result: the one-step remesh is the last recommendation.
			if len(recommended) < 2 || recommended[len(recommended)-1] != id {
				t.Errorf("%s %s recommends %v, want %s last", purpose, shape, recommended, id)
			}
			offering, ok := product.Get(generation.OfferingID(id))
			if !ok {
				t.Fatalf("%s is not in the Catalog", id)
			}
			targetBounds := declaredNumberRange(t, offering, "target_polycount")
			for platform, target := range want.targets {
				resolved, _ := usage.ForPlatform(profile, platform)
				override := resolved.ParameterOverrides[id]
				var remesh bool
				var topology, texture, pose string
				var count float64
				if json.Unmarshal(override["should_remesh"], &remesh) != nil || !remesh ||
					json.Unmarshal(override["topology"], &topology) != nil || topology != want.topology ||
					json.Unmarshal(override["target_polycount"], &count) != nil || int(count) != target ||
					json.Unmarshal(override["texture_resolution"], &texture) != nil || texture != "2k" {
					t.Errorf("%s on %q: %s override = %v, want remesh to %s at %d with 2k textures", purpose, platform, id, override, want.topology, target)
				}
				if raw, set := override["pose_mode"]; want.pose != "" {
					if !set || json.Unmarshal(raw, &pose) != nil || pose != want.pose {
						t.Errorf("%s on %q: %s pose_mode = %s, want %s", purpose, platform, id, raw, want.pose)
					}
				} else if set {
					t.Errorf("%s on %q: %s sets a pose for a prop: %s", purpose, platform, id, raw)
				}
				for name := range override {
					switch name {
					case "should_remesh", "topology", "target_polycount", "texture_resolution", "pose_mode":
					default:
						t.Errorf("%s on %q: %s overrides the unexpected %s", purpose, platform, id, name)
					}
				}
				if float64(target) < targetBounds[0] || float64(target) > targetBounds[1] {
					t.Errorf("%s on %q: %d is outside Meshy's %v face range", purpose, platform, target, targetBounds)
				}
				// A quad target delivers about 2.25 triangles per face in the GLB; the
				// estimate must fit the budget the purpose resolves to on that platform,
				// with a tenth of it to spare for the ratio to vary.
				triangles := target
				if topology == "quad" {
					triangles = target * meshyQuadTrianglesPerTargetPercent / 100
				}
				budget := resolved.Budget
				if triangles > budget.MaxTriangles || (budget.MinTriangles != nil && triangles < *budget.MinTriangles) {
					t.Errorf("%s on %q: %d triangles are outside the %v..%d budget", purpose, platform, triangles, budget.MinTriangles, budget.MaxTriangles)
				}
				if topology == "quad" && triangles*100 > budget.MaxTriangles*meshyQuadBudgetSharePercent {
					t.Errorf("%s on %q: %d triangles leave less than a tenth of the %d budget for the quad ratio to vary",
						purpose, platform, triangles, budget.MaxTriangles)
				}
				if budget.MaxTextureSize == nil || 2048 > *budget.MaxTextureSize {
					t.Errorf("%s on %q: 2K textures do not fit the budget %v", purpose, platform, budget.MaxTextureSize)
				}
			}
		}
	}
}

// declaredNumberRange is the declared minimum and maximum of a number parameter.
func declaredNumberRange(t *testing.T, offering generation.Offering, name string) [2]float64 {
	t.Helper()
	for _, parameter := range offering.Parameters {
		if parameter.Name == name && parameter.Minimum != nil && parameter.Maximum != nil {
			return [2]float64{*parameter.Minimum, *parameter.Maximum}
		}
	}
	t.Fatalf("%s declares no range for %s", offering.ID, name)
	return [2]float64{}
}

// The game purposes name the baked decimation as the step after a high-detail
// generation, and rigging after it for a character. The list is reference
// copy: nothing reads it at runtime, so what the Agent follows is in the craft
// prompt, which is checked below.
func TestGameProfilesDeclareTheBakedDecimationBeforeRigging(t *testing.T) {
	product := shippedProduct(t)
	for purpose, want := range map[string][]string{
		"game_character": {"remesh", "rigging", "animation"},
		"game_prop":      {"remesh", "mesh-segmentation"},
	} {
		pipeline := shippedPurpose(t, product, purpose).Pipeline
		var got []string
		for index, step := range pipeline {
			got = append(got, step.Capability)
			if step.Capability != "remesh" {
				continue
			}
			if index != 0 || step.OfferingID != decimateTriangleID {
				t.Errorf("%s: the reduction step is %+v at %d, want the baked decimation first", purpose, step, index)
			}
			offering, ok := product.Get(generation.OfferingID(step.OfferingID))
			if !ok || !offering.Enabled || string(offering.Capability) != step.Capability {
				t.Errorf("%s: pipeline step %+v does not name an enabled %s Offering", purpose, step, step.Capability)
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s pipeline = %v, want %v", purpose, got, want)
		}
	}
	// The purposes that do not reduce a high-detail model keep their own list.
	if len(shippedPurpose(t, product, "web_ar").Pipeline) != 0 {
		t.Error("web_ar declares a pipeline")
	}
}

func TestCraftPromptTeachesTheHighDetailRoute(t *testing.T) {
	product := shippedProduct(t)
	var text string
	for _, prompt := range product.Application().Chat.Agent.CraftPrompts {
		if prompt.ID == "model3d" {
			text = prompt.Text
		}
	}
	for _, want := range []string{
		"高模细节加游戏面数", "Tripo v3.1", "set_params", "face_limit", "smart_low_poly", "带烘焙的减面", "自动展开 UV",
		"Tripo P2、Meshy、Rodin 的结果和上传文件不接带烘焙的减面", "Meshy 重拓扑", "换贴图", "绑骨",
		"Meshy 官方更推荐低模游戏资产用智能拓扑",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the 3D craft prompt does not say %q", want)
		}
	}
}
