package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

type testProjectSource struct {
	projects map[string]bool
	contexts map[string]chat.ProjectContext
}

func (source testProjectSource) Exists(_ context.Context, ownerID, projectID string) (bool, error) {
	return source.projects[ownerID+"/"+projectID], nil
}

func (source testProjectSource) Context(_ context.Context, ownerID, projectID string) (chat.ProjectContext, error) {
	key := ownerID + "/" + projectID
	if value, exists := source.contexts[key]; exists {
		return value, nil
	}
	if source.projects[key] {
		return chat.ProjectContext{ID: projectID}, nil
	}
	return chat.ProjectContext{}, chat.ErrProjectNotFound
}

func TestCreateSessionInProjectValidatesOwnershipAndDefaultsSource(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Projects = testProjectSource{projects: map[string]bool{"owner-a/prj_own": true}}
	})
	session, err := service.CreateSessionInProject(context.Background(), "owner-a", "Owner", "Project chat", "prj_own")
	if err != nil {
		t.Fatal(err)
	}
	if session.ProjectID != "prj_own" || session.StyleLockSource != chat.StyleLockSourceProject {
		t.Fatalf("session project/source = %q/%q", session.ProjectID, session.StyleLockSource)
	}
	stored, found, err := repository.Session(context.Background(), "owner-a", session.ID)
	if err != nil || !found || stored.ProjectID != "prj_own" || stored.StyleLockSource != chat.StyleLockSourceProject {
		t.Fatalf("stored session = %+v found=%v err=%v", stored, found, err)
	}
	for _, probe := range []struct {
		name    string
		owner   string
		project string
	}{
		{"foreign project", "owner-b", "prj_own"},
		{"unknown project", "owner-a", "prj_unknown"},
	} {
		if _, err := service.CreateSessionInProject(context.Background(), probe.owner, "Owner", "Project chat", probe.project); !errors.Is(err, chat.ErrProjectNotFound) {
			t.Errorf("%s err = %v", probe.name, err)
		}
	}
	unassigned, err := service.CreateSession(context.Background(), "owner-a", "Owner", "Plain")
	if err != nil || unassigned.ProjectID != "" || unassigned.StyleLockSource != chat.StyleLockSourceProject {
		t.Fatalf("unassigned session = %+v err=%v", unassigned, err)
	}
}

func TestCreateSessionWithoutProjectSourceRejectsProject(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatService(t, repository)
	if _, err := service.CreateSessionInProject(context.Background(), "owner-a", "Owner", "Project chat", "prj_own"); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("project without source err = %v", err)
	}
}

func TestSetSessionProjectIsOwnerScoped(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Projects = testProjectSource{projects: map[string]bool{"owner-a/prj_own": true}}
	})
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	assigned, err := service.SetSessionProject(context.Background(), "owner-a", session.ID, "prj_own")
	if err != nil || assigned.ProjectID != "prj_own" {
		t.Fatalf("assign = %+v err=%v", assigned, err)
	}
	if _, err := service.SetSessionProject(context.Background(), "owner-b", session.ID, "prj_own"); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("foreign session err = %v", err)
	}
	if _, err := service.SetSessionProject(context.Background(), "owner-a", session.ID, "prj_unknown"); !errors.Is(err, chat.ErrProjectNotFound) {
		t.Fatalf("unknown project err = %v", err)
	}
	cleared, err := service.SetSessionProject(context.Background(), "owner-a", session.ID, "")
	if err != nil || cleared.ProjectID != "" {
		t.Fatalf("clear = %+v err=%v", cleared, err)
	}
}

func TestCreateSessionSeedsTheProjectBriefAndDefaults(t *testing.T) {
	repository := newMemoryChatRepository()
	projectID := "prj_own"
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Projects = testProjectSource{
			projects: map[string]bool{"owner-a/" + projectID: true},
			contexts: map[string]chat.ProjectContext{
				"owner-a/" + projectID: {
					ID: projectID, Name: "Star Harbor",
					StyleGuide: chat.ProjectStyleGuideContext{Enabled: true, Text: "warm ink",
						References: []string{"/api/v1/tasks/t1/assets/a1"}},
					Conventions: chat.ProjectConventionsContext{
						Brief: chat.ProductionBrief{"purpose": []byte(`"character"`)},
						Texts: map[string]string{"naming": "hero_"},
					},
					Assets: []chat.ProjectAssetContext{{
						Handle: "@project1", Kind: "character", Name: "Role A",
						Images: []string{"/api/v1/tasks/t1/assets/a1"},
					}},
				},
			},
		}
	})
	session, err := service.CreateSessionInProject(context.Background(), "owner-a", "Owner", "Project chat", projectID)
	if err != nil {
		t.Fatal(err)
	}
	if session.ProjectID != projectID || session.StyleLockSource != chat.StyleLockSourceProject {
		t.Fatalf("session = %+v", session)
	}
	if session.Brief.Text("purpose") != "character" {
		t.Fatalf("seeded brief = %#v", session.Brief)
	}
	assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
		OwnerID: "owner-a", SessionID: session.ID, Content: "draw role B",
	})
	if err != nil {
		t.Fatal(err)
	}
	turn, err := service.GetTurn(context.Background(), "owner-a", session.ID, assistant.ID)
	if err != nil {
		t.Fatal(err)
	}
	options := turn.Options
	if options.ProjectID != projectID || options.ProjectContext == nil ||
		options.ProjectContext.Name != "Star Harbor" || len(options.ProjectContext.Assets) != 1 {
		t.Fatalf("frozen project = %+v", options.ProjectContext)
	}
	lock := options.StyleLock
	if lock.Source != chat.StyleLockSourceProject || !lock.Enabled || lock.Text != "warm ink" ||
		len(lock.ProjectReferences) != 1 || lock.ProjectName != "Star Harbor" || lock.ProjectID != projectID {
		t.Fatalf("frozen project style lock = %+v", lock)
	}
}

