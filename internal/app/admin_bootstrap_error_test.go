package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/authentication"
)

// TestApplicationWeakAdminPasswordNamesTheVariable 锁定自部署首次启动的报错
// （#251）：ADMIN_PASSWORD 短于 Schema 最小长度时，启动错误写明变量名与
// 最小字符数，而不是只有 "authentication: weak password"；原错误仍可识别。
func TestApplicationWeakAdminPasswordNamesTheVariable(t *testing.T) {
	root := t.TempDir()
	_, err := New(Config{
		RuntimeSettings:           map[string]string{"llm_model": "test-model"},
		Environment:               "development",
		Address:                   "127.0.0.1:0",
		DatabasePath:              filepath.Join(root, "art-ai.db"),
		StorageRoot:               filepath.Join(root, "storage"),
		LegacyGeneratedImagesRoot: filepath.Join(root, "generated"),
		ProviderMode:              "mock",
		AgentMode:                 "mock",
		AuthMode:                  "session",
		SessionSecret:             strings.Repeat("s", 32),
		AdminUsername:             "admin",
		AdminPassword:             "abc",
		AdminName:                 "Administrator",
		AdminEmail:                "admin@example.test",
	}, discardLogger())
	if err == nil {
		t.Fatal("New succeeded with a 3-character ADMIN_PASSWORD")
	}
	if !errors.Is(err, authentication.ErrWeakPassword) {
		t.Fatalf("error %v does not wrap ErrWeakPassword", err)
	}
	for _, want := range []string{"ADMIN_PASSWORD", "6", "admin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}
