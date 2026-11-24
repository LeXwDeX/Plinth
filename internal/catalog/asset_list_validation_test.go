package catalog_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #327 PR1：默认 Catalog 声明制作清单的类型、路线、状态与文案，公开投影只放
// 前端需要的 id/label/role 与缺省路线。
func TestDefaultAssetListSchemaLoadsAndProjectsSafeMembers(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	list := application.AssetList
	if list == nil {
		t.Fatal("default Catalog has no application.asset_list")
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default asset_list invalid: %v", err)
	}
	if limits := list.Limits; limits.MaxListsPerProject != 20 || limits.DraftTTLSeconds != 604800 ||
		limits.NameMaxLength != 120 || limits.DescriptionMaxLength != 1000 || limits.MaxReferencesPerItem != 4 {
		t.Fatalf("limits = %+v", limits)
	}
	// 项数上限复用 project.limits.maximum_list_items，asset_list 不再声明同名上限。
	if application.Project == nil || application.Project.Limits.MaximumListItems <= 0 {
		t.Fatalf("project item limit = %+v", application.Project)
	}

	expectedKinds := map[string]struct{ projectKind, route string }{
		"character": {"character", "text-to-3d"},
		"weapon":    {"prop", "text-to-3d"},
		"prop":      {"prop", "text-to-3d"},
		"vehicle":   {"other", "text-to-3d"},
		"scene":     {"other", "text-to-3d"},
		"image":     {"other", "text-to-image"},
	}
	if len(list.Kinds) != len(expectedKinds) {
		t.Fatalf("kinds = %+v", list.Kinds)
	}
	for _, kind := range list.Kinds {
		want, exists := expectedKinds[kind.ID]
		if !exists || kind.ProjectKind != want.projectKind || kind.DefaultRoute != want.route {
			t.Fatalf("kind %+v does not match the product mapping %+v", kind, want)
		}
	}

	capabilities := map[string]bool{}
	for _, capability := range application.Capabilities {
		capabilities[string(capability.ID)] = true
	}
	multiStep := false
	for _, route := range list.Routes {
		if len(route.Steps) == 0 {
			t.Fatalf("route %q has no steps", route.ID)
		}
		if len(route.Steps) > 1 {
			multiStep = true
		}
		for _, step := range route.Steps {
			if !capabilities[string(step.Capability)] {
				t.Fatalf("route %q step %q is not a declared capability", route.ID, step.Capability)
			}
		}
	}
	if !multiStep {
		t.Fatal("no route declares a capability sequence")
	}

	expectedStates := []string{"draft", "confirmed", "rejected", "expired", "superseded"}
	if ids := assetListStateIDs(list.States); !slices.Equal(ids, expectedStates) {
		t.Fatalf("states = %v", ids)
	}
	expectedItemStates := []string{"todo", "working", "review", "accepted", "returned", "failed", "skipped"}
	if ids := assetListStateIDs(list.ItemStates); !slices.Equal(ids, expectedItemStates) {
		t.Fatalf("item_states = %v", ids)
	}
	for _, state := range append(append([]catalog.AssetListStateSchema(nil), list.States...), list.ItemStates...) {
		if state.Label == "" || state.Role == "" {
			t.Fatalf("state %+v lacks a label or role", state)
		}
	}
	blocks := []string{}
	for _, block := range list.ItemBlocks {
		blocks = append(blocks, block.Value)
	}
	if !slices.Equal(blocks, []string{"awaiting_approval", "step_cap"}) {
		t.Fatalf("item_blocks = %v", blocks)
	}

	public := application.Public().AssetList
	if public == nil {
		t.Fatal("public projection lost application.asset_list")
	}
	if len(public.Kinds) != len(list.Kinds) || public.Kinds[0].DefaultRoute == "" {
		t.Fatalf("public kinds = %+v", public.Kinds)
	}
	if len(public.Routes) != len(list.Routes) || len(public.States) != len(list.States) ||
		len(public.ItemStates) != len(list.ItemStates) {
		t.Fatalf("public asset list = %+v", public)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"messages", "item_blocks", "card_title", "step_cap_warning", "确认清单"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public asset_list leaks %q: %s", private, encoded)
		}
	}
}

func assetListStateIDs(states []catalog.AssetListStateSchema) []string {
	ids := make([]string, 0, len(states))
	for _, state := range states {
		ids = append(ids, state.ID)
	}
	return ids
}

