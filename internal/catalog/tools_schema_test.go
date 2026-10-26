package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestDefaultCatalogDeclaresTheRailTools(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	type entry struct {
		id, rail, section, icon, shortcut string
		modifiers                         []string
	}
	want := []entry{
		{"select", "canvas", "select", "mouse-pointer-2", "select_tool_key", nil},
		{"hand", "canvas", "select", "hand", "hand_tool_key", nil},
		{"marquee", "canvas", "select", "square-dashed", "marquee_tool_key", nil},
		{"lasso", "canvas", "select", "lasso", "lasso_tool_key", nil},
		{"mask", "canvas", "select", "spray-can", "mask_tool_key", nil},
		{"eyedropper", "canvas", "select", "pipette", "eyedropper_tool_key", nil},
		{"zoom", "canvas", "select", "zoom-in", "zoom_tool_key", nil},
		{"upload", "canvas", "draw", "image-plus", "", nil},
		{"shape", "canvas", "draw", "square", "shape_tool_key", nil},
		{"text", "canvas", "draw", "type", "text_tool_key", nil},
		{"pencil", "canvas", "draw", "pencil", "brush_tool_key", []string{"shift"}},
		{"brush", "canvas", "draw", "paintbrush", "brush_tool_key", nil},
		{"eraser", "canvas", "draw", "eraser", "eraser_tool_key", nil},
		{"crop", "canvas", "draw", "scissors", "crop_tool_key", nil},
		{"snap", "canvas", "snap", "magnet", "", nil},
		{"undo", "canvas", "history", "undo-2", "undo_key", []string{"mod"}},
		{"redo", "canvas", "history", "redo-2", "redo_key", []string{"mod", "shift"}},
		{"texture", "viewer", "views", "wallpaper", "", nil},
		{"clay", "viewer", "views", "cylinder", "", nil},
		{"wire", "viewer", "views", "grid-3x3", "", nil},
	}
	tools := application.Canvas.Frontend.Tools
	if len(tools) != len(want) {
		t.Fatalf("tools has %d entries, want %d", len(tools), len(want))
	}
	for index, expected := range want {
		tool := tools[index]
		if tool.ID != expected.id || tool.Rail != expected.rail || tool.Section != expected.section ||
			tool.Icon != expected.icon || tool.Shortcut != expected.shortcut || !reflect.DeepEqual(tool.Modifiers, expected.modifiers) {
			t.Errorf("tools[%d] = %+v, want %+v", index, tool, expected)
		}
	}
}

// Sections of one rail are contiguous, so the thin line between sections never splits one in two.
func TestRailToolSectionsAreContiguous(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	closed := map[string]bool{}
	previous := ""
	for _, tool := range taskCatalog.Application().Canvas.Frontend.Tools {
		key := tool.Rail + "/" + tool.Section
		if key != previous {
			if closed[key] {
				t.Errorf("section %s of tool %s comes back after another section", key, tool.ID)
			}
			if previous != "" {
				closed[previous] = true
			}
			previous = key
		}
	}
}

func TestRailToolsAreWordedFromSchemaMessages(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	messages := taskCatalog.Application().Content.Messages
	for _, tool := range taskCatalog.Application().Canvas.Frontend.Tools {
		for _, key := range []string{tool.LabelKey, tool.HintKey} {
			if strings.TrimSpace(messages[key]) == "" {
				t.Errorf("tool %s: message %q is missing", tool.ID, key)
			}
		}
	}
	for key, want := range map[string]string{
		"ui.canvas.toolbar.mask":           "蒙版",
		"ui.canvas.toolbar.brush":          "画笔",
		"ui.canvas.toolbar.snap":           "吸附",
		"ui.shortcuts.item.tool_mask":      "蒙版",
		"ui.studio.stage.view.texture":     "贴图",
		"ui.studio.stage.view.clay":        "白模",
		"ui.studio.stage.view.wire":        "线框",
		"ui.studio.rail.expand":            "展开",
		"ui.studio.rail.collapse":          "收起",
		"ui.canvas.toolbar.undo_empty":     "没有可以撤销的操作。",
		"ui.canvas.toolbar.redo_empty":     "没有可以重做的操作。",
		"ui.studio.stage.view.unsupported": "这个模型读不出材质，只能看贴图。",
		"ui.studio.stage.view.not_loaded":  "模型还没加载好，加载完成后才能切换白模和线框。",
	} {
		if messages[key] != want {
			t.Errorf("message %q = %q, want %q", key, messages[key], want)
		}
	}
}

