package generation

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

type hostileStorageDiagnostic struct{}

func (hostileStorageDiagnostic) Error() string { panic("raw error must never be formatted") }
func (hostileStorageDiagnostic) StorageDiagnostic() AssetStorageDiagnostic {
	return AssetStorageDiagnostic{Stage: "https://PRIVATE.example/path", Reason: "PRIVATE_BODY", Errno: "PRIVATE_KEY"}
}

func TestInlineDiagnosticNeverFormatsRawErrorAndAllowlistsAdapterFields(t *testing.T) {
	var output bytes.Buffer
	module := &Module{logger: slog.New(slog.NewJSONHandler(&output, nil))}
	module.logInlineStorageFailure(Task{ID: "public-task"}, &Attempt{ID: "attempt"}, "store", "store_failed", 32, 64, "image/png", hostileStorageDiagnostic{})
	log := output.String()
	if strings.Contains(log, "PRIVATE") || strings.Contains(log, "https://") {
		t.Fatal("untrusted diagnostic escaped allowlist")
	}
	for _, field := range []string{`"storage_stage":"unknown"`, `"storage_reason":"unknown"`, `"errno":"unknown"`} {
		if !strings.Contains(log, field) {
			t.Fatal("adapter field was not normalized")
		}
	}
}
