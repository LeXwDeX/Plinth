package catalog

import (
	"github.com/LeXwDeX/Plinth/internal/schema"
	"testing"
)

func TestLocalModelConnectionHasNoNetworkSettings(t *testing.T) {
	for _, name := range []string{"valid", "base", "key", "auth", "timeout", "http", "conditional", "error mapping", "status mapping", "capability"} {
		t.Run(name, func(t *testing.T) {
			c := ProviderConnectionSchema{ID: "local", Protocol: "local-model", AdapterKey: "local.model", Capabilities: []schema.Capability{schema.FormatConversion}}
			switch name {
			case "base":
				c.BaseURLSetting = "base"
			case "key":
				c.APIKeySetting = "key"
			case "auth":
				c.AuthHeader = "Authorization"
			case "timeout":
				c.RequestTimeoutSeconds = 1
			case "http":
				c.AllowHTTPAssets = true
			case "conditional":
				c.ConditionalCapabilities = []ProviderConditionalCapabilitySchema{{Capabilities: []schema.Capability{schema.FormatConversion}, AllOfSettings: []string{"key"}}}
			case "error mapping":
				c.ErrorMapping = &schema.ErrorMapping{}
			case "status mapping":
				c.StatusMapping = &schema.StatusMapping{}
			case "capability":
				c.Capabilities = []schema.Capability{schema.TextTo3D}
			}
			err := validateProviderConnections(RuntimeSchema{ProviderConnections: []ProviderConnectionSchema{c}})
			if (err == nil) != (name == "valid") {
				t.Fatalf("unexpected local connection result: %v", err)
			}
		})
	}
}
