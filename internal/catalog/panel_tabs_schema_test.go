package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogDeclaresTheRightPanelTabs(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	panel := taskCatalog.Application().Canvas.Frontend.PanelTabs
	if panel.WidthPixels != 300 {
		t.Errorf("width_pixels = %d, want 300", panel.WidthPixels)
	}
	type entry struct{ id, icon, shortcut string }
	wantTabs := []entry{
		{"layers", "layers", "layers_panel_shortcut"},
		{"history", "history", "history_panel_shortcut"},
		{"details", "info", ""},
		{"operations", "list-checks", ""},
		{"process", "sliders-horizontal", ""},
		{"formats", "file-box", ""},
		{"parts", "puzzle", ""},
	}
	if len(panel.Tabs) != len(wantTabs) {
		t.Fatalf("panel_tabs.tabs has %d entries, want %d", len(panel.Tabs), len(wantTabs))
	}
	for index, want := range wantTabs {
		tab := panel.Tabs[index]
		if tab.ID != want.id || tab.Icon != want.icon || tab.Shortcut != want.shortcut {
			t.Errorf("panel_tabs.tabs[%d] = %+v, want %+v", index, tab, want)
		}
	}
	wantKinds := []CanvasPanelKindSchema{
		{Kind: "image", Tabs: []string{"layers", "history", "details", "operations"}, Default: "layers"},
		{Kind: "board", Tabs: []string{"layers", "history", "operations"}, Default: "layers"},
		{Kind: "model", Tabs: []string{"operations", "process", "formats", "parts", "details"}, Default: "operations"},
	}
	if !reflect.DeepEqual(panel.Kinds, wantKinds) {
		t.Errorf("panel_tabs.kinds = %+v, want %+v", panel.Kinds, wantKinds)
	}
}

// One icon, one meaning (UI spec §9): the tab icons share the rule with the stage actions, the processing
// steps and the rail tools.
func TestRightPanelTabIconsAreUniquePerMeaning(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	owners := map[string]string{}
	claim := func(icon, owner string) {
		if previous, exists := owners[icon]; exists {
			t.Errorf("icon %q is used by both %s and %s", icon, previous, owner)
		}
		owners[icon] = owner
	}
	for _, action := range application.Canvas.Frontend.StageActions {
		claim(action.Icon, "stage action "+action.ID)
	}
	for _, tool := range application.Canvas.Frontend.Tools {
		claim(tool.Icon, "rail tool "+tool.Rail+"/"+tool.ID)
	}
	for _, configured := range application.Runtime.Frontend.ModelProcessingCapabilities {
		for _, capability := range application.Capabilities {
			if capability.ID == configured {
				claim(capability.Icon, "processing capability "+string(capability.ID))
			}
		}
	}
	for _, tab := range application.Canvas.Frontend.PanelTabs.Tabs {
		claim(tab.Icon, "panel tab "+tab.ID)
	}
	// 加工 no longer borrows the magic wand, which belongs to the inpaint button alone.
	for _, tab := range application.Canvas.Frontend.PanelTabs.Tabs {
		if tab.ID == "process" && tab.Icon == "wand-sparkles" {
			t.Error("the process tab uses the inpaint icon")
		}
	}
}

func TestRightPanelTabsAreWordedFromSchemaMessages(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := taskCatalog.Application().Content.Messages
	for _, tab := range taskCatalog.Application().Canvas.Frontend.PanelTabs.Tabs {
		for _, key := range []string{tab.LabelKey, tab.HintKey} {
			if strings.TrimSpace(messages[key]) == "" {
				t.Errorf("tab %s: message %q is missing", tab.ID, key)
			}
		}
	}
	for key, want := range map[string]string{
		"ui.studio.panel.tab.layers":      "图层",
		"ui.studio.panel.tab.history":     "历史",
		"ui.studio.panel.tab.details":     "详情",
		"ui.studio.panel.tab.process":     "加工",
		"ui.studio.panel.tab.formats":     "格式",
		"ui.studio.panel.tab.parts":       "部件",
		"ui.studio.panel.title":           "右侧栏",
		"ui.studio.panel.empty":           "在左边对话里说你想做什么，或把图拖到画板上。",
		"ui.shortcuts.item.panel_layers":  "切到图层",
		"ui.shortcuts.item.panel_history": "切到历史",
	} {
		if messages[key] != want {
			t.Errorf("message %q = %q, want %q", key, messages[key], want)
		}
	}
}