func TestAssetListValidationRejectsIncompleteOrUnknownDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"step policy missing": func(app *catalog.ApplicationSchema) {
			app.StepPolicy = nil
		},
		"zero list limit": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.MaxListsPerProject = 0
		},
		"zero draft ttl": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.DraftTTLSeconds = 0
		},
		"zero name limit": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.NameMaxLength = 0
		},
		"negative description limit": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.DescriptionMaxLength = -1
		},
		"zero reference limit": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.MaxReferencesPerItem = 0
		},
		"too many references": func(app *catalog.ApplicationSchema) {
			app.AssetList.Limits.MaxReferencesPerItem = 9
		},
		"empty kinds": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds = nil
		},
		"unknown kind": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[0].ID = "monster"
		},
		"duplicate kind": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[1].ID = app.AssetList.Kinds[0].ID
		},
		"kind label missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[0].Label = " "
		},
		"invalid project kind": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[0].ProjectKind = "weapon"
		},
		"unknown default route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[0].DefaultRoute = "text-to-2d"
		},
		"default route does not serve the kind": func(app *catalog.ApplicationSchema) {
			app.AssetList.Kinds[0].DefaultRoute = "text-to-image"
		},
		"empty routes": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes = nil
		},
		"duplicate route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[1].ID = app.AssetList.Routes[0].ID
		},
		"route label missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[0].Label = ""
		},
		"route kinds empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[0].Kinds = nil
		},
		"route references unknown kind": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[0].Kinds = append(app.AssetList.Routes[0].Kinds, "monster")
		},
		"route steps empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[0].Steps = nil
		},
		"route references unknown capability": func(app *catalog.ApplicationSchema) {
			app.AssetList.Routes[0].Steps[0].Capability = "generate_image"
		},
		"state missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.States = app.AssetList.States[:len(app.AssetList.States)-1]
		},
		"state unknown": func(app *catalog.ApplicationSchema) {
			app.AssetList.States[0].ID = "archived"
		},
		"state duplicate": func(app *catalog.ApplicationSchema) {
			app.AssetList.States[1].ID = app.AssetList.States[0].ID
		},
		"state role missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.States[0].Role = ""
		},
		"item state missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.ItemStates = app.AssetList.ItemStates[:len(app.AssetList.ItemStates)-1]
		},
		"item state unknown": func(app *catalog.ApplicationSchema) {
			app.AssetList.ItemStates[0].ID = "done"
		},
		"item block not reserved": func(app *catalog.ApplicationSchema) {
			app.AssetList.ItemBlocks = append(app.AssetList.ItemBlocks, catalog.ValueLabel{Value: "blocked", Label: "受阻"})
		},
		"item block label missing": func(app *catalog.ApplicationSchema) {
			app.AssetList.ItemBlocks[0].Label = ""
		},
		"message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Messages.Confirmed = " "
		},
		"ui empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.UI.EmptyText = ""
		},
		"project asset lists route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetLists = ""
		},
		"project asset list route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetList = ""
		},
		"decision route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListDecision = ""
		},
		"board route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListBoard = ""
		},
		"session asset lists route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = ""
		},
		"poll interval too small": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.PollIntervalMilliseconds = 499
		},
		"poll interval too large": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.PollIntervalMilliseconds = 60001
		},
		"parallel items zero": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxParallelItems = 0
		},
		"parallel items too many": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxParallelItems = 4
		},
		"rounds zero": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxRoundsPerItem = 0
		},
		"rounds too many": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxRoundsPerItem = 11
		},
		"capacity retries negative": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxCapacityRetries = -1
		},
		"capacity retries too many": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.MaxCapacityRetries = 4
		},
		"heartbeat too small": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Presence.HeartbeatSeconds = 4
		},
		"heartbeat too large": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Presence.HeartbeatSeconds = 61
		},
		"grace too small": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Presence.GraceSeconds = 29
		},
		"grace too large": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Presence.GraceSeconds = 601
		},
		"grace below twice heartbeat": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Presence.HeartbeatSeconds = 60
			app.AssetList.Execution.Presence.GraceSeconds = 119
		},
		"continuation empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Continuation.Message = " "
		},
		"continuation without name": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Continuation.Message = "{description} {route} {round}"
		},
		"continuation without description": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Continuation.Message = "{name} {route} {round}"
		},
		"continuation without route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Continuation.Message = "{name} {description} {round}"
		},
		"continuation without round": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Continuation.Message = "{name} {description} {route}"
		},
		"enabled without presence route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Enabled = true
			app.Runtime.HTTP.Routes.ProjectPresence = ""
		},
		"enabled without run route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Enabled = true
			app.Runtime.HTTP.Routes.ProjectAssetListRun = ""
		},
		"enabled without item route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Execution.Enabled = true
			app.Runtime.HTTP.Routes.ProjectAssetListItem = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("invalid asset_list declaration was accepted")
			}
		})
	}
}

