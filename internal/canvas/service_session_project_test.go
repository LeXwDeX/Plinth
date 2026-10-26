package canvas

import (
	"context"
	"errors"
	"testing"
)

type concurrentFallbackRepository struct {
	Repository
	lookups  int
	creates  int
	existing Project
}

func (repository *concurrentFallbackRepository) GetProjectBySession(
	context.Context,
	string,
	string,
) (Project, bool, error) {
	repository.lookups++
	if repository.lookups == 1 {
		return Project{}, false, nil
	}
	return repository.existing, true, nil
}

func (repository *concurrentFallbackRepository) CreateProject(
	context.Context,
	Project,
) (Project, error) {
	repository.creates++
	return Project{}, errors.New("project already created concurrently")
}

type ownedSessionService struct {
	SessionService
}

func (ownedSessionService) OwnsSession(context.Context, string, string) (bool, error) {
	return true, nil
}

func TestGetOrCreateProjectForSessionConcurrentFallbackIsNotCreated(t *testing.T) {
	existing := Project{ID: "concurrent-project", OwnerID: "owner", SessionID: "session"}
	repository := &concurrentFallbackRepository{existing: existing}
	service, err := New(Config{
		Repository:                       repository,
		Sessions:                         ownedSessionService{},
		DefaultName:                      "Canvas",
		DefaultViewport:                  `{"zoom":1}`,
		DefaultBGStyle:                   "dots",
		ProjectIDPrefix:                  "canvas",
		MaximumBitmapNodes:               10,
		MaximumPlacedResults:             10,
		MaximumPlacedResultKeyCharacters: 32,
		MinimumFrameDimension:            64,
		MaximumFrameDimension:            8192,
		CoordinateVersion:                2,
		NodeDefaults:                     Node{Width: 1, Height: 1, Data: "{}"},
	})
	if err != nil {
		t.Fatal(err)
	}

	project, created, err := service.GetOrCreateProjectForSession(
		context.Background(), "owner", "session", "Canvas",
	)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("concurrent fallback reported a newly created project")
	}
	if project.ID != existing.ID || repository.lookups != 2 || repository.creates != 1 {
		t.Fatalf("project = %#v, lookups=%d creates=%d", project, repository.lookups, repository.creates)
	}
}
