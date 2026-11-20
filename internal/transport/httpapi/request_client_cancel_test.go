package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/generation"
	"github.com/LeXwDeX/Plinth/internal/logging"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

// #385: a page refresh cancels every request in flight. The failure that follows
// (a read that ends with "context canceled") is the client leaving, not a server
// error: it must not be logged as a 5xx or at ERROR level. Every real failure
// keeps its ERROR/WARN line with the request ID (#233).

type cancelLogs struct {
	entries []map[string]any
}

func (logs cancelLogs) find(msg string) map[string]any {
	for _, entry := range logs.entries {
		if entry["msg"] == msg {
			return entry
		}
	}
	return nil
}

func (logs cancelLogs) levelsAtLeastError() []string {
	var found []string
	for _, entry := range logs.entries {
		if entry["level"] == "ERROR" {
			found = append(found, fmt.Sprint(entry["msg"]))
		}
	}
	return found
}

// serveDomainFailure serves one request through the production chain whose handler
// fails with a 503 domain error caused by `cause`. The request's context is the
// one given.
func serveDomainFailure(t *testing.T, ctx context.Context, cause error) (cancelLogs, *httptest.ResponseRecorder) {
	t.Helper()
	var buffer bytes.Buffer
	logger := logging.New(&buffer, slog.LevelDebug)
	var server *Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/things/{sessionID}", func(writer http.ResponseWriter, _ *http.Request) {
		server.writeDomainError(writer, &generation.Error{
			Code: generation.ErrUnavailable, Operation: "open_asset", Detail: "资产暂不可读取", Cause: cause,
		})
	})
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server = &Server{application: product.Application(), logger: logger, mux: mux, providerAssetRoute: route}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/things/session_one", nil).WithContext(ctx)
	server.Handler().ServeHTTP(response, request)
	var logs cancelLogs
	for _, raw := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		var entry map[string]any
		if json.Unmarshal([]byte(raw), &entry) == nil {
			logs.entries = append(logs.entries, entry)
		}
	}
	return logs, response
}

func TestClientCancelIsNotLoggedAsServerError(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	logs, response := serveDomainFailure(t, ctx, fmt.Errorf("资产暂不可读取: %w", context.Canceled))

	requestID := response.Header().Get("X-Request-ID")
	if !strings.HasPrefix(requestID, "req_") {
		t.Fatalf("request id %q", requestID)
	}
	access := logs.find("HTTP request")
	if access == nil {
		t.Fatalf("no access log: %v", logs.entries)
	}
	if access["status"] != float64(499) || access["level"] != "INFO" || access["client_canceled"] != true {
		t.Fatalf("access log = %v", access)
	}
	if access["request_id"] != requestID || access["route"] != "GET /api/v1/things/{sessionID}" {
		t.Fatalf("access log lost the request ID or route: %v", access)
	}
	if !strings.Contains(fmt.Sprint(access["cause"]), "context canceled") {
		t.Fatalf("the cancellation cause is no longer logged: %v", access)
	}
	if errors := logs.levelsAtLeastError(); len(errors) != 0 {
		t.Fatalf("a client cancel produced ERROR lines: %v", errors)
	}
	if logs.find("domain operation failed") != nil {
		t.Fatalf("the canceled read was logged as a failed domain operation: %v", logs.entries)
	}
	if debug := logs.find("domain operation canceled by client"); debug == nil || debug["level"] != "DEBUG" {
		t.Fatalf("canceled domain operation line = %v", debug)
	}
	// The answer is the ordinary one: nobody reads it, and nothing about the
	// cancellation leaks into it.
	if response.Code != http.StatusServiceUnavailable || strings.Contains(response.Body.String(), "canceled") {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
}

func TestRealServerFailureStillLoggedAsError(t *testing.T) {
	t.Parallel()
	logs, response := serveDomainFailure(t, context.Background(), fmt.Errorf("资产暂不可读取: %w", fmt.Errorf("disk read failed")))

	requestID := response.Header().Get("X-Request-ID")
	access := logs.find("HTTP request")
	if access == nil || access["status"] != float64(503) || access["level"] != "ERROR" || access["request_id"] != requestID {
		t.Fatalf("access log = %v", access)
	}
	if _, marked := access["client_canceled"]; marked {
		t.Fatalf("a real failure was marked as a client cancel: %v", access)
	}
	if failed := logs.find("domain operation failed"); failed == nil || failed["level"] != "ERROR" {
		t.Fatalf("domain failure line = %v", failed)
	}
}

// A deadline the server set itself ends with DeadlineExceeded: that is a slow
// dependency, which stays an error.
func TestServerDeadlineIsNotAClientCancel(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-ctx.Done()
	logs, _ := serveDomainFailure(t, ctx, context.DeadlineExceeded)

	access := logs.find("HTTP request")
	if access == nil || access["status"] != float64(503) || access["level"] != "ERROR" {
		t.Fatalf("access log = %v", access)
	}
	if logs.find("domain operation failed") == nil {
		t.Fatalf("deadline failure was not logged as a failed domain operation: %v", logs.entries)
	}
}

// A request answered in full before the client left is an ordinary success.
func TestCanceledAfterSuccessKeepsItsStatus(t *testing.T) {
	t.Parallel()
	var buffer bytes.Buffer
	logger := logging.New(&buffer, slog.LevelDebug)
	ctx, cancel := context.WithCancel(context.Background())
	chain := requestLogMiddleware(logger, nil, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte("ok"))
		cancel()
	}))
	chain.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctx))
	var entry map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buffer.Bytes()), &entry); err != nil {
		t.Fatalf("log = %s: %v", buffer.String(), err)
	}
	if entry["status"] != float64(200) || entry["level"] != "INFO" {
		t.Fatalf("access log = %v", entry)
	}
	if _, marked := entry["client_canceled"]; marked {
		t.Fatalf("a success was marked as a client cancel: %v", entry)
	}
}
