package httpapi

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/routing"
)

func runRequestLogChain(
	t *testing.T,
	handler http.Handler,
) (string, *httptest.ResponseRecorder) {
	t.Helper()
	var buffer bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buffer, nil))
	route, err := routing.ParseOne("/provider-assets/{token}", "token")
	if err != nil {
		t.Fatalf("parse provider asset route: %v", err)
	}
	chain := requestLogMiddleware(
		logger,
		route,
		nil,
		recoverMiddleware(logger, "internal error", handler),
	)
	response := httptest.NewRecorder()
	chain.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/logs/probe", nil))
	return buffer.String(), response
}

func requestLogLine(t *testing.T, output string) string {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if strings.Contains(line, `msg="HTTP request"`) {
			return line
		}
	}
	t.Fatalf("request log line not found in output:\n%s", output)
	return ""
}

func assertRequestLogSummary(t *testing.T, line string) {
	t.Helper()
	for _, field := range []string{
		"method=GET",
		"path=/logs/probe",
		"duration_ms=",
	} {
		if !strings.Contains(line, field) {
			t.Errorf("request log line missing %s: %s", field, line)
		}
	}
}

func TestRequestLogMiddlewareStatus(t *testing.T) {
	t.Parallel()
	t.Run("implicit 200", func(t *testing.T) {
		output, response := runRequestLogChain(t,
			http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				_, _ = writer.Write([]byte("ok"))
			}),
		)
		line := requestLogLine(t, output)
		assertRequestLogSummary(t, line)
		if !strings.Contains(line, "status=200") {
			t.Errorf("request log line missing status=200: %s", line)
		}
		if response.Code != http.StatusOK {
			t.Errorf("response code = %d, want %d", response.Code, http.StatusOK)
		}
	})

	t.Run("explicit 500", func(t *testing.T) {
		output, response := runRequestLogChain(t,
			http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(http.StatusInternalServerError)
			}),
		)
		line := requestLogLine(t, output)
		assertRequestLogSummary(t, line)
		if !strings.Contains(line, "status=500") {
			t.Errorf("request log line missing status=500: %s", line)
		}
		if response.Code != http.StatusInternalServerError {
			t.Errorf("response code = %d, want %d",
				response.Code, http.StatusInternalServerError)
		}
	})

	t.Run("panic recovered to 500", func(t *testing.T) {
		output, response := runRequestLogChain(t,
			http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				panic("boom")
			}),
		)
		line := requestLogLine(t, output)
		assertRequestLogSummary(t, line)
		if !strings.Contains(line, "status=500") {
			t.Errorf("request log line missing status=500: %s", line)
		}
		if response.Code != http.StatusInternalServerError {
			t.Errorf("response code = %d, want %d",
				response.Code, http.StatusInternalServerError)
		}
	})

	t.Run("flush passthrough keeps stream endpoints working", func(t *testing.T) {
		output, response := runRequestLogChain(t,
			http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				flusher, ok := writer.(http.Flusher)
				if !ok {
					t.Error("response writer lost http.Flusher through statusRecorder")
					return
				}
				_, _ = writer.Write([]byte("data: chunk"))
				flusher.Flush()
			}),
		)
		line := requestLogLine(t, output)
		assertRequestLogSummary(t, line)
		if !strings.Contains(line, "status=200") {
			t.Errorf("request log line missing status=200: %s", line)
		}
		if response.Code != http.StatusOK || response.Body.String() != "data: chunk" {
			t.Errorf("response = %d %q", response.Code, response.Body.String())
		}
	})
}
