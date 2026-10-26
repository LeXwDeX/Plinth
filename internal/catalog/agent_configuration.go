package catalog

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// AgentConfigurationSchema declares the bounded administration contract. Connection
// URLs, credentials and implementation keys are intentionally absent from editable DTOs.
type AgentConfigurationSchema struct {
	Routes          AgentConfigurationRoutes            `json:"routes"`
	Limits          AgentConfigurationLimits            `json:"limits"`
	Permission      string                              `json:"permission"`
	AuditAction     string                              `json:"audit_action"`
	ModelLabel      string                              `json:"model_label"`
	ParameterSource string                              `json:"parameter_source"`
	MCPPresets      []AgentMCPPresetSchema              `json:"mcp_presets"`
	MCPPolicy       AgentMCPPolicySchema                `json:"mcp_policy"`
	MCPConnections  map[string]AgentMCPConnectionSchema `json:"mcp_connections"`
	UI              map[string]string                   `json:"ui"`
}
type AgentConfigurationRoutes struct {
	MCP       string `json:"mcp"`
	Admin     string `json:"admin"`
	Validate  string `json:"validate"`
	Publish   string `json:"publish"`
	Restore   string `json:"restore"`
	Reset     string `json:"reset"`
	Effective string `json:"effective"`
}
type AgentMCPConnectionSchema struct {
	EndpointSetting string   `json:"endpoint_setting"`
	APIKeySetting   string   `json:"api_key_setting"`
	AllowedOrigins  []string `json:"allowed_origins"`
}
type AgentConfigurationLimits struct {
	MaxPromptCharacters      int `json:"max_prompt_characters"`
	MaxSkillCount            int `json:"max_skill_count"`
	MaxSkillPromptCharacters int `json:"max_skill_prompt_characters"`
}
type AgentMCPPresetSchema struct {
	ID              string               `json:"id"`
	Name            string               `json:"name"`
	Description     string               `json:"description"`
	ToolNames       []string             `json:"tool_names"`
	Transport       string               `json:"transport"`
	ConnectionRef   string               `json:"connection_ref"`
	ProtocolVersion string               `json:"protocol_version"`
	Tools           []AgentMCPToolSchema `json:"tools"`
}
type AgentMCPToolSchema struct {
	Name        string                  `json:"name"`
	RemoteName  string                  `json:"remote_name"`
	Description string                  `json:"description"`
	Parameters  json.RawMessage         `json:"parameters"`
	Execution   ChatToolExecutionSchema `json:"execution"`
}
type AgentMCPPolicySchema struct {
	TimeoutMilliseconds int   `json:"timeout_milliseconds"`
	MaxResponseBytes    int64 `json:"max_response_bytes"`
	MaxTools            int   `json:"max_tools"`
}

type PublicAgentConfigurationSchema struct {
	Routes AgentConfigurationRoutes `json:"routes"`
	Limits AgentConfigurationLimits `json:"limits"`
	UI     map[string]string        `json:"ui"`
}

func (s AgentConfigurationSchema) Public() PublicAgentConfigurationSchema {
	return PublicAgentConfigurationSchema{Routes: s.Routes, Limits: s.Limits, UI: s.UI}
}

