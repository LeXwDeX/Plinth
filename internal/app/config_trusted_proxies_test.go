package app

import (
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// #256: trusted proxies and the admin reset switch come from Product Schema
// settings; nothing is trusted and nothing is reset by default.
func TestTrustedProxiesAndAdminResetComeFromProductSchema(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	defaults, err := loadConfigFromSchema(schema, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if len(defaults.TrustedProxies) != 0 || defaults.AdminPasswordReset {
		t.Fatalf("defaults trust %v, reset %v", defaults.TrustedProxies, defaults.AdminPasswordReset)
	}
	configured, err := loadConfigFromSchema(schema, func(name string) string {
		switch name {
		case "TRUSTED_PROXY_CIDRS":
			return "172.30.0.0/16, 10.1.2.3"
		case "ADMIN_PASSWORD_RESET":
			return "true"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(configured.TrustedProxies); got != 2 ||
		configured.TrustedProxies[0].String() != "172.30.0.0/16" ||
		configured.TrustedProxies[1].String() != "10.1.2.3/32" {
		t.Fatalf("TrustedProxies = %v", configured.TrustedProxies)
	}
	if !configured.AdminPasswordReset {
		t.Fatal("ADMIN_PASSWORD_RESET=true was not applied")
	}
	_, err = loadConfigFromSchema(schema, func(name string) string {
		if name == "TRUSTED_PROXY_CIDRS" {
			return "not-a-network"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "trusted_proxy_cidrs") {
		t.Fatalf("invalid network error = %v", err)
	}
}
