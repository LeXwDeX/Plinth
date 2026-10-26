package catalog

import (
	"strings"
	"testing"
)

func TestDefaultCatalogOwnsCompleteProviderWebhookContract(t *testing.T) {
	productCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := productCatalog.Application()
	if len(application.ProviderWebhooks) != 1 {
		t.Fatalf("provider webhooks = %#v", application.ProviderWebhooks)
	}
	webhook := application.ProviderWebhooks[0]
	if webhook.ID != "meshy-task-status" ||
		webhook.ProviderID != "meshy" ||
		webhook.Route != "/{webhookSecret}/wbh" ||
		webhook.PathParameter != "webhookSecret" ||
		webhook.PathSecretSetting != "meshy_webhook_path_secret" ||
		webhook.KeySetting != "meshy_webhook_key" ||
		webhook.EventType != "provider.webhook_received" ||
		webhook.API.TimestampLayout != "2006-01-02T15:04:05.000Z07:00" {
		t.Fatalf("provider webhook = %#v", webhook)
	}
	if strings.Join(webhook.KeyHeaders, ",") !=
		"x-webhook-key,x-webhook-secret,x-meshy-webhook-key" {
		t.Fatalf("key headers = %#v", webhook.KeyHeaders)
	}
	if strings.Join(webhook.PayloadJobIDFields, ",") != "id,task_id" {
		t.Fatalf("job ID fields = %#v", webhook.PayloadJobIDFields)
	}
	if webhook.API.Fields.Error != "error" ||
		webhook.API.Fields.Received != "received" ||
		webhook.API.Statuses.OK != 200 ||
		webhook.API.Statuses.NotFound != 404 ||
		webhook.API.Values.Service != "Plinth WebHook Endpoint" {
		t.Fatalf("provider webhook API = %#v", webhook.API)
	}
	for _, settingName := range []string{
		webhook.PathSecretSetting,
		webhook.KeySetting,
	} {
		setting, exists := application.Runtime.Configuration.Settings[settingName]
		if !exists || !setting.Secret {
			t.Fatalf("secret setting %q = %#v, exists=%v", settingName, setting, exists)
		}
	}
}

func TestProviderWebhookContractIsRequired(t *testing.T) {
	productCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := productCatalog.Application()
	application.ProviderWebhooks = nil
	if err := ValidateApplicationSchema(application); err == nil ||
		!strings.Contains(err.Error(), "provider_webhooks") {
		t.Fatalf("validation error = %v", err)
	}
}

func TestProviderWebhookContractsAreExtensibleAndUnambiguous(t *testing.T) {
	productCatalog, err := LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	application := productCatalog.Application()
	future := application.ProviderWebhooks[0]
	future.ID = "future-task-status"
	future.ProviderID = "future-provider"
	future.Route = "/future/{futureSecret}/callback"
	future.PathParameter = "futureSecret"
	future.EventType = "future.webhook_received"
	application.ProviderWebhooks = append(application.ProviderWebhooks, future)
	if err := ValidateApplicationSchema(application); err != nil {
		t.Fatalf("multiple provider webhook contracts rejected: %v", err)
	}

	t.Run("duplicate stable ID is rejected", func(t *testing.T) {
		invalid := cloneApplication(application)
		invalid.ProviderWebhooks[1].ID = invalid.ProviderWebhooks[0].ID
		if err := ValidateApplicationSchema(invalid); err == nil ||
			!strings.Contains(err.Error(), "ID") {
			t.Fatalf("validation error = %v", err)
		}
	})

	t.Run("equivalent route shape is rejected", func(t *testing.T) {
		invalid := cloneApplication(application)
		invalid.ProviderWebhooks[1].Route = "/{anotherSecret}/wbh"
		invalid.ProviderWebhooks[1].PathParameter = "anotherSecret"
		if err := ValidateApplicationSchema(invalid); err == nil ||
			!strings.Contains(err.Error(), "route") {
			t.Fatalf("validation error = %v", err)
		}
	})
}
