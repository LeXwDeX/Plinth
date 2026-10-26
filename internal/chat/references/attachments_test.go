package references_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

type fakeCaptures struct {
	records map[string]capture.Record
}

func (captures fakeCaptures) Get(_ context.Context, owner, id string) (capture.Record, error) {
	record, found := captures.records[id]
	if !found || record.OwnerID != owner {
		return capture.Record{}, capture.ErrNotFound
	}
	return record, nil
}

func attachmentFixture(t *testing.T) *references.Resolver {
	t.Helper()
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{
		"upload-a":  {ID: "upload-a", OwnerID: "owner-a"},
		"upload-b":  {ID: "upload-b", OwnerID: "owner-b"},
		"shot-a":    {ID: "shot-a", OwnerID: "owner-a"},
		"shot-a-s2": {ID: "shot-a-s2", OwnerID: "owner-a"},
	}}
	tasks := &fakeTasks{tasks: map[generation.TaskID]generation.Task{
		"image-task": {ID: "image-task", OwnerID: "owner-a", SessionID: "session-1", State: generation.TaskSucceeded,
			Assets: []generation.Asset{{ID: "image-asset", Role: "configured-image-role", StorageKey: "k"}}},
		"loose-task": {ID: "loose-task", OwnerID: "owner-a", State: generation.TaskSucceeded,
			Assets: []generation.Asset{{ID: "loose-asset", Role: "configured-image-role", StorageKey: "k"}}},
		"model-task": {ID: "model-task", OwnerID: "owner-a", SessionID: "session-1", State: generation.TaskSucceeded,
			Assets: []generation.Asset{
				{ID: "model-asset", Role: "model-file", StorageKey: "k"},
				{ID: "thumb-asset", Role: "thumbnail", StorageKey: "k"}}},
		"running-model": {ID: "running-model", OwnerID: "owner-a", SessionID: "session-1", State: generation.TaskRunning,
			Assets: []generation.Asset{{ID: "running-asset", Role: "model-file", StorageKey: "k"}}},
		"other-session": {ID: "other-session", OwnerID: "owner-a", SessionID: "session-2", State: generation.TaskSucceeded,
			Assets: []generation.Asset{{ID: "other-asset", Role: "configured-image-role", StorageKey: "k"}, {ID: "other-model", Role: "model-file", StorageKey: "k"}}},
		"foreign-task": {ID: "foreign-task", OwnerID: "owner-b", SessionID: "session-1", State: generation.TaskSucceeded,
			Assets: []generation.Asset{{ID: "foreign-asset", Role: "configured-image-role", StorageKey: "k"}, {ID: "foreign-model", Role: "model-file", StorageKey: "k"}}},
	}}
	resolver, err := references.New(references.Config{
		Inputs: inputs, Tasks: tasks, Legacy: fakeLegacy{},
		InputAssetRoutes:    []string{"/configured/assets/{assetID}"},
		TaskAssetRoute:      "/configured/tasks/{taskID}/assets/{assetID}",
		GeneratedImageRoute: "/configured/generated/{filename}",
		UploadMaxBytes:      1024, GeneratedReferenceMaxBytes: 4096, GeneratedReferenceTooLargeTemplate: "too large {size} {limit}", DefaultMediaType: "application/octet-stream",
		DefaultAssetType: "configured-image", ReferenceFilename: "configured-reference",
		LegacyGeneratedImage: catalog.LegacyGeneratedImageRuntimeSchema{FilenameSeparator: "_", AssetRoles: []string{"configured-image-role"}},
		Captures: fakeCaptures{records: map[string]capture.Record{
			"capture-a": {Command: capture.Command{ID: "capture-a", OwnerID: "owner-a", SessionID: "session-1",
				SourceTaskID: "model-task", ModelAssetID: "model-asset", ScreenshotAssetID: "shot-a"}},
			"capture-s2": {Command: capture.Command{ID: "capture-s2", OwnerID: "owner-a", SessionID: "session-2",
				SourceTaskID: "model-task", ModelAssetID: "model-asset", ScreenshotAssetID: "shot-a-s2"}},
			"capture-b": {Command: capture.Command{ID: "capture-b", OwnerID: "owner-b", SessionID: "session-1",
				SourceTaskID: "foreign-task", ModelAssetID: "foreign-model", ScreenshotAssetID: "upload-b"}},
		}},
		ModelAssetRole: "model-file",
	})
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func verify(t *testing.T, resolver *references.Resolver, owner, session string, ref chat.AttachmentRef) (chat.Attachment, error) {
	t.Helper()
	return resolver.VerifyAttachment(context.Background(), owner, session, ref)
}