// The unified names (UI spec §10) replaced 局部重绘蒙版 and 像素画笔 everywhere the product words a tool, and
// the UV placeholder and the old native-title tooltips are gone.
func TestRailToolRenamesAndRemovals(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	for key, text := range application.Content.Messages {
		for _, old := range []string{"局部重绘蒙版", "像素画笔"} {
			if strings.Contains(text, old) {
				t.Errorf("message %q still says %q: %q", key, old, text)
			}
		}
		if strings.HasPrefix(key, "ui.canvas.toolbar.") && strings.HasSuffix(key, "_tooltip") {
			t.Errorf("message %q is an unused tooltip text", key)
		}
		if strings.HasPrefix(key, "ui.studio.stage.view.uv") {
			t.Errorf("message %q belongs to the removed UV placeholder", key)
		}
	}
	for key, text := range application.Canvas.Presentation.Text {
		for _, old := range []string{"局部重绘蒙版", "像素画笔"} {
			if strings.Contains(text, old) {
				t.Errorf("canvas.presentation.text %q still says %q: %q", key, old, text)
			}
		}
	}
	for _, tool := range application.Canvas.Frontend.Tools {
		if strings.Contains(strings.ToLower(tool.ID), "uv") {
			t.Errorf("tool %s is a UV placeholder", tool.ID)
		}
	}
	for _, tool := range application.Canvas.Frontend.Tools {
		if tool.ID == "mask" && tool.Section != "select" {
			t.Errorf("mask is in section %q; it is a selection-like tool", tool.Section)
		}
	}
}

// Rail tools never call a model (R12), so the table carries no cost fields and the hint has no cost line.
func TestRailToolsCarryNoCostFields(t *testing.T) {
	toolType := reflect.TypeOf(CanvasToolSchema{})
	for index := 0; index < toolType.NumField(); index++ {
		tag, _, _ := strings.Cut(toolType.Field(index).Tag.Get("json"), ",")
		switch tag {
		case "calls_model", "cost_hint", "target_key", "kinds", "group":
			t.Errorf("canvas.frontend.tools declares %q, which belongs to stage_actions", tag)
		}
	}
}