// The unified names (UI spec §10): 加工 replaces 模型加工, and 右侧栏 replaces 检查器 and 画布侧栏. The old
// right-panel and canvas-side-panel texts are gone with the two panels they labelled.
func TestRightPanelRenamesAndRemovals(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for key, text := range application.Content.Messages {
		for _, old := range []string{"模型加工", "检查器", "画布侧栏"} {
			if strings.Contains(text, old) {
				t.Errorf("message %q still says %q: %q", key, old, text)
			}
		}
		if strings.HasPrefix(key, "ui.studio.inspector.") || strings.HasPrefix(key, "ui.canvas.panel.") {
			t.Errorf("message %q belongs to a panel that no longer exists", key)
		}
	}
	for key, text := range application.Canvas.Presentation.Text {
		for _, old := range []string{"模型加工", "检查器", "画布侧栏"} {
			if strings.Contains(text, old) {
				t.Errorf("canvas.presentation.text %q still says %q: %q", key, old, text)
			}
		}
	}
	if _, exists := application.Content.Messages["ui.studio.panel.tab.process"]; !exists {
		t.Fatal("the process tab has no label")
	}
}

func TestPanelTabsAreInThePublicProjection(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	public := application.Public()
	if !reflect.DeepEqual(public.Canvas.Frontend.PanelTabs, application.Canvas.Frontend.PanelTabs) {
		t.Fatal("public canvas.frontend.panel_tabs differs from the Schema")
	}
	encoded, err := json.Marshal(public.Canvas.Frontend)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"panel_tabs":{"width_pixels":300,"tabs":[{"id":"layers","icon":"layers","label_key":"ui.studio.panel.tab.layers","hint_key":"ui.studio.panel.tab.layers_hint","shortcut":"layers_panel_shortcut"}`,
		`{"id":"process","icon":"sliders-horizontal"`,
		`{"kind":"model","tabs":["operations","process","formats","parts","details"],"default":"operations"}`,
	} {
		if !strings.Contains(string(encoded), fragment) {
			t.Errorf("public canvas.frontend lacks %s", fragment)
		}
	}
	// The old resident-panel block (width, collapse threshold, icon strip) is gone.
	if strings.Contains(string(encoded), `"panels":`) || strings.Contains(string(encoded), "collapse_below_stage_width_pixels") {
		t.Fatal("public canvas.frontend still carries the removed panels block")
	}
}

func TestPanelTabsCloneIsIndependent(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Canvas.Frontend.PanelTabs.Tabs[0].ID = "mutated"
	first.Canvas.Frontend.PanelTabs.Kinds[0].Tabs[0] = "mutated"
	first.Canvas.Frontend.PanelTabs.Kinds[2].Default = "mutated"
	second := taskCatalog.Application()
	panel := second.Canvas.Frontend.PanelTabs
	if panel.Tabs[0].ID != "layers" || panel.Kinds[0].Tabs[0] != "layers" || panel.Kinds[2].Default != "operations" {
		t.Fatalf("panel_tabs leaked a mutation: %+v", panel)
	}
}

func TestPanelTabsRejectInvalidDeclarations(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	index := func(id string) int {
		for position, tab := range taskCatalog.Application().Canvas.Frontend.PanelTabs.Tabs {
			if tab.ID == id {
				return position
			}
		}
		t.Fatalf("tab %q is not declared", id)
		return -1
	}
	layers, details := index("layers"), index("details")
	const image, board, model = 0, 1, 2
	mutations := []struct {
		name   string
		mutate func(*ApplicationSchema)
		want   string
	}{
		{"zero width", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.WidthPixels = 0 }, "width_pixels"},
		{"negative width", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.WidthPixels = -300 }, "width_pixels"},
		{"no tabs", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs = nil }, "tabs 不能为空"},
		{"empty id", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].ID = " " }, "id 不能为空"},
		{"unknown id", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].ID = "inspector" }, "id 无效"},
		{"duplicate id", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[details].ID = "layers" }, "id 重复"},
		{"empty icon", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].Icon = "" }, "icon 不能为空"},
		{"unknown label key", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Tabs[layers].LabelKey = "ui.studio.panel.tab.no_such"
		}, "label_key"},
		{"empty hint key", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].HintKey = "" }, "hint_key"},
		{"label text removed", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.panel.tab.layers") }, "label_key"},
		{"hint text removed", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.panel.tab.layers_hint") }, "hint_key"},
		{"unknown shortcut setting", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].Shortcut = "no_such_key" }, "shortcut"},
		{"shortcut on a list setting", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].Shortcut = "delete_keys" }, "shortcut"},
		{"shortcut on a number setting", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Tabs[layers].Shortcut = "nudge_pixels" }, "shortcut"},
		{"no kinds", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds = nil }, "kinds 不能为空"},
		{"unknown kind", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[image].Kind = "video" }, "kind 无效"},
		{"duplicate kind", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[board].Kind = "image" }, "kind 重复"},
		{"a kind is missing", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Kinds = a.Canvas.Frontend.PanelTabs.Kinds[:model]
		}, "缺少种类"},
		{"kind without tabs", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[board].Tabs = nil }, "tabs 不能为空"},
		{"kind lists an undeclared tab", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Tabs = append(a.Canvas.Frontend.PanelTabs.Tabs[:details], a.Canvas.Frontend.PanelTabs.Tabs[details+1:]...)
		}, "未声明的标签"},
		{"kind lists a tab twice", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Kinds[image].Tabs = []string{"layers", "layers", "details"}
		}, "tabs 重复"},
		{"default is not one of the kind's tabs", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[board].Default = "details" }, "default 必须"},
		{"empty default", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[image].Default = "" }, "default 必须"},
		{"default is not a declared tab", func(a *ApplicationSchema) { a.Canvas.Frontend.PanelTabs.Kinds[image].Default = "nowhere" }, "default 必须"},
		{"fallback is not one of the kind's tabs", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Kinds[model].DefaultWhenUnavailable = "layers"
		}, "default_when_unavailable"},
		{"fallback is not a declared tab", func(a *ApplicationSchema) {
			a.Canvas.Frontend.PanelTabs.Kinds[model].DefaultWhenUnavailable = "nowhere"
		}, "default_when_unavailable"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			application := taskCatalog.Application()
			mutation.mutate(&application)
			err := ValidateApplicationSchema(application)
			if err == nil {
				t.Fatal("accepted an invalid panel_tabs declaration")
			}
			if !strings.Contains(err.Error(), mutation.want) {
				t.Fatalf("error = %v, want it to mention %q", err, mutation.want)
			}
		})
	}
}

// A kind may leave the fallback out, and a shortcut may name any single-key keyboard setting.
func TestPanelTabsAcceptTheOptionalFields(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	application.Canvas.Frontend.PanelTabs.Kinds[2].DefaultWhenUnavailable = ""
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("a kind without a fallback was rejected: %v", err)
	}
	for _, setting := range []string{"space_code", "recenter_key", "help_key", "lock_shortcut", "layers_panel_shortcut", "history_panel_shortcut"} {
		application := taskCatalog.Application()
		application.Canvas.Frontend.PanelTabs.Tabs[2].Shortcut = setting
		if err := ValidateApplicationSchema(application); err != nil {
			t.Errorf("shortcut %q rejected: %v", setting, err)
		}
	}
}
