package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// acceptingAssetSource verifies nothing: the adapter test only checks how
// stored references become frozen URLs.
type acceptingAssetSource struct{}

func (acceptingAssetSource) Picture(_ context.Context, _ string, reference project.Reference) (project.Reference, error) {
	return reference, nil
}

func (acceptingAssetSource) Model(_ context.Context, _ string, reference project.Reference) (project.Reference, error) {
	return reference, nil
}

func TestProjectChatSourceFreezesOwnerProjectAsCanonicalURLsAndHandles(t *testing.T) {
	context := context.Background()
	loaded, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := loaded.Application()
	if application.Project == nil {
		t.Fatal("default catalog must declare application.project")
	}
	repository, err := sqlite.Open(filepath.Join(t.TempDir(), "project-source.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = repository.Close() })
	service, err := project.New(repository.ProjectRepository(), project.Config{
		Schema: *application.Project, Sources: acceptingAssetSource{},
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(context, "owner-a", "Star Harbor")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetStyleGuide(context, "owner-a", created.ID, project.StyleGuide{
		Enabled: true, Text: "warm ink",
		References: []project.Reference{
			{InputAssetID: "upload 1"},
			{TaskID: "task-1", AssetID: "asset-1"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetConventions(context, "owner-a", created.ID, project.Conventions{
		Brief: []byte(`{"purpose":"character"}`),
		Texts: map[string]string{"naming": "hero_"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAsset(context, "owner-a", created.ID, project.AssetInput{
		Kind: "character", Name: "Role A",
		Images: []project.Reference{{TaskID: "task-2", AssetID: "asset-2"}},
	}); err != nil {
		t.Fatal(err)
	}
	source := newProjectChatSource(service, *application.Project, application.Runtime.HTTP.Routes)
	frozen, err := source.Context(context, "owner-a", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.ID != created.ID || frozen.Name != "Star Harbor" {
		t.Fatalf("frozen = %+v", frozen)
	}
	if !frozen.StyleGuide.Enabled || frozen.StyleGuide.Text != "warm ink" {
		t.Fatalf("style guide = %+v", frozen.StyleGuide)
	}
	if len(frozen.StyleGuide.References) != 2 ||
		frozen.StyleGuide.References[0] != "/api/v1/assets/upload%201" ||
		frozen.StyleGuide.References[1] != "/api/v1/tasks/task-1/assets/asset-1" {
		t.Fatalf("style references = %v", frozen.StyleGuide.References)
	}
	if frozen.Conventions.Brief.Text("purpose") != "character" || frozen.Conventions.Texts["naming"] != "hero_" {
		t.Fatalf("conventions = %+v", frozen.Conventions)
	}
	if len(frozen.Assets) != 1 || frozen.Assets[0].Handle != "@project1" ||
		frozen.Assets[0].Name != "Role A" || len(frozen.Assets[0].Images) != 1 ||
		frozen.Assets[0].Images[0] != "/api/v1/tasks/task-2/assets/asset-2" {
		t.Fatalf("assets = %+v", frozen.Assets)
	}
	if _, err := source.Context(context, "owner-b", created.ID); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("foreign project err = %v", err)
	}
	if _, err := source.Context(context, "owner-a", "prj_missing"); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("unknown project err = %v", err)
	}
}
