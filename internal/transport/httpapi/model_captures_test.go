package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/capture"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

type captureRecorderFake struct {
	byID      map[string]capture.Record
	last      capture.Command
	recordErr error
}

func (f *captureRecorderFake) Record(_ context.Context, command capture.Command) (capture.Record, error) {
	f.last = command
	if f.recordErr != nil {
		return capture.Record{}, f.recordErr
	}
	record := capture.Record{Command: command}
	if f.byID == nil {
		f.byID = map[string]capture.Record{}
	}
	f.byID[record.ID] = record
	return record, nil
}

func (f *captureRecorderFake) Get(_ context.Context, owner, id string) (capture.Record, error) {
	record, ok := f.byID[id]
	if !ok || record.OwnerID != owner {
		return capture.Record{}, capture.ErrNotFound
	}
	return record, nil
}

func newCaptureHTTPTestHandler(t *testing.T, recorder capture.Recorder) http.Handler {
	t.Helper()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{{Key: "user"}, {Key: "admin"}}})
	if err != nil {
		t.Fatal(err)
	}
	application := product.Application()
	application.Runtime.HTTP.CSRFOriginEnforced = true
	server := &Server{
		identity:    DevelopmentIdentity{DefaultClaims: access.Claims{Roles: []string{"user"}}, Policy: policy},
		application: application,
		captures:    recorder,
		mux:         http.NewServeMux(),
	}
	server.mux.HandleFunc("POST /api/v1/model-captures", server.withIdentity(server.recordModelCapture))
	server.mux.HandleFunc("GET /api/v1/model-captures/{captureID}", server.withIdentity(server.getModelCapture))
	return server.securityHeadersMiddleware(server.sameOriginCSRFMiddleware(server.mux))
}

func serveCaptureRequest(handler http.Handler, method, path, body, owner, origin string, cookie bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if owner != "" {
		request.Header.Set("X-User-ID", owner)
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	if cookie {
		request.AddCookie(&http.Cookie{Name: "session", Value: "opaque"})
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestModelCaptureHTTPRequiresIdentityAndSameOrigin(t *testing.T) {
	t.Parallel()
	recorder := &captureRecorderFake{}
	handler := newCaptureHTTPTestHandler(t, recorder)
	body := `{"session_id":"session-a","source_task_id":"task-a","model_asset_id":"model-a","screenshot_asset_id":"image-a","idempotency_key":"key-a","view":{"camera_orbit":"45deg 55deg 4m","camera_target":"0m 0m 0m","field_of_view":"45deg","environment":"neutral","renderer_version":"model-viewer:test"}}`
	if response := serveCaptureRequest(handler, http.MethodPost, "/api/v1/model-captures", body, "", "http://example.com", false); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serveCaptureRequest(handler, http.MethodPost, "/api/v1/model-captures", body, "owner-a", "", true); response.Code != http.StatusForbidden {
		t.Fatalf("cookie POST without Origin status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serveCaptureRequest(handler, http.MethodPost, "/api/v1/model-captures", body, "owner-a", "https://attacker.invalid", true); response.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST status=%d body=%s", response.Code, response.Body.String())
	}
	if recorder.last.ID != "" {
		t.Fatalf("rejected request reached recorder: %+v", recorder.last)
	}
}

func TestModelCaptureHTTPBindsOwnerAndHidesOtherOwners(t *testing.T) {
	t.Parallel()
	recorder := &captureRecorderFake{}
	handler := newCaptureHTTPTestHandler(t, recorder)
	body := `{"id":"attacker-chosen","session_id":"session-a","source_task_id":"task-a","model_asset_id":"model-a","screenshot_asset_id":"image-a","idempotency_key":"key-a","view":{"camera_orbit":"45deg 55deg 4m","camera_target":"0m 0m 0m","field_of_view":"45deg","environment":"neutral","renderer_version":"model-viewer:test"}}`
	response := serveCaptureRequest(handler, http.MethodPost, "/api/v1/model-captures", body, "owner-a", "http://example.com", true)
	if response.Code != http.StatusCreated {
		t.Fatalf("owned POST status=%d body=%s", response.Code, response.Body.String())
	}
	if recorder.last.OwnerID != "owner-a" || recorder.last.ID == "attacker-chosen" {
		t.Fatalf("server did not bind owner/id: %+v", recorder.last)
	}
	var record capture.Record
	if err := json.Unmarshal(response.Body.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if record.OwnerID != "" || record.ID != recorder.last.ID {
		t.Fatalf("response exposed internal owner or differs from persisted record: %+v", record)
	}
	if response := serveCaptureRequest(handler, http.MethodGet, "/api/v1/model-captures/"+record.ID, "", "owner-b", "", false); response.Code != http.StatusNotFound {
		t.Fatalf("cross-owner GET status=%d body=%s", response.Code, response.Body.String())
	}
	if response := serveCaptureRequest(handler, http.MethodGet, "/api/v1/model-captures/"+record.ID, "", "owner-a", "", false); response.Code != http.StatusOK {
		t.Fatalf("owned GET status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestModelCaptureHTTPMapsRepositoryRejections(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{name: "bad image", err: capture.ErrInvalid, want: http.StatusBadRequest},
		{name: "mismatched image", err: capture.ErrMismatch, want: http.StatusConflict},
		{name: "unready or foreign source", err: capture.ErrNotFound, want: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := &captureRecorderFake{recordErr: test.err}
			handler := newCaptureHTTPTestHandler(t, recorder)
			body := `{"session_id":"session-a","source_task_id":"task-a","model_asset_id":"model-a","screenshot_asset_id":"image-a","idempotency_key":"key-a","view":{"camera_orbit":"45deg 55deg 4m","camera_target":"0m 0m 0m","field_of_view":"45deg","environment":"neutral","renderer_version":"model-viewer:test"}}`
			response := serveCaptureRequest(handler, http.MethodPost, "/api/v1/model-captures", body, "owner-a", "http://example.com", true)
			if response.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.want, response.Body.String())
			}
		})
	}
}
