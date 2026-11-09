package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/testutil/fakeupstream/registry"
	"github.com/LeXwDeX/Plinth/internal/testutil/wirefixture"
)

func worktreeRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// The official run's upstream recorder (#220) is off without switches and
// refuses half-set ones before any request is sent.
func TestOfficialRecorderIsOffByDefaultAndRefusesHalfSetSwitches(t *testing.T) {
	profile := registry.Profiles()["tripo/v3"]
	t.Setenv(wirefixture.EnvUpstreamRecordDir, "")
	t.Setenv(wirefixture.EnvUpstreamRecordAck, "")
	recorder, _, err := officialRecorder(profile, worktreeRoot(t), profile.Official)
	if err != nil || recorder != nil {
		t.Fatalf("recorder must be off by default: %v", err)
	}
	t.Setenv(wirefixture.EnvUpstreamRecordAck, "1")
	if _, _, err := officialRecorder(profile, worktreeRoot(t), profile.Official); !errors.Is(err, wirefixture.ErrUpstreamRecordRefused) {
		t.Fatalf("ACK without a directory must be refused, got %v", err)
	}
}

func TestDraftCommandRequiresPrivateDirectories(t *testing.T) {
	if err := run([]string{"draft"}); err == nil || !strings.Contains(err.Error(), "required") {
		t.Fatalf("draft without flags must fail, got %v", err)
	}
	open := t.TempDir()
	if err := os.Chmod(open, 0o755); err != nil {
		t.Fatal(err)
	}
	err := run([]string{"draft", "-records", open, "-out", open, "-name", "x", "-worktree", worktreeRoot(t)})
	if err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("a non-0700 records directory must be refused, got %v", err)
	}
	empty := t.TempDir()
	if err := os.Chmod(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"draft", "-records", empty, "-out", empty, "-name", "x", "-worktree", worktreeRoot(t)}); !errors.Is(err, wirefixture.ErrInvalidExchange) {
		t.Fatalf("no records must not produce a draft, got %v", err)
	}
}
