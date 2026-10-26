package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/adapters/sqlite"
)

func TestReadinessTracksWorkersDatabaseAndStorage(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	repo, err := sqlite.Open(filepath.Join(root, "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	state := &readinessState{ping: repo.CheckReadiness, storageRoot: root}
	if state.check(t.Context()) == nil {
		t.Fatal("starting application was ready")
	}
	state.coordinator.Store(true)
	state.chat.Store(true)
	if err := state.check(t.Context()); err != nil {
		t.Fatal(err)
	}
	realPing := state.ping
	state.ping = func(context.Context) error { return errors.New("database unavailable") }
	if state.check(t.Context()) == nil {
		t.Fatal("database failure was ready")
	}
	state.ping = realPing
	if err := state.check(t.Context()); err != nil {
		t.Fatalf("did not recover: %v", err)
	}
	state.coordinator.Store(false)
	if state.check(t.Context()) == nil {
		t.Fatal("stopped coordinator was ready")
	}
	state.coordinator.Store(true)
	if err := os.Remove(filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	if state.check(t.Context()) == nil {
		t.Fatal("unavailable storage was ready")
	}
	if err := os.Mkdir(filepath.Join(root, "tmp"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := repo.Close(); err != nil {
		t.Fatal(err)
	}
	if state.check(t.Context()) == nil {
		t.Fatal("closed SQLite connection was ready")
	}
}
