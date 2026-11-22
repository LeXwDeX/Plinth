package httpapi

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A handler that aborted its response already gave the connection up, usually
// after its status and headers were sent: the middleware must not answer it,
// because there is no response left to write. It re-panics so net/http closes
// the connection and the client sees the failure (#425).
func TestRecoverMiddlewareRePanicsAnAbortedResponse(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := recoverMiddleware(logger, "internal", http.HandlerFunc(
		func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) }))
	defer func() {
		recovered := recover()
		abort, isAbort := recovered.(error)
		if !isAbort || !errors.Is(abort, http.ErrAbortHandler) {
			t.Fatalf("recovered = %v; want the abort re-panicked", recovered)
		}
	}()
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	t.Fatal("middleware returned; want the abort re-panicked")
}
