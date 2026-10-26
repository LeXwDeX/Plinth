package httpapi

import (
	"testing"

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
		route,
		nil,
	)
	if got != "/configured/provider-assets/{redacted}/content" {
		t.Fatalf("safeLogPath = %q", got)
	}
	if got := safeLogPath(
		"/api/v1/tasks/task-1",
		route,
		nil,
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
		ordinaryRoute,
		[]routing.One{webhookRoute},
	)
	if got != "/{redacted}/wbh" {
		t.Fatalf("safeLogPath = %q", got)
	}
	if got := safeLogPath(
		"/configured-webhook-secret/wbh/",
		ordinaryRoute,
		[]routing.One{webhookRoute},
	); got != "/configured-webhook-secret/wbh/" {
		t.Fatalf("non-matching path changed to %q", got)
	}
}