// 未声明该段时旧 Catalog 仍可加载，公开投影也没有该字段。
func TestAssetListSectionStaysOptional(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.AssetList = nil
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("Catalog without application.asset_list rejected: %v", err)
	}
	if application.Public().AssetList != nil {
		t.Fatal("public projection invented an undeclared asset_list")
	}
}

func TestAssetListRouteParametersAreExact(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"list route without project": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetLists = "/api/v1/asset-lists"
		},
		"list route with extra parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetLists = "/api/v1/projects/{projectID}/asset-lists/{listID}"
		},
		"item route without list": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetList = "/api/v1/projects/{projectID}/asset-lists"
		},
		"decision route without list": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListDecision = "/api/v1/projects/{projectID}/asset-lists/decision"
		},
		"board route without list": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListBoard = "/api/v1/projects/{projectID}/asset-lists/board"
		},
		"session route without session": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = "/api/v1/chat/sessions/asset-lists"
		},
		"session route with project parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = "/api/v1/chat/sessions/{projectID}/asset-lists"
		},
		"presence route without project": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectPresence = "/api/v1/presence"
		},
		"presence route with extra parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectPresence = "/api/v1/projects/{projectID}/presence/{tabID}"
		},
		"run control route without project": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListRun = "/api/v1/asset-lists/{listID}/run"
		},
		"run control route without list": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListRun = "/api/v1/projects/{projectID}/asset-lists/run"
		},
		"item control route without list": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListItem = "/api/v1/projects/{projectID}/items/{itemID}"
		},
		"item control route without item": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListItem = "/api/v1/projects/{projectID}/asset-lists/{listID}/items"
		},
		"item control route with extra parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ProjectAssetListItem = "/api/v1/projects/{projectID}/asset-lists/{listID}/items/{itemID}/{action}"
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("inexact asset list route parameters were accepted")
			}
		})
	}
}

func TestAssetListLocalizationRequiresEnglishAndSkipsUndeclaredSections(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	delete(application.Localization.Bundles["en"].Fields, "asset_list.kinds[id=image].label")
	err := catalog.ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "asset_list.kinds[id=image].label") {
		t.Fatalf("missing asset list translation accepted: %v", err)
	}

	// 未声明该段的 Catalog 不要求英文；共享 bundle 与 coverage 里残留的
	// asset_list 条目被跳过。
	application = defaultApplication(t)
	application.AssetList = nil
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("Catalog without asset_list rejects leftover translations: %v", err)
	}
}

func TestAssetListCloneDoesNotShareSlices(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	first.AssetList.Kinds[0].Label = "mutated"
	first.AssetList.Routes[0].Kinds[0] = "mutated"
	first.AssetList.Routes[0].Steps[0].Capability = "mutated"
	first.AssetList.States[0].Role = "mutated"
	first.AssetList.ItemStates[0].Label = "mutated"
	first.AssetList.ItemBlocks[0].Value = "mutated"
	first.AssetList.Execution.Continuation.Message = "mutated"
	second := source.Application()
	if second.AssetList.Kinds[0].Label == "mutated" ||
		second.AssetList.Routes[0].Kinds[0] == "mutated" ||
		second.AssetList.Routes[0].Steps[0].Capability == "mutated" ||
		second.AssetList.States[0].Role == "mutated" ||
		second.AssetList.ItemStates[0].Label == "mutated" ||
		second.AssetList.ItemBlocks[0].Value == "mutated" ||
		second.AssetList.Execution.Continuation.Message == "mutated" {
		t.Fatal("Catalog Application() shares asset list slices with a previous copy")
	}
}

