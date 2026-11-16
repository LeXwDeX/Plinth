package httpapi

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/wirecapture"
)

type captureStatusStub wirecapture.RecorderStatus

func (s captureStatusStub) Status() wirecapture.RecorderStatus { return wirecapture.RecorderStatus(s) }

// #233: the administrator's diagnostics view shows whether the opt-in capture
// of upstream calls is on and what it holds. It is guarded by the Schema's
// permission, never cached, and carries counts and the configured directory,
// no value from any upstream call.
func TestDiagnosticsRouteShowsTheCaptureStateToAdministratorsOnly(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	if schema.Diagnostics == nil || schema.Diagnostics.Routes.Admin == "" {
		t.Fatal("the default Catalog declares no diagnostics route")
	}
	policy, err := access.NewPolicy(access.PolicyConfig{DefaultRole: "user", AdminRole: "admin", Roles: []access.RoleDefinition{
		{Key: "user"}, {Key: "admin", Permissions: []access.Permission{access.Permission(schema.Diagnostics.Permission)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	identity := DevelopmentIdentity{Policy: policy}
	server := &Server{identity: identity, application: schema, logger: slog.New(slog.NewTextHandler(io.Discard, nil)), mux: http.NewServeMux()}
	server.registerDiagnosticsContract()
	route := schema.Diagnostics.Routes.Admin
	get := func(claims access.Claims) *httptest.ResponseRecorder {
		server.identity = DevelopmentIdentity{DefaultClaims: claims, Policy: policy}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		return response
	}

	if response := get(access.Claims{}); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", response.Code)
	}
	if response := get(access.Claims{ID: "member", Roles: []string{"user"}}); response.Code != http.StatusForbidden {
		t.Fatalf("ordinary user: %d", response.Code)
	}
	administrator := access.Claims{ID: "admin-1", Roles: []string{"admin"}}

	// No recorder wired: the view says it is off.
	off := get(administrator)
	var view struct {
		UpstreamCapture wirecapture.RecorderStatus `json:"upstream_capture"`
	}
	if off.Code != http.StatusOK || off.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("off: %d %v", off.Code, off.Header())
	}
	if err := json.Unmarshal(off.Body.Bytes(), &view); err != nil || view.UpstreamCapture.Enabled {
		t.Fatalf("off view = %+v, %v", view, err)
	}

	server.upstreamCapture = captureStatusStub{Enabled: true, Directory: "/var/lib/plinth/upstream-capture", MaxRecords: 2000,
		MaxBytes: 64 << 20, Records: 12, Bytes: 34567, Written: 10, Skipped: 2, Rotated: 3}
	on := get(administrator)
	if on.Code != http.StatusOK {
		t.Fatalf("on: %d %s", on.Code, on.Body.String())
	}
	if err := json.Unmarshal(on.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if got := view.UpstreamCapture; !got.Enabled || got.Records != 12 || got.Written != 10 || got.Skipped != 2 || got.Rotated != 3 ||
		got.MaxRecords != 2000 || got.Directory != "/var/lib/plinth/upstream-capture" {
		t.Fatalf("on view = %+v", got)
	}
	// The field names are the contract the diagnostics export (#234) reads.
	for _, name := range []string{`"enabled"`, `"directory"`, `"max_records"`, `"max_bytes"`, `"records"`, `"bytes"`, `"written"`, `"skipped"`, `"rotated"`, `"write_errors"`} {
		if !strings.Contains(on.Body.String(), name) {
			t.Errorf("response lacks %s: %s", name, on.Body.String())
		}
	}
	// Only a GET is routed.
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, route, nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST: %d", response.Code)
	}
}

// A Catalog without the diagnostics block (an older external document) simply
// leaves the route unregistered.
func TestDiagnosticsRouteIsAbsentWithoutASchemaDeclaration(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := product.Application()
	route := schema.Diagnostics.Routes.Admin
	schema.Diagnostics = nil
	server := &Server{application: schema, mux: http.NewServeMux()}
	server.registerDiagnosticsContract()
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("an undeclared route answers %d", response.Code)
	}
}
