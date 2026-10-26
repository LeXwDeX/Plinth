package catalog

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

func TestDefaultCatalogDeclaresStageToolbarActions(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	type entry struct {
		id, group, cost, icon string
		kinds                 []string
	}
	want := []entry{
		{"merge", "produce", "free", "merge", []string{"board", "image"}},
		{"continue_from", "produce", "free", "git-branch", []string{"image", "model"}},
		{"inpaint", "produce", "approval_card", "wand-sparkles", []string{"image"}},
		{"remove_background", "produce", "approval_card", "image-minus", []string{"image", "board"}},
		{"to_3d", "produce", "approval_card", "rotate-3d", []string{"image"}},
		{"retexture_with_image", "produce", "approval_card", "palette", []string{"image"}},
		{"capture_2d", "produce", "free", "camera", []string{"model"}},
		{"send_chat", "output", "free", "message-square-plus", []string{"image", "board"}},
		{"export_board", "output", "free", "file-output", []string{"image", "board"}},
		{"compare", "view", "free", "columns-2", []string{"image", "model"}},
		{"enlarge", "view", "free", "maximize-2", []string{"image"}},
	}
	actions := application.Canvas.Frontend.StageActions
	if len(actions) != len(want) {
		t.Fatalf("stage_actions has %d entries, want %d", len(actions), len(want))
	}
	for index, expected := range want {
		action := actions[index]
		if action.ID != expected.id || action.Group != expected.group || action.CostHint != expected.cost ||
			action.Icon != expected.icon || !reflect.DeepEqual(action.Kinds, expected.kinds) {
			t.Errorf("stage_actions[%d] = %+v, want %+v", index, action, expected)
		}
		if action.CallsModel != (expected.cost != "free") {
			t.Errorf("stage_actions[%d] %s: calls_model = %v with cost_hint %s", index, action.ID, action.CallsModel, action.CostHint)
		}
		if action.Shortcut != "" && action.ID != "enlarge" {
			t.Errorf("stage_actions[%d] %s: unexpected shortcut %q", index, action.ID, action.Shortcut)
		}
	}
	enlarge := actions[len(actions)-1]
	if enlarge.Shortcut != "space_code" || application.Content.Messages[enlarge.LabelKey] != "大图" {
		t.Errorf("enlarge = %+v, label %q", enlarge, application.Content.Messages[enlarge.LabelKey])
	}
}

func TestStageToolbarAndRailIconsAreUniquePerMeaning(t *testing.T) {
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
	for _, configured := range application.Runtime.Frontend.ModelProcessingCapabilities {
		for _, capability := range application.Capabilities {
			if capability.ID == configured {
				claim(capability.Icon, "processing capability "+string(capability.ID))
			}
		}
	}
	// The rail tools (canvas tool rail and 3D view bar) share the same rule: one icon, one meaning.
	for _, tool := range application.Canvas.Frontend.Tools {
		claim(tool.Icon, "rail tool "+tool.Rail+"/"+tool.ID)
	}
	if len(owners) < len(application.Canvas.Frontend.StageActions)+len(application.Canvas.Frontend.Tools) {
		t.Fatalf("only %d icons are claimed", len(owners))
	}
}

