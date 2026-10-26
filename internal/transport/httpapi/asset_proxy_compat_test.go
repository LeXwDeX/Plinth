package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/access"
	"github.com/LeXwDeX/Plinth/internal/administration"
	"github.com/LeXwDeX/Plinth/internal/assetdelivery"
	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestLegacyAssetProxyCannotFetchOrRevealProviderURL(t *testing.T) {
	t.Parallel()
	application := assetProxyTestApplication(t)
	schema := application.Runtime.AssetDelivery.Proxy
	targetURL := "https://cdn.meshy.ai/models/a.png?Signature=secret"
	proxy := &assetProxyStub{
		result: assetdelivery.Result{
			Body: io.NopCloser(strings.NewReader("upstream content")),
		},
	}
	audits := &assetProxyAuditRepository{}
	server := newAssetProxyTransportServer(
		t, application, proxy, audits,
		assetProxyIdentity{principal: access.Principal{Claims: access.Claims{ID: "user-1"}}},
	)
	query := url.Values{schema.API.Query.URL: []string{targetURL}}
	request := httptest.NewRequest(http.MethodGet,
		application.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset+"?"+query.Encode(), nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != schema.API.Statuses.NotFound {
		t.Fatalf("status = %d body = %q", response.Code, response.Body.String())
	}
	if proxy.calls != 0 || len(audits.events) != 0 {
		t.Fatalf("proxy calls = %d audits = %#v", proxy.calls, audits.events)
	}
	if strings.Contains(response.Body.String(), targetURL) ||
		strings.Contains(response.Body.String(), "cdn.meshy.ai") ||
		response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("unsafe response: headers=%#v body=%q", response.Header(), response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload[schema.API.Fields.Error] != schema.Messages.FetchFailed {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestLegacyAssetProxyRouteRequiresAuthentication(t *testing.T) {
	t.Parallel()
	application := assetProxyTestApplication(t)
	proxy := &assetProxyStub{}
	server := newAssetProxyTransportServer(
		t,
		application,
		proxy,
		&assetProxyAuditRepository{},
		assetProxyIdentity{err: errors.New("no identity")},
	)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(
		response,
		httptest.NewRequest(
			http.MethodGet,
			application.Runtime.HTTP.LegacyRoutes.MeshyProxyAsset,
			nil,
		),
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", response.Code)
	}
	if proxy.calls != 0 {
		t.Fatalf("proxy calls = %d", proxy.calls)
	}
}

func assetProxyTestApplication(t *testing.T) catalog.ApplicationSchema {
	t.Helper()
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	return taskCatalog.Application()
}

func newAssetProxyTransportServer(
	t *testing.T,
	application catalog.ApplicationSchema,
	proxy AssetProxy,
	audits *assetProxyAuditRepository,
	identity IdentityResolver,
) *Server {
	t.Helper()
	auditService, err := administration.NewAuditService(
		administration.AuditServiceConfig{
			Repository:           audits,
			DefaultPage:          1,
			DefaultPageSize:      20,
			MaximumPageSize:      100,
			IDPrefix:             "audit_",
			WriteMaximumAttempts: 1,
			WriteRetryDelay:      0,
			Clock: func() time.Time {
				return time.Date(
					2026,
					time.July,
					27,
					12,
					0,
					0,
					0,
					time.UTC,
				)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{
		identity:            identity,
		assetProxy:          proxy,
		administrationAudit: auditService,
		application:         application,
		logger: slog.New(
			slog.NewTextHandler(io.Discard, nil),
		),
		mux: http.NewServeMux(),
	}
	server.routes()
	return server
}

type assetProxyStub struct {
	request assetdelivery.Request
	result  assetdelivery.Result
	err     error
	calls   int
}

func (proxy *assetProxyStub) Get(
	_ context.Context,
	request assetdelivery.Request,
) (assetdelivery.Result, error) {
	proxy.calls++
	proxy.request = request
	return proxy.result, proxy.err
}

type assetProxyIdentity struct {
	principal access.Principal
	err       error
}

func (identity assetProxyIdentity) Identify(
	*http.Request,
) (access.Principal, error) {
	return identity.principal, identity.err
}

type assetProxyAuditRepository struct {
	events []administration.AuditEvent
}

func (*assetProxyAuditRepository) QueryAdministrationAuditEvents(
	context.Context,
	administration.AuditQuery,
) ([]administration.AuditEvent, int, error) {
	return nil, 0, nil
}

func (repository *assetProxyAuditRepository) RecordAudit(
	_ context.Context,
	event administration.AuditEvent,
) error {
	repository.events = append(repository.events, event)
	return nil
}
