package generationtool

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func handleExecutor(t *testing.T) (Executor, catalog.ChatSchema) {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application().Chat
	return Executor{chat: schema}, schema
}

func historyWithHandles(schema catalog.ChatSchema) []chat.Message {
	return []chat.Message{
		{Role: schema.Roles.User, ImageURLs: []string{"upload-url"}, Attachments: []chat.Attachment{
			{Handle: "@img1", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleUpload, ImageSlot: 1}}},
		{Role: schema.Roles.Assistant, ImageURLs: []string{"generated-url", "model-thumb"}, Attachments: []chat.Attachment{
			{Handle: "@img2", Kind: chat.AttachmentKindImage, Role: chat.AttachmentRoleGenerated, TaskID: "task-1", AssetID: "asset-1", ImageSlot: 1},
			{Handle: "@model1", Kind: chat.AttachmentKindModel, Role: chat.AttachmentRoleGenerated, TaskID: "task-3d", AssetID: "asset-3d", ImageSlot: 2}}},
		{Role: schema.Roles.User, Content: "again"},
	}
}

func TestReferenceHandlesResolveToTheSessionsPictures(t *testing.T) {
	executor, schema := handleExecutor(t)
	command := chat.ToolCommand{History: historyWithHandles(schema)}
	handles := schema.Context.ReferenceHandlesArgument
	indices := schema.Context.ToolReferenceArgument

	got, err := executor.selectReferences(command, map[string]json.RawMessage{handles: json.RawMessage(`["@img2","@img1"]`)})
	if err != nil || !reflect.DeepEqual(got, []string{"generated-url", "upload-url"}) {
		t.Fatalf("handles = %v, %v", got, err)
	}
	// A single string and a bare number-form handle are accepted too.
	got, err = executor.selectReferences(command, map[string]json.RawMessage{handles: json.RawMessage(`"@IMG1"`)})
	if err != nil || !reflect.DeepEqual(got, []string{"upload-url"}) {
		t.Fatalf("single handle = %v, %v", got, err)
	}
	// Indices keep working, alone and beside handles.
	got, err = executor.selectReferences(command, map[string]json.RawMessage{indices: json.RawMessage(`[2]`)})
	if err != nil || !reflect.DeepEqual(got, []string{"generated-url"}) {
		t.Fatalf("index = %v, %v", got, err)
	}
	got, err = executor.selectReferences(command, map[string]json.RawMessage{
		handles: json.RawMessage(`["@img1"]`), indices: json.RawMessage(`[2]`)})
	if err != nil || !reflect.DeepEqual(got, []string{"upload-url", "generated-url"}) {
		t.Fatalf("handles beside indices = %v, %v", got, err)
	}
}

func TestReferenceHandlesRefuseUnknownAndWrongKindWithSchemaWording(t *testing.T) {
	executor, schema := handleExecutor(t)
	command := chat.ToolCommand{History: historyWithHandles(schema)}
	handles := schema.Context.ReferenceHandlesArgument
	for token, want := range map[string]string{
		"@img9":    strings.ReplaceAll(schema.Context.UnknownHandleTemplate, "{handle}", "@img9"),
		"@model1":  strings.ReplaceAll(schema.Context.WrongHandleKindTemplate, "{handle}", "@model1"),
		"nonsense": strings.ReplaceAll(schema.Context.UnknownHandleTemplate, "{handle}", "nonsense"),
	} {
		_, err := executor.selectReferences(command, map[string]json.RawMessage{handles: json.RawMessage(`["` + token + `"]`)})
		if err == nil || err.Error() != want {
			t.Fatalf("%s: err = %v; want %q", token, err, want)
		}
	}
}

func TestReferenceHandlesNeverReachAnotherSessionsHistory(t *testing.T) {
	executor, schema := handleExecutor(t)
	// The history the server loads for this owner and session is the only
	// source of handles: a handle that the session never showed is unknown,
	// even when another session has one with the same number.
	command := chat.ToolCommand{History: []chat.Message{{Role: schema.Roles.User, Content: "no pictures here"}}}
	_, err := executor.selectReferences(command, map[string]json.RawMessage{
		schema.Context.ReferenceHandlesArgument: json.RawMessage(`["@img1"]`)})
	if err == nil {
		t.Fatal("a handle of another session was accepted")
	}
}

func TestSelectionAliasAndDefaultToSelection(t *testing.T) {
	executor, schema := handleExecutor(t)
	selection := []chat.Attachment{
		{Handle: "@img5", Kind: chat.AttachmentKindImage, URL: "selected-1"},
		{Handle: "@img6", Kind: chat.AttachmentKindImage, URL: "selected-2"},
		{Handle: "@model2", Kind: chat.AttachmentKindModel, TaskID: "task-3d"},
	}
	command := chat.ToolCommand{
		History: []chat.Message{{Role: schema.Roles.User, Content: "remove the background of this"}},
		Options: chat.TurnOptions{Selection: selection},
	}
	alias := schema.Context.SelectionAlias
	got, err := executor.selectReferences(command, map[string]json.RawMessage{
		schema.Context.ReferenceHandlesArgument: json.RawMessage(`["` + alias + `"]`)})
	if err != nil || !reflect.DeepEqual(got, []string{"selected-1", "selected-2"}) {
		t.Fatalf("alias = %v, %v (the model in the selection must not be an image)", got, err)
	}
	// A fresh request does not silently use the selection...
	if got, err := executor.selectReferences(command, nil); err != nil || len(got) != 0 {
		t.Fatalf("a plain tool inherited the selection: %v %v", got, err)
	}
	// ...but a tool whose Schema says it needs a picture does.
	command.Tool.DefaultToSelection = true
	if got, err := executor.selectReferences(command, nil); err != nil || !reflect.DeepEqual(got, []string{"selected-1", "selected-2"}) {
		t.Fatalf("default to selection = %v, %v", got, err)
	}
	// A picture sent with this very turn wins over the selection.
	command.History[0].ImageURLs = []string{"this-turn"}
	if got, err := executor.selectReferences(command, nil); err != nil || !reflect.DeepEqual(got, []string{"this-turn"}) {
		t.Fatalf("current turn picture = %v, %v", got, err)
	}
}

func TestDefaultToSelectionIsDeclaredByTheProductSchema(t *testing.T) {
	_, schema := handleExecutor(t)
	want := map[string]bool{"remove_background": true, "create_model_3d_from_image": true, "create_gaussian_splat": true, "create_multiview_images": true,
		"create_creative_lab_design": true}
	for _, tool := range schema.Tools {
		if tool.DefaultToSelection != want[tool.Name] {
			t.Fatalf("tool %s default_to_selection = %v", tool.Name, tool.DefaultToSelection)
		}
	}
}
