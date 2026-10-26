package generation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/assets/local"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/providers"
)

func TestInlineRealStoreFailureHasSafeDiagnosticAndNeverResubmits(t *testing.T) {
	root := filepath.Join(t.TempDir(), "PRIVATE_PATH_MARKER")
	store, err := local.New(root)
	if err != nil {
		t.Fatal(err)
	}
	adapter := &inlineImageAdapter{data: validInlineImage(t, 0)}
	h := newHarness(t, adapter, store)
	registry := providers.NewRegistry()
	if err := registry.Register("openai-images.v1", adapter); err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	module, err := generation.New(generation.Dependencies{Repository: h.repository, Catalog: product, Providers: registry, Assets: store, Clock: h.clock, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	h.module = module
	id := submitInlineImage(t, h)
	if err := os.Remove(filepath.Join(root, "tmp")); err != nil {
		t.Fatal(err)
	}
	h.reconcile(t)
	h.reconcile(t)
	task, err := module.Get(context.Background(), "inline", id)
	if err != nil {
		t.Fatal(err)
	}
	if task.State != generation.TaskFailed || task.Failure.Class != generation.FailureIntegrity || adapter.starts != 1 || len(task.Assets) != 0 {
		t.Fatal("failure changed state or repeated paid dispatch")
	}
	if task.Offering.ExecutionMessages.InlineStoreFailed == "" || task.Failure.SafeDetail != task.Offering.ExecutionMessages.InlineStoreFailed {
		t.Fatal("public inline failure message changed")
	}
	aggregate, err := h.repository.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if len(aggregate.Claims) != 0 {
		t.Fatal("rejected inline content was persisted")
	}
	if strings.Contains(logs.String(), "PRIVATE_PATH_MARKER") {
		t.Fatal("private path reached logs")
	}
	var found bool
	for _, line := range bytes.Split(logs.Bytes(), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			t.Fatal(err)
		}
		if fields["event"] != "materialization.inline_storage_diagnostic" {
			continue
		}
		found = true
		if fields["stage"] != "store" || fields["storage_stage"] != "directory_check" || fields["storage_reason"] != "io_failure" || fields["errno"] != "not_found" || fields["mime"] != "image/png" || fields["bytes"] != float64(len(adapter.data)) {
			t.Fatalf("unexpected diagnostic: %v", fields)
		}
		for _, key := range []string{"error", "path", "body", "locator", "opaque"} {
			if _, ok := fields[key]; ok {
				t.Fatal("raw private field logged")
			}
		}
	}
	if !found {
		t.Fatal("missing server diagnostic")
	}
	public, _ := json.Marshal(task.Failure)
	if strings.Contains(string(public), "directory_check") || strings.Contains(string(public), "PRIVATE_PATH_MARKER") || strings.Contains(string(public), "not_found") {
		t.Fatal("server diagnostic reached public failure")
	}
}
