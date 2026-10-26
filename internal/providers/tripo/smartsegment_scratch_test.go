package tripo_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers/tripo"
)

// unusableProcessTempDir stands in for the container's /tmp, a 64 MB tmpfs
// that a 107 MB Rodin GLB does not fit into (#321 A): every file created
// there fails.
func unusableProcessTempDir(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", path)
}

func smartSegmentValidation(f *smartSegmentFixture) generation.InputValidationRequest {
	return generation.InputValidationRequest{OwnerID: f.request.OwnerID, Offering: f.request.Offering,
		Input: f.request.Input, InputAssets: f.request.InputAssets}
}

func providerFailure(t *testing.T, err error) *generation.Failure {
	t.Helper()
	var provider *generation.ProviderError
	if !errors.As(err, &provider) || provider.Failure == nil {
		t.Fatalf("not a provider failure: %v", err)
	}
	return provider.Failure
}

// Checking the frozen GLB reads it where it is stored; it never needs room for
// a copy, so a model the Offering declares (up to 150 MB) is not refused for
// the size of the process temp directory.
func TestSmartSegmentModelCheckDoesNotCopyIntoTheProcessTempDir(t *testing.T) {
	f := newSmartSegmentFixture(t, "model")
	unusableProcessTempDir(t)
	if err := f.adapter.ValidateInput(t.Context(), smartSegmentValidation(f)); err != nil {
		t.Fatalf("a valid GLB was refused because the process temp dir is full: %v", err)
	}
}

// The private upload keeps its copy of the checked bytes in the configured
// scratch directory (the storage volume), never in the process temp dir, and
// leaves nothing behind.
func TestSmartSegmentModelUploadUsesTheScratchDirectory(t *testing.T) {
	scratch := t.TempDir()
	f := newSmartSegmentFixture(t, "model", func(config *tripo.Config) { config.ScratchDir = scratch })
	unusableProcessTempDir(t)
	if _, err := f.start(t); err != nil {
		t.Fatalf("upload failed: %v", err)
	}
	if f.uploads.Load() != 1 || !bytes.Equal(f.uploaded, f.input.body) {
		t.Fatalf("uploads=%d, uploaded bytes match=%v", f.uploads.Load(), bytes.Equal(f.uploaded, f.input.body))
	}
	if entries, _ := os.ReadDir(scratch); len(entries) != 0 {
		t.Fatalf("scratch files left behind: %v", entries)
	}
}

// A server that cannot write its scratch copy has not received a bad input:
// the refusal is a retryable server condition, and nothing was uploaded.
func TestSmartSegmentScratchFailureIsNotAnInputError(t *testing.T) {
	f := newSmartSegmentFixture(t, "model", func(config *tripo.Config) {
		config.ScratchDir = filepath.Join(t.TempDir(), "missing")
	})
	_, err := f.start(t)
	failure := providerFailure(t, err)
	if failure.Class == generation.FailureInvalidInput || !failure.Retryable || f.uploads.Load() != 0 {
		t.Fatalf("scratch failure = %+v uploads=%d", failure, f.uploads.Load())
	}
}

// A refused model says what is wrong with it, so the Agent can repeat the
// reason instead of inventing one.
func TestSmartSegmentModelRefusalNamesTheReason(t *testing.T) {
	f := newSmartSegmentFixture(t, "model")
	f.input.body = append([]byte("NOTGLB"), f.input.body[6:]...)
	err := f.adapter.ValidateInput(t.Context(), smartSegmentValidation(f))
	failure := providerFailure(t, err)
	if failure.Class != generation.FailureInvalidInput || !strings.Contains(failure.SafeDetail, "GLB") {
		t.Fatalf("changed bytes = %+v", failure)
	}
	g := newSmartSegmentFixture(t, "model")
	g.input.body = g.input.body[:len(g.input.body)-4]
	failure = providerFailure(t, g.adapter.ValidateInput(t.Context(), smartSegmentValidation(g)))
	if failure.Class != generation.FailureInvalidInput || !strings.Contains(failure.SafeDetail, "字节") {
		t.Fatalf("short file = %+v", failure)
	}
}