func validateAgentConfiguration(schema AgentConfigurationSchema) error {
	if schema.Routes.Admin == "" {
		return nil
	} // Older external Catalog documents remain valid.
	if schema.Limits.MaxPromptCharacters <= 0 || schema.Limits.MaxSkillCount <= 0 || schema.Limits.MaxSkillPromptCharacters <= 0 {
		return fmt.Errorf("agent configuration limits must be positive")
	}
	routes := []string{schema.Routes.Admin, schema.Routes.Validate, schema.Routes.Publish, schema.Routes.Restore, schema.Routes.Reset, schema.Routes.Effective, schema.Routes.MCP}
	seen := map[string]bool{}
	for _, path := range routes {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "?#\\") || seen[path] {
			return fmt.Errorf("invalid agent configuration route")
		}
		seen[path] = true
	}
	if schema.Permission == "" || schema.AuditAction == "" || schema.ModelLabel == "" || schema.ParameterSource == "" {
		return fmt.Errorf("agent configuration metadata missing")
	}
	if schema.MCPPolicy.TimeoutMilliseconds <= 0 || schema.MCPPolicy.MaxResponseBytes <= 0 || schema.MCPPolicy.MaxTools <= 0 {
		return fmt.Errorf("invalid MCP policy")
	}
	presets := map[string]bool{}
	names := map[string]bool{}
	count := 0
	for _, preset := range schema.MCPPresets {
		if preset.ID == "" || presets[preset.ID] || preset.ConnectionRef == "" || preset.ProtocolVersion == "" || (preset.Transport != "local" && preset.Transport != "streamable_http") {
			return fmt.Errorf("invalid MCP preset")
		}
		presets[preset.ID] = true
		for _, tool := range preset.Tools {
			if tool.Name == "" || names[tool.Name] || tool.RemoteName == "" || tool.Description == "" || !json.Valid(tool.Parameters) {
				return fmt.Errorf("invalid MCP tool")
			}
			names[tool.Name] = true
			count++
			execution := tool.Execution
			if execution.ImplementationKey != "mcp/read" || len(execution.Effects) != 1 || execution.Effects[0] != "READ" || execution.Scope != "session" || execution.Consent != "none" || execution.Idempotency != "none" || (execution.Cancellation != "context" && execution.Cancellation != "none") || len(execution.Capabilities) != 0 {
				return fmt.Errorf("MCP tools must be read-only session tools")
			}
			if err := validateToolAgentScope(tool.Name, execution); err != nil {
				return fmt.Errorf("MCP tool agent_scope: %w", err)
			}
			domains := map[string]bool{}
			for _, domain := range execution.Domains {
				if (domain != "image" && domain != "model-3d") || domains[domain] {
					return fmt.Errorf("invalid MCP tool domain")
				}
				domains[domain] = true
			}
			if len(domains) == 0 {
				return fmt.Errorf("MCP tool domain missing")
			}
		}
	}
	if count > schema.MCPPolicy.MaxTools {
		return fmt.Errorf("too many MCP tools")
	}
	return nil
}

func validateAgentConfigurationConnections(application ApplicationSchema) error {
	schema := application.AgentConfiguration
	names := map[string]bool{}
	for _, tool := range application.Chat.Tools {
		names[tool.Name] = true
	}
	for _, tool := range application.Chat.AgentTools {
		names[tool.Name] = true
	}
	for _, preset := range schema.MCPPresets {
		for _, tool := range preset.Tools {
			if names[tool.Name] {
				return fmt.Errorf("MCP tool name overlaps another implementation")
			}
			names[tool.Name] = true
		}
		if preset.Transport == "streamable_http" {
			if _, found := schema.MCPConnections[preset.ConnectionRef]; !found {
				return fmt.Errorf("MCP connection reference missing")
			}
		}
	}
	for id, connection := range schema.MCPConnections {
		if id == "" || len(connection.AllowedOrigins) == 0 {
			return fmt.Errorf("MCP connection requires exact allowed origins")
		}
		endpoint, found := application.Runtime.Configuration.Settings[connection.EndpointSetting]
		if !found || endpoint.Type != "string" || endpoint.Secret {
			return fmt.Errorf("invalid MCP endpoint setting")
		}
		if connection.APIKeySetting != "" {
			key, found := application.Runtime.Configuration.Settings[connection.APIKeySetting]
			if !found || !key.Secret {
				return fmt.Errorf("MCP credentials must use a secret runtime setting")
			}
		}
		for _, origin := range connection.AllowedOrigins {
			parsed, err := url.Parse(origin)
			if err != nil || parsed.Host == "" || parsed.User != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
				return fmt.Errorf("MCP origin must be exact HTTP origin")
			}
		}
	}
	return nil
}
