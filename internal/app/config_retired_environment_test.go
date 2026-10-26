package app

import (
	"reflect"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestRetiredEnvironmentIsIgnoredAndReportsOnlyConfiguredNames(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	schema := taskCatalog.Application()
	baseline, err := loadConfigFromSchema(schema, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		"SSO_APP_ID":               "unused-app",
		"SSO_APP_SECRET":           "never-include-this-value-in-a-warning",
		"SSO_ADMIN_ROLE_KEYS":      "root",
		"SSO_GLOBAL_COOKIE_DOMAIN": "   ",
	}
	configured, err := loadConfigFromSchema(schema, func(name string) string { return values[name] })
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"SSO_APP_ID", "SSO_APP_SECRET", "SSO_ADMIN_ROLE_KEYS"}
	if !reflect.DeepEqual(configured.RetiredEnvironment, want) {
		t.Fatalf("retired variable names = %q, want %q", configured.RetiredEnvironment, want)
	}
	configured.RetiredEnvironment = nil
	if !reflect.DeepEqual(configured, baseline) {
		t.Fatal("retired variables changed the runtime configuration")
	}
}