// #331：拆解段的指针也随副本深拷贝。
func TestAssetListCloneDoesNotShareDecompose(t *testing.T) {
	t.Parallel()
	source, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := source.Application()
	if first.AssetList.Decompose == nil {
		t.Fatal("default Catalog has no asset_list.decompose")
	}
	first.AssetList.Decompose.Crop.MinimumShortEdge = 1
	first.AssetList.Decompose.Messages.ReferenceInvalid = "mutated"
	second := source.Application()
	if second.AssetList.Decompose.Crop.MinimumShortEdge == 1 ||
		second.AssetList.Decompose.Messages.ReferenceInvalid == "mutated" {
		t.Fatal("Catalog Application() shares the decompose section with a previous copy")
	}
}

// #331：默认 Catalog 声明单图拆解段：每图上限 24、裁图尺寸限制、参考图缺省
// 路线与四条拒绝文案；公开投影只多出单图项数上限，裁图细节不进公开投影。
func TestDefaultAssetListDecomposeDeclaresCropContract(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	decompose := application.AssetList.Decompose
	if decompose == nil {
		t.Fatal("default Catalog has no application.asset_list.decompose")
	}
	if decompose.MaximumItemsPerImage != 24 ||
		decompose.Crop.MinimumShortEdge != 64 ||
		decompose.Crop.MaximumRegionPixels != 4_000_000 ||
		decompose.Crop.MaximumPNGBytes != 16<<20 {
		t.Fatalf("decompose = %+v", decompose)
	}
	if decompose.DefaultRouteWithReference != "reference-to-3d" {
		t.Fatalf("default_route_with_reference = %q", decompose.DefaultRouteWithReference)
	}
	steps := map[string][]string{}
	for _, route := range application.AssetList.Routes {
		for _, step := range route.Steps {
			steps[route.ID] = append(steps[route.ID], string(step.Capability))
		}
	}
	if got := steps[decompose.DefaultRouteWithReference]; len(got) != 4 ||
		got[0] != "image-to-image" || got[1] != "image-to-multiview" ||
		got[2] != "image-to-3d" || got[3] != "remesh" {
		t.Fatalf("reference route steps = %v", got)
	}
	for field, text := range map[string]string{
		"region_invalid":    decompose.Messages.RegionInvalid,
		"region_too_small":  decompose.Messages.RegionTooSmall,
		"image_too_large":   decompose.Messages.ImageTooLarge,
		"image_item_limit":  decompose.Messages.ImageItemLimit,
		"reference_invalid": decompose.Messages.ReferenceInvalid,
	} {
		if strings.TrimSpace(text) == "" {
			t.Fatalf("decompose message %s is empty", field)
		}
	}
	if strings.TrimSpace(decompose.AgentPrompt) == "" || !strings.Contains(decompose.AgentPrompt, "{maximum}") {
		t.Fatalf("decompose agent_prompt = %q", decompose.AgentPrompt)
	}
	// The sub-session's crop and scene labels are declared for both the
	// pictured and the unpictured attachment paths and are translated.
	englishFields := application.Localization.Bundles["en"].Fields
	for _, role := range []string{"image.crop", "image.source_scene"} {
		if !strings.Contains(application.Chat.Context.AttachmentLabelTemplates[role], "{handle}") ||
			!strings.Contains(application.Chat.Context.UnpicturedAttachmentTemplates[role], "{handle}") {
			t.Fatalf("role %q has no attachment labels: %+v", role,
				application.Chat.Context.UnpicturedAttachmentTemplates)
		}
		for _, path := range []string{
			"chat.context.attachment_label_templates[" + role + "]",
			"chat.context.unpictured_attachment_templates[" + role + "]",
		} {
			if strings.TrimSpace(englishFields[path]) == "" {
				t.Fatalf("English bundle lacks %s", path)
			}
		}
	}
	if strings.TrimSpace(englishFields["asset_list.decompose.agent_prompt"]) == "" {
		t.Fatal("English bundle lacks asset_list.decompose.agent_prompt")
	}
	covered := map[string]bool{}
	for _, path := range application.Localization.Coverage {
		covered[path] = true
	}
	for _, role := range []string{"image.crop", "image.source_scene"} {
		for _, prefix := range []string{"chat.context.attachment_label_templates", "chat.context.unpictured_attachment_templates"} {
			if !covered[prefix+"["+role+"]"] {
				t.Fatalf("localization coverage lacks %s[%s]", prefix, role)
			}
		}
	}
	public := application.Public().AssetList
	if public == nil || public.Limits.MaximumItemsPerImage != 24 ||
		public.Limits.MinimumCropShortEdge != 64 {
		t.Fatalf("public limits = %+v", public)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"maximum_region_pixels", "maximum_png_bytes", "reference_invalid"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("public asset_list leaks decompose field %q", private)
		}
	}
}

