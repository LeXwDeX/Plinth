package httpapi

import (
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/routing"
)

func TestSafeLogPathRedactsProviderAssetCapability(t *testing.T) {
	t.Parallel()
	const providerAssetRoute = "/configured/provider-assets/{token}/content"
	route, err := routing.ParseOne(providerAssetRoute, "token")
	if err != nil {
		t.Fatal(err)
	}
	got := safeLogPath(
		"/configured/provider-assets/secret.token/content",
		[]routing.One{route},
	)
	if got != "/configured/provider-assets/{redacted}/content" {
		t.Fatalf("safeLogPath = %q", got)
	}
	if got := safeLogPath(
		"/api/v1/tasks/task-1",
		[]routing.One{route},
	); got != "/api/v1/tasks/task-1" {
		t.Fatalf("ordinary path changed to %q", got)
	}
}

func TestSafeLogPathRedactsWebhookPathSecret(t *testing.T) {
	t.Parallel()
	webhookRoute, err := routing.ParseOne("/{webhookSecret}/wbh", "webhookSecret")
	if err != nil {
		t.Fatal(err)
	}
	ordinaryRoute, err := routing.ParseOne("/api/v1/tasks/{taskID}", "taskID")
	if err != nil {
		t.Fatal(err)
	}
	got := safeLogPath(
		"/configured-webhook-secret/wbh",
		[]routing.One{ordinaryRoute, webhookRoute},
	)
	if got != "/{redacted}/wbh" {
		t.Fatalf("safeLogPath = %q", got)
	}
	if got := safeLogPath(
		"/configured-webhook-secret/wbh/",
		[]routing.One{ordinaryRoute, webhookRoute},
	); got != "/configured-webhook-secret/wbh/" {
		t.Fatalf("non-matching path changed to %q", got)
	}
}

func TestSafeLogPathRedactsSignedAssetToken(t *testing.T) {
	t.Parallel()
	signedRoute, err := routing.ParseOne("/mcp/signed-assets/{token}", "token")
	if err != nil {
		t.Fatal(err)
	}
	got := safeLogPath("/mcp/signed-assets/header.signature", []routing.One{signedRoute})
	if got != "/mcp/signed-assets/{redacted}" {
		t.Fatalf("safeLogPath = %q", got)
	}
	if got := safeLogPath("/mcp/tasks/task-1/assets/asset-1", []routing.One{signedRoute}); got != "/mcp/tasks/task-1/assets/asset-1" {
		t.Fatalf("ordinary path changed to %q", got)
	}
}

// TestMCPGetTaskAssetsSignedWithoutASigner covers the deployment without an
// asset_signing_key: signed:true is a business failure, and the check happens
// before any Task is read, so a bare Server is enough to run it.
func TestMCPGetTaskAssetsSignedWithoutASigner(t *testing.T) {
	t.Parallel()
	product, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	server := &Server{application: product.Application()}
	outcome, err := server.mcpGetTaskAssets(&mcpCall{
		locale: "en-US", owner: "owner-a",
		arguments: map[string]any{"task_id": "task-1", "signed": true},
	}, routing.Two{}, routing.One{}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.problem == nil || outcome.problem.Code != "signed_download_unavailable" || outcome.problem.Message == "" {
		t.Fatalf("outcome = %+v", outcome)
	}
}
