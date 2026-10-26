package catalog

import (
	"fmt"
	"regexp"
	"strings"
)

// LLMSettingsSchema declares the administrator's LLM settings page (#322
// Scope 2) and how the runtime environment declares chat model connections.
// Connection values (addresses, keys) are never part of the Schema or of any
// DTO: the runtime only reports whether each variable has a value.
type LLMSettingsSchema struct {
	Routes LLMSettingsRoutes `json:"routes"`
	// Permission guards every administration route; AuditAction records a
	// publish, restore or reset, TestAuditAction a connection test.
	Permission      string `json:"permission"`
	AuditAction     string `json:"audit_action"`
	TestAuditAction string `json:"test_audit_action"`
	// HistoryLimit bounds the versions the administration page lists.
	HistoryLimit int                  `json:"history_limit"`
	Connections  LLMConnectionsSchema `json:"connections"`
	Probe        LLMProbeSchema       `json:"probe"`
	UI           map[string]string    `json:"ui"`
}

type LLMSettingsRoutes struct {
	Admin   string `json:"admin"`
	Publish string `json:"publish"`
	Restore string `json:"restore"`
	Reset   string `json:"reset"`
	Test    string `json:"test"`
}

// LLMConnectionsSchema is the environment contract of chat connections. The
// default connection is DefaultID (its variables are the chat.agent runtime
// settings); ListSetting names the extra connection IDs, comma separated, and
// connection <ID> reads exactly <ID> plus each suffix. No connection reads
// another one's variables, and no name falls back to another.
type LLMConnectionsSchema struct {
	DefaultID           string                `json:"default_id"`
	ListSetting         string                `json:"list_setting"`
	IDPattern           string                `json:"id_pattern"`
	MaxConnections      int                   `json:"max_connections"`
	EnvironmentSuffixes LLMConnectionSuffixes `json:"environment_suffixes"`
}

type LLMConnectionSuffixes struct {
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key"`
	Model               string `json:"model"`
	ContextWindowTokens string `json:"context_window_tokens"`
}

// LLMProbeSchema is the administrator's connection test: one minimal chat
// request carrying a small generated picture (ImageSize pixels square), so a
// single click also tells whether the model reads images.
type LLMProbeSchema struct {
	TimeoutSeconds int    `json:"timeout_seconds"`
	Prompt         string `json:"prompt"`
	ImageSize      int    `json:"image_size"`
}

// PublicLLMSettingsSchema is what the browser gets: routes and copy.
type PublicLLMSettingsSchema struct {
	Routes LLMSettingsRoutes `json:"routes"`
	UI     map[string]string `json:"ui"`
}

func (schema *LLMSettingsSchema) Public() *PublicLLMSettingsSchema {
	if schema == nil {
		return nil
	}
	return &PublicLLMSettingsSchema{Routes: schema.Routes, UI: cloneStringMap(schema.UI)}
}

// ValidID reports whether id is a connection ID the environment may declare.
func (schema LLMConnectionsSchema) ValidID(id string) bool {
	pattern, err := regexp.Compile(schema.IDPattern)
	return err == nil && id != "" && pattern.MatchString(id)
}

// EnvironmentNames are the variables connection id reads, in the order base
// URL, API key, model, context window.
func (schema LLMConnectionsSchema) EnvironmentNames(id string) [4]string {
	suffixes := schema.EnvironmentSuffixes
	return [4]string{id + suffixes.BaseURL, id + suffixes.APIKey, id + suffixes.Model, id + suffixes.ContextWindowTokens}
}

func cloneLLMSettings(source *LLMSettingsSchema) *LLMSettingsSchema {
	if source == nil {
		return nil
	}
	result := *source
	result.UI = cloneStringMap(source.UI)
	return &result
}

func validateLLMSettings(application ApplicationSchema) error {
	schema := application.LLMSettings
	if schema == nil {
		return nil // Older external Catalog documents remain valid.
	}
	seen := map[string]bool{}
	for _, path := range []string{schema.Routes.Admin, schema.Routes.Publish, schema.Routes.Restore, schema.Routes.Reset, schema.Routes.Test} {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#\\{}") || seen[path] {
			return fmt.Errorf("llm_settings: invalid route %q", path)
		}
		seen[path] = true
	}
	if schema.Permission == "" || schema.AuditAction == "" || schema.TestAuditAction == "" || schema.HistoryLimit <= 0 {
		return fmt.Errorf("llm_settings: permission, audit actions and history_limit are required")
	}
	connections := schema.Connections
	if _, err := regexp.Compile(connections.IDPattern); err != nil || !strings.HasPrefix(connections.IDPattern, "^") || !strings.HasSuffix(connections.IDPattern, "$") {
		return fmt.Errorf("llm_settings.connections.id_pattern must be an anchored pattern")
	}
	if !connections.ValidID(connections.DefaultID) || connections.MaxConnections <= 0 {
		return fmt.Errorf("llm_settings.connections: invalid default_id or max_connections")
	}
	suffixes := connections.EnvironmentSuffixes
	for name, suffix := range map[string]string{"base_url": suffixes.BaseURL, "api_key": suffixes.APIKey, "model": suffixes.Model, "context_window_tokens": suffixes.ContextWindowTokens} {
		if suffix == "" || !connections.ValidID("A"+suffix) {
			return fmt.Errorf("llm_settings.connections.environment_suffixes.%s is invalid", name)
		}
	}
	list, found := application.Runtime.Configuration.Settings[connections.ListSetting]
	if !found || list.Secret || len(list.Environment) == 0 {
		return fmt.Errorf("llm_settings.connections.list_setting %q is not a declared runtime setting", connections.ListSetting)
	}
	// The default connection is the chat.agent settings themselves: its
	// derived names must be exactly their variables, so it reads nothing new.
	agent := application.Chat.Agent
	names := connections.EnvironmentNames(connections.DefaultID)
	for index, setting := range []string{agent.BaseURLSetting, agent.APIKeySetting, agent.ModelSetting, agent.ContextWindowSetting} {
		declared := application.Runtime.Configuration.Settings[setting]
		if len(declared.Environment) == 0 || declared.Environment[0].Name != names[index] {
			return fmt.Errorf("llm_settings.connections.default_id %q does not name the chat.agent setting %q", connections.DefaultID, setting)
		}
	}
	if schema.Probe.TimeoutSeconds <= 0 || strings.TrimSpace(schema.Probe.Prompt) == "" || schema.Probe.ImageSize < 8 || schema.Probe.ImageSize > 512 {
		return fmt.Errorf("llm_settings.probe requires a timeout, a prompt and an image size of 8-512 pixels")
	}
	return nil
}
