package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

func TestBearerConfigurationErrorDoesNotExposeCredentials(t *testing.T) {
	taskCatalog, err := catalog.LoadDefault()
	if err != nil {
		t.Fatal(err)
	}
	const credential = "audit-only-bearer-value-never-log"
	for _, claims := range []any{123, map[string]any{"id": []string{"invalid"}}} {
		raw, err := json.Marshal(map[string]any{credential: claims})
		if err != nil {
			t.Fatal(err)
		}
		_, err = loadConfigFromSchema(taskCatalog.Application(), func(name string) string {
			if name == "AUTH_TOKENS_JSON" {
				return string(raw)
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "auth_tokens_json") {
			t.Fatal("invalid bearer configuration must identify the failing setting")
		}
		var log bytes.Buffer
		slog.New(slog.NewJSONHandler(&log, nil)).Error("configuration invalid", "error", err)
		if strings.Contains(err.Error(), credential) || strings.Contains(log.String(), credential) {
			t.Fatal("bearer credential was exposed by a configuration error or startup log")
		}
	}
}

func TestSecretRuntimeSettingErrorDoesNotExposeValue(t *testing.T) {
	const credential = "audit-only-secret-value-never-log"
	setting := catalog.RuntimeSettingSchema{Secret: true, AllowedValues: []string{"accepted"}}
	_, err := validateRuntimeSettingValue("secret_setting", setting, credential)
	if err == nil || !strings.Contains(err.Error(), "secret_setting") {
		t.Fatal("invalid secret setting must retain its setting name")
	}
	if strings.Contains(err.Error(), credential) {
		t.Fatal("secret setting value was exposed by a configuration error")
	}
	setting.Secret = false
	_, err = validateRuntimeSettingValue("public_setting", setting, "invalid-public-value")
	if err == nil || !strings.Contains(err.Error(), "invalid-public-value") {
		t.Fatal("non-secret diagnostics must retain the invalid value")
	}
}
