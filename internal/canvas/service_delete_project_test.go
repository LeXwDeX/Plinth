package canvas

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The runner is found by a type assertion, so a rename on either side would
// silently turn the protocol off: the chat service must keep satisfying it.
var _ SessionDeletionRunner = (*chat.Service)(nil)

// deletingRepository answers GetProject with a fixed project and records the
// deletion of project and session.
type deletingRepository struct {
	Repository
	project Project
	found   bool
	log     *[]string
}

func (repository *deletingRepository) GetProject(context.Context, string, string) (Project, bool, error) {
	return repository.project, repository.found, nil
}

func (repository *deletingRepository) DeleteProjectAndSession(context.Context, string, string) (bool, error) {
	*repository.log = append(*repository.log, "delete rows")
	return true, nil
}

// deletionRunnerSessions is a session service that runs a deletion under the
// stop-the-turn protocol, as the chat service does.
type deletionRunnerSessions struct {
	SessionService
	log *[]string
	err error
}

func (sessions deletionRunnerSessions) RunSessionDeletion(
	ctx context.Context,
	ownerID, sessionID string,
	deleteRows func(context.Context) (bool, error),
) (bool, error) {
	*sessions.log = append(*sessions.log, "stop "+ownerID+"/"+sessionID)
	if sessions.err != nil {
		return false, sessions.err
	}
	deleted, err := deleteRows(ctx)
	*sessions.log = append(*sessions.log, "resume or clear")
	return deleted, err
}

func newDeleteTestService(t *testing.T, repository Repository, sessions SessionService) *Service {
	t.Helper()
	service, err := New(Config{
		Repository:                       repository,
		Sessions:                         sessions,
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
	return service
}

// A canvas project that owns a chat session deletes the session with it in one
// transaction, which the repository runs. The session's running turn must be
// stopped before that, exactly as for a deletion of the chat session itself
// (#384): the deletion goes through the session service's protocol.
func TestDeleteProjectStopsTheOwnedSessionsTurnBeforeDeletingRows(t *testing.T) {
	var log []string
	repository := &deletingRepository{
		project: Project{ID: "project", OwnerID: "owner", SessionID: "session", OwnsSession: true},
		found:   true,
		log:     &log,
	}
	service := newDeleteTestService(t, repository, deletionRunnerSessions{log: &log})
	if err := service.DeleteProject(context.Background(), "owner", "project"); err != nil {
		t.Fatal(err)
	}
	want := []string{"stop owner/session", "delete rows", "resume or clear"}
	if len(log) != len(want) || log[0] != want[0] || log[1] != want[1] || log[2] != want[2] {
		t.Fatalf("order = %v, want %v", log, want)
	}
}

// A project that does not own its session, or a session service that has no
// turns to stop, deletes the project directly, as before.
func TestDeleteProjectWithoutAnOwnedSessionDeletesDirectly(t *testing.T) {
	for name, project := range map[string]Project{
		"session not owned": {ID: "project", OwnerID: "owner", SessionID: "session"},
		"no session":        {ID: "project", OwnerID: "owner", OwnsSession: true},
	} {
		t.Run(name, func(t *testing.T) {
			var log []string
			repository := &deletingRepository{project: project, found: true, log: &log}
			service := newDeleteTestService(t, repository, deletionRunnerSessions{log: &log})
			if err := service.DeleteProject(context.Background(), "owner", "project"); err != nil {
				t.Fatal(err)
			}
			if len(log) != 1 || log[0] != "delete rows" {
				t.Fatalf("order = %v, want a direct deletion", log)
			}
		})
	}
	t.Run("session service without the protocol", func(t *testing.T) {
		var log []string
		repository := &deletingRepository{
			project: Project{ID: "project", OwnerID: "owner", SessionID: "session", OwnsSession: true},
			found:   true,
			log:     &log,
		}
		service := newDeleteTestService(t, repository, ownedSessionService{})
		if err := service.DeleteProject(context.Background(), "owner", "project"); err != nil {
			t.Fatal(err)
		}
		if len(log) != 1 || log[0] != "delete rows" {
			t.Fatalf("order = %v, want a direct deletion", log)
		}
	})
}

func TestDeleteProjectOfAMissingProjectStopsNothing(t *testing.T) {
	var log []string
	repository := &deletingRepository{log: &log}
	service := newDeleteTestService(t, repository, deletionRunnerSessions{log: &log})
	if err := service.DeleteProject(context.Background(), "owner", "missing"); !errors.Is(err, ErrProjectNotFound) {
		t.Fatalf("DeleteProject = %v, want ErrProjectNotFound", err)
	}
	if len(log) != 0 {
		t.Fatalf("a missing project touched %v", log)
	}
}

// The refusal of the session deletion (work in flight) reaches the caller as
// the typed error, so the boundary can answer 409.
func TestDeleteProjectReportsARefusedSessionDeletion(t *testing.T) {
	var log []string
	repository := &deletingRepository{
		project: Project{ID: "project", OwnerID: "owner", SessionID: "session", OwnsSession: true},
		found:   true,
		log:     &log,
	}
	service := newDeleteTestService(t, repository,
		deletionRunnerSessions{log: &log, err: chat.ErrSessionDeletionBlocked})
	err := service.DeleteProject(context.Background(), "owner", "project")
	if !errors.Is(err, chat.ErrSessionDeletionBlocked) {
		t.Fatalf("DeleteProject = %v, want ErrSessionDeletionBlocked", err)
	}
	for _, entry := range log {
		if entry == "delete rows" {
			t.Fatal("the project was deleted although the session deletion was refused")
		}
	}
}