func TestStartTurnFreezesTheThreeStyleSources(t *testing.T) {
	repository := newMemoryChatRepository()
	projectID := "prj_own"
	source := testProjectSource{
		projects: map[string]bool{"owner-a/" + projectID: true},
		contexts: map[string]chat.ProjectContext{
			"owner-a/" + projectID: {
				ID: projectID, Name: "P",
				StyleGuide: chat.ProjectStyleGuideContext{Enabled: true, Text: "project style",
					References: []string{"/api/v1/tasks/t1/assets/a1"}},
			},
		},
	}
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.Projects = source
		config.StyleLockSources = []string{"project", "session", "off"}
		config.MaximumStyleLockText = 100
		config.MaximumStyleLockReferences = 3
	})
	session, err := service.CreateSessionInProject(context.Background(), "owner-a", "Owner", "chat", projectID)
	if err != nil {
		t.Fatal(err)
	}
	freeze := func() chat.StyleLock {
		t.Helper()
		assistant, err := service.StartTurn(context.Background(), chat.StartTurn{
			OwnerID: "owner-a", SessionID: session.ID, Content: "draw",
		})
		if err != nil {
			t.Fatal(err)
		}
		turn, err := service.GetTurn(context.Background(), "owner-a", session.ID, assistant.ID)
		if err != nil {
			t.Fatal(err)
		}
		return turn.Options.StyleLock
	}
	if lock := freeze(); lock.Source != chat.StyleLockSourceProject || lock.Text != "project style" || len(lock.ProjectReferences) != 1 {
		t.Fatalf("project source = %+v", lock)
	}
	if _, err := service.SetStyleLockWithSource(context.Background(), "owner-a", session.ID,
		chat.StyleLockSourceSession, true, "session ink", nil); err != nil {
		t.Fatal(err)
	}
	if lock := freeze(); lock.Source != chat.StyleLockSourceSession || lock.Text != "session ink" || len(lock.ProjectReferences) != 0 {
		t.Fatalf("session source = %+v", lock)
	}
	if _, err := service.SetStyleLockWithSource(context.Background(), "owner-a", session.ID,
		chat.StyleLockSourceOff, true, "session ink", nil); err != nil {
		t.Fatal(err)
	}
	if lock := freeze(); lock.Source != chat.StyleLockSourceOff || lock.Enabled || lock.Text != "" || len(lock.ProjectReferences) != 0 {
		t.Fatalf("off source = %+v", lock)
	}
	// A project edit applies to the next turn, never a frozen one.
	source.contexts["owner-a/"+projectID] = chat.ProjectContext{
		ID: projectID, Name: "P", StyleGuide: chat.ProjectStyleGuideContext{Enabled: true, Text: "changed"},
	}
	if _, err := service.SetStyleLockWithSource(context.Background(), "owner-a", session.ID,
		chat.StyleLockSourceProject, true, "", nil); err != nil {
		t.Fatal(err)
	}
	if lock := freeze(); lock.Text != "changed" {
		t.Fatalf("next turn project edit = %+v", lock)
	}
}

func TestStyleLockSourceIsValidatedAndPreserved(t *testing.T) {
	repository := newMemoryChatRepository()
	service := newChatServiceWith(t, repository, func(config *chat.ServiceConfig) {
		config.StyleLockSources = []string{"project", "session", "off"}
		config.MaximumStyleLockText = 100
		config.MaximumStyleLockReferences = 3
	})
	session, err := service.CreateSession(context.Background(), "owner-a", "Owner", "chat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetStyleLockWithSource(context.Background(), "owner-a", session.ID, "bogus", true, "ink", nil); !errors.Is(err, chat.ErrStyleLockSourceInvalid) {
		t.Fatalf("invalid source err = %v", err)
	}
	lock, err := service.SetStyleLockWithSource(context.Background(), "owner-a", session.ID, chat.StyleLockSourceSession, true, "ink", nil)
	if err != nil || lock.Source != chat.StyleLockSourceSession {
		t.Fatalf("set source = %+v err=%v", lock, err)
	}
	preserved, err := service.SetStyleLock(context.Background(), "owner-a", session.ID, true, "changed", nil)
	if err != nil || preserved.Source != chat.StyleLockSourceSession {
		t.Fatalf("preserved source = %+v err=%v", preserved, err)
	}
	read, err := service.StyleLock(context.Background(), "owner-a", session.ID)
	if err != nil || read.Source != chat.StyleLockSourceSession || read.Text != "changed" {
		t.Fatalf("read lock = %+v err=%v", read, err)
	}
}
