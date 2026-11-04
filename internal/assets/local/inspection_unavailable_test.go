package local

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

func diagnosticOfErr(t *testing.T, err error) generation.AssetStorageDiagnostic {
	t.Helper()
	diagnostic, ok := generation.AssetStorageDiagnosticOf(err)
	if !ok {
		t.Fatalf("error carries no storage diagnostic: %v", err)
	}
	return diagnostic
}

// A wait for the shared Draco execution slot that times out never inspected
// the bytes, so it must not be recorded as a content verdict.
func TestDracoSlotTimeoutIsUnavailableNotContentVerdict(t *testing.T) {
	dracoEngine.once.Do(initializeDraco)
	if dracoEngine.err != nil {
		t.Fatal(dracoEngine.err)
	}
	dracoEngine.slot <- struct{}{}
	defer func() { <-dracoEngine.slot }()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	inspectErr := validateDracoContext(ctx, dracoFixtureBytes(t), dracoRange{})
	if inspectErr == nil {
		t.Fatal("slot contention timeout reported success")
	}
	diagnostic := diagnosticOfErr(t, diagnoseWrite("inspect", "content_rejected", labelInspectionFailure("glb", inspectErr)))
	if diagnostic.Stage != "inspect_unavailable" || diagnostic.Reason != "inspection_busy" {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
	failure, _ := generation.AssetStoreRejection(diagnoseWrite("inspect", "content_rejected", inspectErr), "model", "glb")
	if failure == nil || !failure.Retryable {
		t.Fatalf("slot timeout must stay retryable: %+v", failure)
	}
}

func putWithInspector(t *testing.T, inspectErr error) error {
	t.Helper()
	store, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.putBlob(context.Background(), strings.NewReader("payload"), "text/plain", 1<<20,
		func(*os.File, string) error { return inspectErr })
	if err == nil {
		t.Fatal("inspector error was swallowed")
	}
	return err
}

func TestCanceledInspectionIsRetryable(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, syscall.EIO} {
		failure, ok := generation.AssetStoreRejection(putWithInspector(t, cause), "model", "glb")
		if !ok || !failure.Retryable {
			t.Fatalf("%v: ok=%v failure=%+v", cause, ok, failure)
		}
	}
}

func TestContentRejectionStaysTerminal(t *testing.T) {
	err := putWithInspector(t, rejectInspection("external_address", errors.New("内容含外部地址")))
	failure, ok := generation.AssetStoreRejection(err, "model", "glb")
	if !ok || failure.Retryable {
		t.Fatalf("content verdict must be terminal: ok=%v failure=%+v", ok, failure)
	}
	if diagnostic := diagnosticOfErr(t, err); diagnostic.Stage != "inspect" || diagnostic.Reason != "external_address" {
		t.Fatalf("diagnostic=%+v", diagnostic)
	}
}
