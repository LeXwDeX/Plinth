package generationtool

import (
	"reflect"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

func TestFailureMessageMeteredNoAutoRetryText(t *testing.T) {
	var chat catalog.ChatSchema
	chat.Presentation.Messages.GenerationFailed = "generic"
	chat.Presentation.Messages.GenerationFailedByClass = map[string]string{string(generation.FailureIntegrity): "by class"}
	chat.Presentation.Messages.GenerationFailedMeteredByClass = map[string]string{string(generation.FailureIntegrity): "metered"}
	integrity := &generation.Failure{Class: generation.FailureIntegrity}
	if got := FailureMessage(chat, true, integrity); got != "metered" {
		t.Fatalf("metered = %q", got)
	}
	if got := FailureMessage(chat, false, integrity); got != "by class" {
		t.Fatalf("unmetered = %q", got)
	}
	if got := FailureMessage(chat, true, &generation.Failure{Class: generation.FailureRejected}); got != "generic" {
		t.Fatalf("other class = %q", got)
	}
}

func TestDefaultCatalogDeclaresMeteredIntegrityFailureText(t *testing.T) {
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := cat.Application().Chat
	if FailureMessage(chat, true, &generation.Failure{Class: generation.FailureIntegrity}) == chat.Presentation.Messages.GenerationFailed {
		t.Fatal("default catalog has no metered integrity failure text")
	}
}

func TestResultAssetURLsOfAModelToolIsOnlyTheFirstPicture(t *testing.T) {
	var schema catalog.ChatSchema
	schema.Context.AssetURLTemplate = "/api/v1/tasks/{task_id}/assets/{asset_id}"
	// A text-to-3D result: the model, the render of it, and the 2D picture the
	// provider drew before modelling. Only the first picture is the cover.
	model := generation.Task{ID: "task-3d", Assets: []generation.Asset{
		{ID: "asset-model", Role: "model"},
		{ID: "asset-render", Role: "thumbnail"},
		{ID: "asset-drawn", Role: "thumbnail"},
	}}
	modelTool := catalog.ChatToolSchema{ResultAttachmentKind: chat.AttachmentKindModel, ResultAssetRoles: []string{"thumbnail"}}
	if got, want := ResultAssetURLs(schema, modelTool, model), []string{"/api/v1/tasks/task-3d/assets/asset-render"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("model tool pictures = %v; want %v", got, want)
	}
	// The other pictures stay among the Task's assets.
	if len(model.Assets) != 3 {
		t.Fatalf("the Task's assets were changed: %+v", model.Assets)
	}
	// A model without a picture of the declared role has none to show.
	if got := ResultAssetURLs(schema, modelTool, generation.Task{ID: "task-3d", Assets: model.Assets[:1]}); len(got) != 0 {
		t.Fatalf("pictures of a model without thumbnail = %v", got)
	}

	// An image tool, or one that declares no attachment kind, keeps every picture.
	images := generation.Task{ID: "task-2d", Assets: []generation.Asset{
		{ID: "asset-a", Role: "image"}, {ID: "asset-b", Role: "image"}, {ID: "asset-other", Role: "thumbnail"},
	}}
	want := []string{"/api/v1/tasks/task-2d/assets/asset-a", "/api/v1/tasks/task-2d/assets/asset-b"}
	for _, kind := range []string{chat.AttachmentKindImage, ""} {
		imageTool := catalog.ChatToolSchema{ResultAttachmentKind: kind, ResultAssetRoles: []string{"image"}}
		if got := ResultAssetURLs(schema, imageTool, images); !reflect.DeepEqual(got, want) {
			t.Fatalf("image tool (kind %q) pictures = %v; want %v", kind, got, want)
		}
	}
}

// #326 N2: a step whose source's original task the provider no longer keeps
// says so in the chat, with what works, instead of a bare 生成失败.
func TestDefaultCatalogNamesAnExpiredSourceInTheChat(t *testing.T) {
	cat, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	chat := cat.Application().Chat
	message := FailureMessage(chat, true, &generation.Failure{Class: generation.FailureSourceExpired})
	for _, want := range []string{"原任务", "已过期", "已保存的模型文件"} {
		if !strings.Contains(message, want) {
			t.Fatalf("source_expired chat text %q lacks %q", message, want)
		}
	}
}