func TestStageActionsAreInThePublicProjection(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := taskCatalog.Application()
	public := application.Public()
	if !reflect.DeepEqual(public.Canvas.Frontend.StageActions, application.Canvas.Frontend.StageActions) {
		t.Fatal("public canvas.frontend.stage_actions differs from the Schema")
	}
	encoded, err := json.Marshal(public.Canvas.Frontend)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"stage_actions":[{"id":"merge"`) {
		t.Fatalf("public canvas.frontend lacks stage_actions: %s", encoded[:200])
	}
	if strings.Contains(string(encoded), "focus_mode_key") {
		t.Fatal("public canvas.frontend still carries focus_mode_key")
	}
	capabilityIcons := map[schemacontract.Capability]string{}
	for _, capability := range public.Capabilities {
		capabilityIcons[capability.ID] = capability.Icon
	}
	for id, icon := range map[schemacontract.Capability]string{
		"remesh": "network", "retexture": "paint-bucket", "rigging": "bone", "animation": "person-standing",
	} {
		if capabilityIcons[id] != icon {
			t.Errorf("public capability %s icon = %q, want %q", id, capabilityIcons[id], icon)
		}
	}
}

func TestStageActionsCloneIsIndependent(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	first := taskCatalog.Application()
	first.Canvas.Frontend.StageActions[0].ID = "mutated"
	first.Canvas.Frontend.StageActions[0].Kinds[0] = "mutated"
	second := taskCatalog.Application()
	if second.Canvas.Frontend.StageActions[0].ID != "merge" || second.Canvas.Frontend.StageActions[0].Kinds[0] != "board" {
		t.Fatalf("stage_actions leaked a mutation: %+v", second.Canvas.Frontend.StageActions[0])
	}
}

func TestFocusModeIsGoneFromTheKeyboardSchema(t *testing.T) {
	keyboardType := reflect.TypeOf(CanvasKeyboardSchema{})
	for index := 0; index < keyboardType.NumField(); index++ {
		if tag := keyboardType.Field(index).Tag.Get("json"); strings.Contains(tag, "focus") {
			t.Fatalf("canvas.frontend.keyboard still declares %q", tag)
		}
	}
	for key := range defaultMessagesForTest(t) {
		if strings.Contains(key, ".focus") {
			t.Errorf("focus mode text %q is still declared", key)
		}
	}
}

// defaultMessagesForTest returns the default catalog's application.content.messages.
func defaultMessagesForTest(t *testing.T) map[string]string {
	t.Helper()
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog.Application().Content.Messages
}

func TestStageActionsRejectInvalidDeclarations(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	inpaint := 2
	mutations := []struct {
		name   string
		mutate func(*ApplicationSchema)
		want   string
	}{
		{"empty table", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions = nil }, "stage_actions 不能为空"},
		{"empty id", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].ID = " " }, "id 不能为空"},
		{"duplicate id", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].ID = "merge" }, "id 重复"},
		{"no kinds", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Kinds = nil }, "kinds 不能为空"},
		{"unknown kind", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Kinds = []string{"image", "audio"} }, "无效对象种类"},
		{"unknown group", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Group = "identity" }, "group 无效"},
		{"empty group", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Group = "" }, "group 无效"},
		{"unknown cost hint", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].CostHint = "paid" }, "cost_hint 无效"},
		{"model call declared free", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].CostHint = "free" }, "calls_model"},
		{"free action calls the model", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[0].CallsModel = true }, "calls_model"},
		{"paid action does not call the model", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].CallsModel = false }, "calls_model"},
		{"empty icon", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Icon = "" }, "icon 不能为空"},
		{"unknown label key", func(a *ApplicationSchema) {
			a.Canvas.Frontend.StageActions[inpaint].LabelKey = "ui.studio.no_such_label"
		}, "label_key"},
		{"empty hint key", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].HintKey = "" }, "hint_key"},
		{"unknown target key", func(a *ApplicationSchema) {
			a.Canvas.Frontend.StageActions[inpaint].TargetKey = "ui.studio.no_such_target"
		}, "target_key"},
		{"unknown shortcut setting", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Shortcut = "no_such_key" }, "shortcut"},
		{"shortcut on a list setting", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Shortcut = "delete_keys" }, "shortcut"},
		{"shortcut on a number setting", func(a *ApplicationSchema) { a.Canvas.Frontend.StageActions[inpaint].Shortcut = "nudge_pixels" }, "shortcut"},
		{"missing approval cost text", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.hint.cost.approval_card") }, "ui.studio.hint.cost.approval_card"},
		{"missing process cost text", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.hint.cost.process_panel") }, "ui.studio.hint.cost.process_panel"},
		{"missing free cost text", func(a *ApplicationSchema) { delete(a.Content.Messages, "ui.studio.hint.cost.free") }, "ui.studio.hint.cost.free"},
	}
	if taskCatalog.Application().Canvas.Frontend.StageActions[inpaint].ID != "inpaint" {
		t.Fatalf("stage_actions[%d] is not inpaint", inpaint)
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			application := taskCatalog.Application()
			mutation.mutate(&application)
			err := ValidateApplicationSchema(application)
			if err == nil {
				t.Fatal("accepted an invalid stage_actions declaration")
			}
			if !strings.Contains(err.Error(), mutation.want) {
				t.Fatalf("error = %v, want it to mention %q", err, mutation.want)
			}
		})
	}
}

func TestStageActionShortcutMayNameAnyStringKeyboardSetting(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range []string{"space_code", "recenter_key", "help_key", "lock_shortcut"} {
		application := taskCatalog.Application()
		application.Canvas.Frontend.StageActions[2].Shortcut = setting
		if err := ValidateApplicationSchema(application); err != nil {
			t.Errorf("shortcut %q rejected: %v", setting, err)
		}
	}
	names := keyboardSettingNames(CanvasKeyboardSchema{})
	for _, listed := range []string{"delete_keys", "shape_cycle", "nudge_pixels", "reserved_shortcuts", "brush_size_step_ratio"} {
		if _, exists := names[listed]; exists {
			t.Errorf("keyboardSettingNames lists %q, which is not a single key", listed)
		}
	}
}

func TestProcessingCapabilitiesNeedAnIcon(t *testing.T) {
	taskCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	for _, configured := range taskCatalog.Application().Runtime.Frontend.ModelProcessingCapabilities {
		application := taskCatalog.Application()
		found := false
		for index := range application.Capabilities {
			if application.Capabilities[index].ID == configured {
				application.Capabilities[index].Icon = " "
				found = true
			}
		}
		if !found {
			t.Fatalf("processing capability %q is not declared", configured)
		}
		err := ValidateApplicationSchema(application)
		if err == nil || !strings.Contains(err.Error(), "icon") {
			t.Errorf("capability %q without an icon: error = %v", configured, err)
		}
	}
	// Capabilities that are not offered as toolbar steps do not need one.
	application := taskCatalog.Application()
	for index := range application.Capabilities {
		if application.Capabilities[index].ID == "text-to-3d" {
			application.Capabilities[index].Icon = ""
		}
	}
	if err := ValidateApplicationSchema(application); err != nil {
		t.Errorf("a generation capability without an icon was rejected: %v", err)
	}
}
