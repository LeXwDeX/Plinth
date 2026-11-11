package agenttools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

func projectAssetsSchema(t *testing.T) *catalog.ProjectSchema {
	t.Helper()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Application().Project == nil || loaded.Application().Project.Agent == nil {
		t.Fatal("default catalog must declare application.project.agent")
	}
	return loaded.Application().Project
}

func TestListProjectAssetsReadsOnlyTheFrozenProjectContext(t *testing.T) {
	project := projectAssetsSchema(t)
	executor := &Executor{project: project}
	executor.project.Agent.ListLimit = 2
	command := chat.ToolCommand{Options: chat.TurnOptions{
		ProjectID: "prj_1",
		ProjectContext: &chat.ProjectContext{
			ID: "prj_1", Name: "Star Harbor",
			Assets: []chat.ProjectAssetContext{
				{Handle: "@project1", Kind: "character", Name: "Role A", Description: "red coat",
					Images: []string{"/api/v1/tasks/t1/assets/a1"},
					Models: []chat.ProjectModelContext{{TaskID: "t9", AssetID: "m9"}}},
				{Handle: "@project2", Kind: "prop", Name: "Sword"},
				{Handle: "@project3", Kind: "other", Name: "Hidden"},
			},
		},
	}}
	result := executor.listProjectAssets(context.Background(), command)
	if !result.NoSubmission {
		t.Fatal("a read-only list must not submit anything")
	}
	var payload struct {
		ProjectID string `json:"project_id"`
		Project   string `json:"project"`
		Assets    []struct {
			Handle      string              `json:"handle"`
			Kind        string              `json:"kind"`
			Name        string              `json:"name"`
			Description string              `json:"description"`
			Images      []string            `json:"images"`
			Model       []map[string]string `json:"model"`
		} `json:"assets"`
		Note string `json:"note"`
	}
	if err := json.Unmarshal([]byte(result.AgentContent), &payload); err != nil {
		t.Fatalf("result = %q err=%v", result.AgentContent, err)
	}
	if payload.ProjectID != "prj_1" || payload.Project != "Star Harbor" {
		t.Fatalf("payload = %+v", payload)
	}
	if len(payload.Assets) != 2 || payload.Assets[0].Handle != "@project1" || payload.Assets[0].Name != "Role A" ||
		payload.Assets[0].Kind != "character" || payload.Assets[0].Description != "red coat" ||
		len(payload.Assets[0].Images) != 1 || len(payload.Assets[0].Model) != 1 ||
		payload.Assets[0].Model[0]["task_id"] != "t9" || payload.Assets[0].Model[0]["asset_id"] != "m9" {
		t.Fatalf("assets = %+v", payload.Assets)
	}
	if payload.Note == "" {
		t.Fatal("a truncated list must carry the Schema's limit note")
	}
}

func TestListProjectAssetsRefusesASessionOutsideAnyProject(t *testing.T) {
	project := projectAssetsSchema(t)
	executor := &Executor{project: project}
	result := executor.listProjectAssets(context.Background(), chat.ToolCommand{
		Options: chat.TurnOptions{ProjectID: "prj_1"},
	})
	if !result.NoSubmission || result.Error != project.Messages.UnknownProject {
		t.Fatalf("missing frozen context result = %+v", result)
	}
	result = executor.listProjectAssets(context.Background(), chat.ToolCommand{})
	if !result.NoSubmission || result.Error != project.Messages.UnknownProject {
		t.Fatalf("no project result = %+v", result)
	}
}
