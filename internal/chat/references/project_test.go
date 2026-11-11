package references_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat/references"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// projectRegistration accepts exactly one owner project's registered picture.
type projectRegistration struct {
	ownerID, projectID, taskID, assetID string
}

func (registration projectRegistration) RegisteredPicture(
	_ context.Context, ownerID, projectID, taskID, assetID string,
) (bool, error) {
	return ownerID == registration.ownerID && projectID == registration.projectID &&
		taskID == registration.taskID && assetID == registration.assetID, nil
}

func TestProjectRegistrationAllowsAnotherSessionTaskPictureOnlyWhenStored(t *testing.T) {
	ctx := context.Background()
	picture := []byte("\x89PNG\r\n\x1a\nproject-standard-picture")
	inputs := &storedInputs{assets: map[generation.AssetID]generation.InputAsset{}, bodies: map[generation.AssetID][]byte{}}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-other": {ID: "task-other", OwnerID: "owner-a", SessionID: "session-b"},
			"task-owner": {ID: "task-owner", OwnerID: "owner-b", SessionID: "session-b"},
		},
		contents: map[string]generation.AssetContent{
			"task-other/asset-a": {Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: int64(len(picture))},
			"task-owner/asset-a": {Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: int64(len(picture))},
		},
	}
	resolver := limitedResolver(t, inputs, resolverLimits{
		upload: 1024, vision: 1024, tasks: tasks,
		project: projectRegistration{ownerID: "owner-a", projectID: "prj_1", taskID: "task-other", assetID: "asset-a"},
	})
	reference := "/configured/tasks/task-other/assets/asset-a"
	ids, err := resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-a", ProjectID: "prj_1",
	}, []string{reference})
	if err != nil || len(ids) != 1 || inputs.uploads != 1 {
		t.Fatalf("registered project picture ids=%v err=%v uploads=%d", ids, err, inputs.uploads)
	}
	// The exact pair is required: another project, no project and another
	// owner are all refused. A registered project picture is readable from
	// any session of that project.
	for name, scope := range map[string]references.ReferenceScope{
		"another project": {Owner: "owner-a", SessionID: "session-a", ProjectID: "prj_2"},
		"no project":      {Owner: "owner-a", SessionID: "session-a"},
		"other owner":     {Owner: "owner-b", SessionID: "session-a", ProjectID: "prj_1"},
	} {
		_, err := resolver.Resolve(ctx, scope, []string{reference})
		if !errors.Is(err, references.ErrUnsupportedReference) {
			t.Errorf("%s err = %v", name, err)
		}
	}
	// The same session needs no registration.
	ids, err = resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-b",
	}, []string{reference})
	if err != nil || len(ids) != 1 {
		t.Fatalf("own session ids=%v err=%v", ids, err)
	}
	// A foreign owner's Task is never read, even with a project registration
	// for another task.
	_, err = resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-b", SessionID: "session-a", ProjectID: "prj_1",
	}, []string{"/configured/tasks/task-other/assets/asset-a"})
	if !errors.Is(err, references.ErrUnsupportedReference) {
		t.Fatalf("foreign owner task err = %v", err)
	}
}

type failingProjectRegistration struct{}

func (failingProjectRegistration) RegisteredPicture(context.Context, string, string, string, string) (bool, error) {
	return false, errors.New("project store unavailable")
}

func TestProjectRegistrationFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	picture := []byte("\x89PNG\r\n\x1a\npicture")
	inputs := &storedInputs{assets: map[generation.AssetID]generation.InputAsset{}, bodies: map[generation.AssetID][]byte{}}
	tasks := &fakeTasks{
		tasks: map[generation.TaskID]generation.Task{
			"task-other": {ID: "task-other", OwnerID: "owner-a", SessionID: "session-b"},
		},
		contents: map[string]generation.AssetContent{
			"task-other/asset-a": {Body: io.NopCloser(bytes.NewReader(picture)), MediaType: "image/png", Size: int64(len(picture))},
		},
	}
	resolver := limitedResolver(t, inputs, resolverLimits{
		upload: 1024, vision: 1024, tasks: tasks, project: failingProjectRegistration{},
	})
	if _, err := resolver.Resolve(ctx, references.ReferenceScope{
		Owner: "owner-a", SessionID: "session-a", ProjectID: "prj_1",
	}, []string{"/configured/tasks/task-other/assets/asset-a"}); err == nil {
		t.Fatal("a failing project registration must refuse the reference")
	}
}