func TestAssetListDecomposeValidationRejectsBadDeclarations(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*catalog.ApplicationSchema){
		"zero maximum items": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.MaximumItemsPerImage = 0
		},
		"too many maximum items": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.MaximumItemsPerImage = 101
		},
		"zero minimum short edge": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MinimumShortEdge = 0
		},
		"minimum short edge too large": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MinimumShortEdge = 4097
		},
		"region pixels below the minimum square": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MaximumRegionPixels = 4095
		},
		"region pixels above the decode bound": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MaximumRegionPixels = 100_000_001
		},
		"zero png bytes": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MaximumPNGBytes = 0
		},
		"png bytes too large": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Crop.MaximumPNGBytes = 256<<20 + 1
		},
		"unknown reference route": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.DefaultRouteWithReference = "missing-route"
		},
		"reference route starts with a 3D step": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.DefaultRouteWithReference = "image-to-3d"
		},
		"region invalid message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Messages.RegionInvalid = " "
		},
		"region too small message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Messages.RegionTooSmall = ""
		},
		"image too large message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Messages.ImageTooLarge = " "
		},
		"image item limit message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Messages.ImageItemLimit = ""
		},
		"reference invalid message empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.Messages.ReferenceInvalid = ""
		},
		"agent prompt empty": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.AgentPrompt = " "
		},
		"agent prompt without the maximum": func(app *catalog.ApplicationSchema) {
			app.AssetList.Decompose.AgentPrompt = "拆解要领"
		},
		"crop attachment label missing": func(app *catalog.ApplicationSchema) {
			delete(app.Chat.Context.AttachmentLabelTemplates, "image.crop")
		},
		"source scene unpictured label missing": func(app *catalog.ApplicationSchema) {
			delete(app.Chat.Context.UnpicturedAttachmentTemplates, "image.source_scene")
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			application := defaultApplication(t)
			mutate(&application)
			if err := catalog.ValidateApplicationSchema(application); err == nil {
				t.Fatal("invalid asset_list.decompose declaration was accepted")
			}
		})
	}
}

// 未声明拆解段时旧 Catalog 仍可加载；公开投影的上限为 0。
func TestAssetListDecomposeStaysOptional(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.AssetList.Decompose = nil
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("Catalog without application.asset_list.decompose rejected: %v", err)
	}
	if public := application.Public().AssetList; public == nil || public.Limits.MaximumItemsPerImage != 0 {
		t.Fatalf("public limits without decompose = %+v", public)
	}
}

// #328：默认 Catalog 声明自动制作段（默认关闭），公开投影只给心跳间隔。
func TestDefaultAssetListExecutionDeclaresScheduleAndPresence(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	execution := application.AssetList.Execution
	if execution == nil {
		t.Fatal("default Catalog has no asset_list.execution")
	}
	if execution.Enabled ||
		execution.PollIntervalMilliseconds != 2000 ||
		execution.MaxParallelItems != 1 ||
		execution.MaxRoundsPerItem != 3 ||
		execution.MaxCapacityRetries != 0 ||
		execution.Presence.HeartbeatSeconds != 15 ||
		execution.Presence.GraceSeconds != 120 {
		t.Fatalf("execution = %+v", execution)
	}
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("default asset_list.execution invalid: %v", err)
	}
	public := application.Public().AssetList
	if public.Presence == nil || public.Presence.HeartbeatSeconds != 15 {
		t.Fatalf("public presence = %+v", public.Presence)
	}
}

