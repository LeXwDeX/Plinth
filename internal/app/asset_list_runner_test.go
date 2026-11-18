package app

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// catalogFileWithAssetListExecution writes the shipped Catalog with
// application.asset_list.execution.enabled switched, so the assembly test can
// observe the runner gate without another fixture Catalog.
func catalogFileWithAssetListExecution(t *testing.T, enabled bool) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(catalog.DefaultDocument(), &document); err != nil {
		t.Fatal(err)
	}
	application, ok := document["application"].(map[string]any)
	if !ok {
		t.Fatal("default catalog has no application section")
	}
	assetList, ok := application["asset_list"].(map[string]any)
	if !ok {
		t.Fatal("default catalog has no application.asset_list section")
	}
	execution, ok := assetList["execution"].(map[string]any)
	if !ok {
		t.Fatal("default catalog has no application.asset_list.execution section")
	}
	execution["enabled"] = enabled
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "catalog.json")
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func newAssetListRunnerTestApplication(t *testing.T, catalogFile string) *Application {
	t.Helper()
	root := t.TempDir()
	application, err := New(Config{
		CatalogFile:               catalogFile,
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		RuntimeSettings:           map[string]string{"llm_model": "test-model"},
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "development",
		DevelopmentUser:           "owner-a",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = application.generation.Close() })
	return application
}

// #328 PR5d: the runner is assembled only for a declared execution section
// with enabled=true; the shipped Catalog keeps it off.
func TestAssetListRunnerIsAssembledOnlyWhenExecutionIsEnabled(t *testing.T) {
	disabled := newAssetListRunnerTestApplication(t, "")
	if disabled.assetListRunner != nil {
		t.Fatal("the runner was assembled for the shipped disabled execution section")
	}

	enabled := newAssetListRunnerTestApplication(t,
		catalogFileWithAssetListExecution(t, true))
	if enabled.assetListRunner == nil {
		t.Fatal("the runner was not assembled for an enabled execution section")
	}
}

// Run counts the runner among its background components and still shuts down
// promptly: the loop stops with runCtx instead of holding the shutdown open.
func TestApplicationRunStopsAnEnabledAssetListRunner(t *testing.T) {
	application := newAssetListRunnerTestApplication(t,
		catalogFileWithAssetListExecution(t, true))
	if application.assetListRunner == nil {
		t.Fatal("the runner was not assembled for an enabled execution section")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan error, 1)
	go func() { done <- application.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("application shutdown timed out with the asset-list runner")
	}
}