func TestVerifyAcceptsOwnedSessionObjects(t *testing.T) {
	resolver := attachmentFixture(t)
	image := chat.AttachmentKindImage

	got, err := verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "/configured/tasks/image-task/assets/image-asset", ImageSlot: 2})
	if err != nil || got.Role != chat.AttachmentRoleGenerated || got.TaskID != "image-task" || got.AssetID != "image-asset" || got.ImageSlot != 2 ||
		got.URL != "/configured/tasks/image-task/assets/image-asset" {
		t.Fatalf("task picture = %+v, %v", got, err)
	}
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "/configured/assets/upload-a"})
	if err != nil || got.Role != chat.AttachmentRoleUpload || got.AssetID != "upload-a" || got.TaskID != "" {
		t.Fatalf("input asset = %+v, %v", got, err)
	}
	// IDs alone are enough; the canonical URL is built by the server.
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, TaskID: "image-task", AssetID: "image-asset"})
	if err != nil || got.URL != "/configured/tasks/image-task/assets/image-asset" {
		t.Fatalf("by ID = %+v, %v", got, err)
	}
	// A picture of a Task made outside any chat is the owner's too.
	if _, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, TaskID: "loose-task", AssetID: "loose-asset"}); err != nil {
		t.Fatalf("unscoped task picture rejected: %v", err)
	}
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a"})
	if err != nil || got.Role != chat.AttachmentRoleCapture || got.CaptureID != "capture-a" || got.TaskID != "model-task" || got.AssetID != "shot-a" ||
		got.URL != "/configured/assets/shot-a" {
		t.Fatalf("capture = %+v, %v", got, err)
	}
	// A capture claim about a message picture binds to the capture's own screenshot.
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a", URL: "/configured/assets/shot-a", ImageSlot: 1})
	if err != nil || got.Role != chat.AttachmentRoleCapture || got.AssetID != "shot-a" || got.ImageSlot != 1 {
		t.Fatalf("capture with its screenshot = %+v, %v", got, err)
	}
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: chat.AttachmentKindModel, TaskID: "model-task", URL: "/configured/tasks/model-task/assets/thumb-asset"})
	if err != nil || got.Kind != chat.AttachmentKindModel || got.AssetID != "model-asset" || got.TaskID != "model-task" ||
		got.URL != "/configured/tasks/model-task/assets/thumb-asset" {
		t.Fatalf("model = %+v, %v", got, err)
	}
	// The thumbnail alone names the model's Task.
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: chat.AttachmentKindModel, URL: "/configured/tasks/model-task/assets/thumb-asset"})
	if err != nil || got.TaskID != "model-task" {
		t.Fatalf("model by thumbnail = %+v, %v", got, err)
	}
	got, err = verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "data:image/png;base64,QQ=="})
	if err != nil || got.Role != chat.AttachmentRoleUpload || got.AssetID != "" {
		t.Fatalf("data URI = %+v, %v", got, err)
	}
}