// 未声明 execution 时旧 Catalog 仍可加载，公开投影也没有 presence。
func TestAssetListExecutionStaysOptional(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	application.AssetList.Execution = nil
	if err := catalog.ValidateApplicationSchema(application); err != nil {
		t.Fatalf("Catalog without asset_list.execution rejected: %v", err)
	}
	public := application.Public().AssetList
	if public.Presence != nil {
		t.Fatalf("public projection invented presence: %+v", public.Presence)
	}
	encoded, err := json.Marshal(public)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "presence") {
		t.Fatalf("public asset_list without execution still carries presence: %s", encoded)
	}
}

// #328：自动制作文案中英成对，占位符一致，续轮文案走 fields 覆盖。
func TestAssetListExecutionCopyIsLocalized(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	english := application.Localization.Bundles["en"]
	var messages map[string]string
	raw, err := json.Marshal(application.AssetList.Messages)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"pause_presence", "pause_user", "pause_step_cap", "pause_failure",
		"resumed", "stopped", "skipped", "round_limit",
		"awaiting_review", "needs_you",
		"item_failed", "item_failed_input", "item_failed_capacity",
		"item_failed_refused", "item_failed_internal",
		"step_cap_blocked", "needs_approval", "run_waiting",
		"item_reported_done", "item_reported_failed", "item_not_in_session",
		"item_not_working", "item_no_successful_task", "item_report_failed",
	} {
		if strings.TrimSpace(messages[key]) == "" {
			t.Errorf("asset_list.messages.%s missing from the default copy", key)
		}
		if strings.TrimSpace(english.Fields["asset_list.messages."+key]) == "" {
			t.Errorf("asset_list.messages.%s missing from the English fields", key)
		}
	}
	if strings.TrimSpace(english.Fields["asset_list.execution.continuation.message"]) == "" {
		t.Error("asset_list.execution.continuation.message missing from the English fields")
	}
	for _, key := range []string{
		"ui.asset_list.run_status", "ui.asset_list.run_running",
		"ui.asset_list.run_paused_presence", "ui.asset_list.run_paused_user",
		"ui.asset_list.run_paused_step_cap", "ui.asset_list.run_paused_failure",
		"ui.asset_list.run_stopped", "ui.asset_list.run_completed",
		"ui.asset_list.run_progress",
		"ui.asset_list.run_pause", "ui.asset_list.run_resume",
		"ui.asset_list.run_stop", "ui.asset_list.run_stop_confirm",
		"ui.asset_list.run_skip", "ui.asset_list.run_start",
		"ui.asset_list.run_waiting",
		"ui.asset_list.item_awaiting_approval", "ui.asset_list.item_step_cap",
		"ui.asset_list.item_round_limit",
	} {
		if strings.TrimSpace(application.Content.Messages[key]) == "" {
			t.Errorf("content.messages %s missing from the default copy", key)
		}
		if strings.TrimSpace(english.Messages[key]) == "" {
			t.Errorf("content.messages %s missing from the English bundle", key)
		}
	}
}

// #508 asset-list-new-01 / asset-list-08: the copy an item turn reads (the
// continuation of every round and the rework turn after a return) tells the
// Agent to report through the completion tool the Catalog actually declares.
// An older rework copy named a report_list_item tool that never existed, and
// the continuation never asked for a report, so a made item ran out of rounds.
func TestAssetListItemTurnCopyNamesTheDeclaredCompletionTool508(t *testing.T) {
	t.Parallel()
	application := defaultApplication(t)
	const completionTool = "submit_asset_list_item"
	declared := false
	for _, tool := range application.Chat.AgentTools {
		if tool.Name == completionTool {
			declared = true
		}
	}
	if !declared {
		t.Fatalf("the Catalog declares no %s tool", completionTool)
	}
	list := application.AssetList
	if list == nil || list.Execution == nil || list.Review == nil {
		t.Fatal("the default Catalog misses asset_list execution or review")
	}
	english := application.Localization.Bundles["en"].Fields
	for name, text := range map[string]string{
		"execution.continuation.message":    list.Execution.Continuation.Message,
		"en execution.continuation.message": english["asset_list.execution.continuation.message"],
		"review.messages.rework_message":    list.Review.Messages.ReworkMessage,
		"en review.messages.rework_message": english["asset_list.review.messages.rework_message"],
	} {
		if !strings.Contains(text, completionTool) {
			t.Errorf("%s does not name %s: %q", name, completionTool, text)
		}
		if strings.Contains(text, "report_list_item") {
			t.Errorf("%s names the undeclared report_list_item tool", name)
		}
	}
}
