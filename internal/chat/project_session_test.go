package chat_test

import (
	"context"
	"errors"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

type testProjectSource struct {
	projects map[string]bool
}

func (source testProjectSource) Exists(_ context.Context, ownerID, projectID string) (bool, error) {
	return source.projects[ownerID+"/"+projectID], nil
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