func TestVerifyRejectsOtherOwnersAndOtherSessions(t *testing.T) {
	resolver := attachmentFixture(t)
	image, model := chat.AttachmentKindImage, chat.AttachmentKindModel
	for name, test := range map[string]struct {
		owner, session string
		ref            chat.AttachmentRef
	}{
		"another owner's task picture":              {"owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "/configured/tasks/foreign-task/assets/foreign-asset"}},
		"another owner's task by ID":                {"owner-a", "session-1", chat.AttachmentRef{Kind: image, TaskID: "foreign-task", AssetID: "foreign-asset"}},
		"another owner's upload":                    {"owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "/configured/assets/upload-b"}},
		"another owner's capture":                   {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-b"}},
		"capture of another session":                {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-s2"}},
		"picture of another session's task":         {"owner-a", "session-1", chat.AttachmentRef{Kind: image, TaskID: "other-session", AssetID: "other-asset"}},
		"model of another owner":                    {"owner-a", "session-1", chat.AttachmentRef{Kind: model, TaskID: "foreign-task"}},
		"model of another session":                  {"owner-a", "session-1", chat.AttachmentRef{Kind: model, TaskID: "other-session"}},
		"model still running":                       {"owner-a", "session-1", chat.AttachmentRef{Kind: model, TaskID: "running-model"}},
		"model without a Task":                      {"owner-a", "session-1", chat.AttachmentRef{Kind: model}},
		"model asset that is not a model":           {"owner-a", "session-1", chat.AttachmentRef{Kind: model, TaskID: "model-task", AssetID: "thumb-asset"}},
		"mismatched asset for the task":             {"owner-a", "session-1", chat.AttachmentRef{Kind: image, TaskID: "image-task", AssetID: "other-asset"}},
		"URL and ID of different tasks":             {"owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "/configured/tasks/image-task/assets/image-asset", TaskID: "loose-task"}},
		"capture with a swapped asset":              {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a", AssetID: "upload-a"}},
		"capture claimed for another owned picture": {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a", URL: "/configured/assets/upload-a"}},
		"capture claimed for a task picture":        {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a", URL: "/configured/tasks/image-task/assets/image-asset"}},
		"capture claimed for a pasted picture":      {"owner-a", "session-1", chat.AttachmentRef{Kind: image, CaptureID: "capture-a", URL: "data:image/png;base64,QQ=="}},
		"unknown picture URL":                       {"owner-a", "session-1", chat.AttachmentRef{Kind: image, URL: "https://attacker.example/x.png"}},
		"model thumbnail of another task":           {"owner-a", "session-1", chat.AttachmentRef{Kind: model, TaskID: "model-task", URL: "/configured/tasks/image-task/assets/image-asset"}},
		"unknown kind":                              {"owner-a", "session-1", chat.AttachmentRef{Kind: "audio", TaskID: "image-task"}},
		"no session":                                {"owner-a", "", chat.AttachmentRef{Kind: image, TaskID: "image-task", AssetID: "image-asset"}},
		"no owner":                                  {"", "session-1", chat.AttachmentRef{Kind: image, TaskID: "image-task", AssetID: "image-asset"}},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := verify(t, resolver, test.owner, test.session, test.ref)
			if !errors.Is(err, chat.ErrAttachmentRejected) {
				t.Fatalf("accepted %+v -> %+v (err %v)", test.ref, got, err)
			}
			if got != (chat.Attachment{}) {
				t.Fatalf("rejected claim leaked %+v", got)
			}
		})
	}
}

func TestVerifyWithoutCaptureStoreRejectsCaptures(t *testing.T) {
	inputs := &fakeInputs{owned: map[generation.AssetID]generation.InputAsset{}}
	resolver := newResolver(t, inputs, &fakeTasks{}, fakeLegacy{})
	if _, err := verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: chat.AttachmentKindImage, CaptureID: "capture-a"}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("err = %v", err)
	}
	if _, err := verify(t, resolver, "owner-a", "session-1", chat.AttachmentRef{Kind: chat.AttachmentKindModel, TaskID: "t"}); !errors.Is(err, chat.ErrAttachmentRejected) {
		t.Fatalf("err = %v", err)
	}
}