func TestRailToolsAreInThePublicProjection(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	public := application.Public()
	if !reflect.DeepEqual(public.Canvas.Frontend.Tools, application.Canvas.Frontend.Tools) {
		t.Fatal("public canvas.frontend.tools differs from the Schema")
	}
	encoded, err := json.Marshal(public.Canvas.Frontend)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"tools":[{"id":"select","rail":"canvas","section":"select"`,
		`{"id":"redo","rail":"canvas","section":"history","icon":"redo-2","label_key":"ui.canvas.toolbar.redo","hint_key":"ui.canvas.toolbar.redo_hint","shortcut":"redo_key","modifiers":["mod","shift"]}`,
		`{"id":"wire","rail":"viewer","section":"views","icon":"grid-3x3"`,
	} {
		if !strings.Contains(string(encoded), fragment) {
			t.Errorf("public canvas.frontend lacks %s", fragment)
		}
	}
}

func TestRailToolsCloneIsIndependent(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	redo := -1
	for index, tool := range taskCatalog.Application().Canvas.Frontend.Tools {
		if tool.ID == "redo" {
			redo = index
		}
	}
	if redo < 0 {
		t.Fatal("redo is not declared")
	}
	first := taskCatalog.Application()
	first.Canvas.Frontend.Tools[0].ID = "mutated"
	first.Canvas.Frontend.Tools[redo].Modifiers[0] = "mutated"
	second := taskCatalog.Application()
	if second.Canvas.Frontend.Tools[0].ID != "select" || second.Canvas.Frontend.Tools[redo].Modifiers[0] != "mod" {
		t.Fatalf("tools leaked a mutation: %+v %+v", second.Canvas.Frontend.Tools[0], second.Canvas.Frontend.Tools[redo])
	}
}

func TestRailToolsRejectInvalidDeclarations(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	indexOf := func(id string) int {
		for index, tool := range taskCatalog.Application().Canvas.Frontend.Tools {
			if tool.ID == id {
				return index
			}
		}
		t.Fatalf("tool %q is not declared", id)
		return -1
	}
	brush, redo, wire := indexOf("brush"), indexOf("redo"), indexOf("wire")
	mutations := []struct {
		name   string
		mutate func(*ApplicationSchema)
		want   string
	}{
		{"empty table", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools = nil }, "tools 不能为空"},
		{"empty id", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].ID = " " }, "id 不能为空"},
		{"duplicate id in a rail", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].ID = "select" }, "id 在 rail"},
		{"unknown rail", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Rail = "floating" }, "rail 无效"},
		{"empty rail", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Rail = "" }, "rail 无效"},
		{"unknown section", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Section = "paint" }, "section"},
		{"empty section", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Section = "" }, "section"},
		{"canvas tool in a viewer section", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Section = "views" }, "section"},
		{"viewer tool in a canvas section", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[wire].Section = "draw" }, "section"},
		{"empty icon", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Icon = "" }, "icon 不能为空"},
		{"unknown label key", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].LabelKey = "ui.canvas.toolbar.no_such" }, "label_key"},
		{"empty hint key", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].HintKey = "" }, "hint_key"},
		{"label text removed", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.canvas.toolbar.brush") }, "label_key"},
		{"unknown shortcut setting", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Shortcut = "no_such_key" }, "shortcut"},
		{"shortcut on a list setting", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Shortcut = "delete_keys" }, "shortcut"},
		{"shortcut on a number setting", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[brush].Shortcut = "nudge_pixels" }, "shortcut"},
		{"unknown modifier", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[redo].Modifiers = []string{"mod", "super"} }, "修饰键"},
		{"ctrl is not a modifier here", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[redo].Modifiers = []string{"ctrl"} }, "修饰键"},
		{"duplicate modifier", func(a *ApplicationSchema) { a.Canvas.Frontend.Tools[redo].Modifiers = []string{"mod", "mod"} }, "modifiers 重复"},
		{"modifiers without a shortcut", func(a *ApplicationSchema) {
			a.Canvas.Frontend.Tools[wire].Modifiers = []string{"shift"}
		}, "modifiers"},
		{"missing expand text", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.rail.expand") }, "ui.studio.rail.expand"},
		{"missing collapse text", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.rail.collapse") }, "ui.studio.rail.collapse"},
		{"missing expand hint", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.rail.expand_hint") }, "ui.studio.rail.expand_hint"},
		{"missing collapse hint", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.rail.collapse_hint") }, "ui.studio.rail.collapse_hint"},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			application := taskCatalog.Application()
			mutation.mutate(&application)
			err := ValidateApplicationSchema(application)
			if err == nil {
				t.Fatal("accepted an invalid tools declaration")
			}
			if !strings.Contains(err.Error(), mutation.want) {
				t.Fatalf("error = %v, want it to mention %q", err, mutation.want)
			}
		})
	}
}

// The same id may name a tool on each rail: ids are unique inside one rail only.
func TestRailToolIDsAreUniquePerRail(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	extra := application.Canvas.Frontend.Tools[len(application.Canvas.Frontend.Tools)-1]
	extra.Rail, extra.Section = "canvas", "draw"
	application.Canvas.Frontend.Tools = append(application.Canvas.Frontend.Tools, extra)
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("a viewer id reused on the canvas rail was rejected: %v", err)
	}
	application = taskCatalog.Application()
	same := application.Canvas.Frontend.Tools[len(application.Canvas.Frontend.Tools)-1]
	application.Canvas.Frontend.Tools = append(application.Canvas.Frontend.Tools, same)
	err = ValidateApplicationSchema(application)
	if err == nil || !strings.Contains(err.Error(), "id 在 rail") {
		t.Fatalf("a repeated viewer id was accepted: %v", err)
	}
}

func TestRailToolShortcutMayNameAnyStringKeyboardSettingWithAnyModifiers(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"space_code", "recenter_key", "help_key", "lock_shortcut", "brush_tool_key"} {
		for _, modifiers := range [][]string{nil, {"mod"}, {"shift"}, {"alt"}, {"mod", "shift"}, {"mod", "shift", "alt"}} {
			application := taskCatalog.Application()
			for index := range application.Canvas.Frontend.Tools {
				if application.Canvas.Frontend.Tools[index].ID == "upload" {
					application.Canvas.Frontend.Tools[index].Shortcut = setting
					application.Canvas.Frontend.Tools[index].Modifiers = modifiers
				}
			}
			if err := ValidateApplicationSchema(application); err != nil {
				t.Errorf("shortcut %q with %v rejected: %v", setting, modifiers, err)
			}
		}
	}
}
