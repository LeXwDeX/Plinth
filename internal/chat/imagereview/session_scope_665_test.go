package imagereview

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// scopedInputs holds input assets of owner "owner" by ID.
type scopedInputs struct {
	assets map[generation.AssetID]generation.InputAsset
	data   []byte
	fault  error
}

func (inputs scopedInputs) OpenOwned(_ context.Context, owner generation.OwnerID,
	id generation.AssetID) (generation.InputAsset, io.ReadCloser, error) {
	if inputs.fault != nil {
		return generation.InputAsset{}, nil, inputs.fault
	}
	asset, found := inputs.assets[id]
	if !found || owner != "owner" {
		// As the input asset service answers a missing or foreign asset.
		return generation.InputAsset{}, nil, &generation.Error{Code: generation.ErrNotFound, Operation: "get_input_asset"}
	}
	asset.ID, asset.OwnerID = id, owner
	return asset, io.NopCloser(bytes.NewReader(inputs.data)), nil
}

func scopeTestScope() *chat.AgentReadScope {
	return &chat.AgentReadScope{
		TaskInSession: func(_ context.Context, _ generation.OwnerID, session string, task generation.TaskID) (bool, error) {
			return session == "branch" && (task == "task-own" || task == "task-parent"), nil
		},
		ProjectReference: func(_ context.Context, _ string, projectID string, reference project.Reference) (bool, error) {
			return projectID == "prj-1" && reference.InputAssetID == "upload-project" ||
				projectID == "prj-2" && reference.InputAssetID == "upload-other-project", nil
		},
		Refused: "读不到 {id}",
	}
}

// #665: a picture compare_model_reference names by asset ID is read only when
// the session may read it: an output of its own or an ancestor's Task, or an
// upload its project registers. A sibling session's output, another
// project's upload and an ID that does not exist get the same refusal.
func TestOpenAgentPictureReadsOnlyTheSessionScope665(t *testing.T) {
	inputs := scopedInputs{data: picture(t), assets: map[generation.AssetID]generation.InputAsset{
		"out-own":              {TaskID: "task-own", SessionID: "branch"},
		"out-parent":           {TaskID: "task-parent", SessionID: "parent"},
		"upload-project":       {},
		"out-sibling":          {TaskID: "task-sibling", SessionID: "sibling"},
		"upload-other-project": {},
	}}
	svc := service(t, inputs, nil)
	svc.scope = scopeTestScope()
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "branch"}
	command.Options.ProjectID = "prj-1"
	for _, id := range []string{"out-own", "out-parent", "upload-project"} {
		if data, err := svc.OpenAgentPicture(context.Background(), command, id); err != nil || len(data) == 0 {
			t.Fatalf("%s: err = %v", id, err)
		}
	}
	for _, id := range []string{"out-sibling", "upload-other-project", "missing"} {
		_, err := svc.OpenAgentPicture(context.Background(), command, id)
		var refusal *chat.ReadScopeRefusal
		if !errors.As(err, &refusal) || refusal.Message != "读不到 "+id {
			t.Fatalf("%s: err = %v, want the scope refusal", id, err)
		}
	}
}

// #665: a storage fault is not the scope refusal: the Agent reads a generic
// failure to read, distinct from "out of scope", and the cause stays wrapped.
func TestOpenAgentPictureReportsAStorageFaultApartFromTheScope665(t *testing.T) {
	fault := errors.New("disk gone")
	svc := service(t, scopedInputs{data: picture(t), fault: &generation.Error{Code: generation.ErrUnavailable,
		Operation: "open_authorized_input_asset", Cause: fault}}, nil)
	svc.scope = scopeTestScope()
	command := chat.ToolCommand{OwnerID: "owner", SessionID: "branch"}
	_, err := svc.OpenAgentPicture(context.Background(), command, "out-own")
	var refusal *chat.ReadScopeRefusal
	var failure *PictureReadFailure
	if errors.As(err, &refusal) || !errors.As(err, &failure) || !errors.Is(err, fault) {
		t.Fatalf("err = %v, want a read failure apart from the scope refusal", err)
	}
	if strings.Contains(err.Error(), "读不到") || strings.Contains(err.Error(), "disk gone") {
		t.Fatalf("failure text = %q", err.Error())
	}
	// A fault of the scope read itself is a failure too, not a refusal.
	svc = service(t, scopedInputs{data: picture(t), assets: map[generation.AssetID]generation.InputAsset{
		"out-x": {TaskID: "task-x", SessionID: "other"}}}, nil)
	svc.scope = scopeTestScope()
	svc.scope.TaskInSession = func(context.Context, generation.OwnerID, string, generation.TaskID) (bool, error) {
		return false, fault
	}
	if _, err := svc.OpenAgentPicture(context.Background(), command, "out-x"); errors.As(err, &refusal) ||
		!errors.Is(err, fault) {
		t.Fatalf("scope read fault: err = %v", err)
	}
}

// #665: inspect_image reads only handles of the session's history and
// verified selection. A branch's copied message keeps its ancestor's picture
// readable; a sibling session's picture is no handle of this session.
func TestInspectImageReadsOnlyHandlesOfTheSession665(t *testing.T) {
	svc := service(t, nil, fakeTasks{data: picture(t)})
	svc.scope = scopeTestScope()
	branch := command(`{"image":"@img1"}`, nil)
	branch.SessionID = "branch"
	branch.History[0].Attachments[0].TaskID = "task-parent"
	if result, err := svc.Inspect(context.Background(), branch); err != nil || result.Error != "" {
		t.Fatalf("ancestor picture = %+v err=%v", result, err)
	}
	sibling := command(`{"image":"@img2"}`, nil)
	sibling.SessionID = "branch"
	if result, _ := svc.Inspect(context.Background(), sibling); !strings.Contains(result.Error, "unknown @img2") {
		t.Fatalf("a picture outside the session = %+v", result)
	}
}
