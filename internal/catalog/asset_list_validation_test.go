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
	if !slices.Equal(blocks, []string{"awaiting_approval", "paused", "step_cap"}) {
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
		"session asset lists route missing": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = ""
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
		"session route without session": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = "/api/v1/chat/sessions/asset-lists"
		},
		"session route with project parameter": func(app *catalog.ApplicationSchema) {
			app.Runtime.HTTP.Routes.ChatSessionAssetLists = "/api/v1/chat/sessions/{projectID}/asset-lists"
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
	second := source.Application()
	if second.AssetList.Kinds[0].Label == "mutated" ||
		second.AssetList.Routes[0].Kinds[0] == "mutated" ||
		second.AssetList.Routes[0].Steps[0].Capability == "mutated" ||
		second.AssetList.States[0].Role == "mutated" ||
		second.AssetList.ItemStates[0].Label == "mutated" ||
		second.AssetList.ItemBlocks[0].Value == "mutated" {
		t.Fatal("Catalog Application() shares asset list slices with a previous copy")
	}
}
