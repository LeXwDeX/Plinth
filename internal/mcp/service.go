// Package mcp owns the bounded, read-only MCP tool contract. Protocol transport
// and workspace data access are injected ports; this package cannot write Tasks.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var (
	ErrUnavailable      = errors.New("mcp: unavailable")
	ErrInvalidArguments = errors.New("mcp: invalid arguments")
	ErrToolDenied       = errors.New("mcp: tool not allowed")
	ErrProtocol         = errors.New("mcp: invalid protocol response")
)

type Scope struct{ OwnerID, SessionID string }
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type Peer interface {
	ListTools(context.Context) ([]Tool, error)
	CallTool(context.Context, string, json.RawMessage) (json.RawMessage, error)
}
type PeerFactory func(context.Context, catalog.AgentMCPPresetSchema, Scope) (Peer, error)
type PresetAvailability func(catalog.AgentMCPPresetSchema) bool

type scopedPeer struct {
	service      *Service
	scope        Scope
	declarations []catalog.ChatAgentToolSchema
}

func (p *scopedPeer) ListTools(context.Context) ([]Tool, error) {
	result := make([]Tool, 0, len(p.declarations))
	for _, tool := range p.declarations {
		result = append(result, Tool{Name: tool.Name, Description: tool.Description, InputSchema: append(json.RawMessage(nil), tool.Parameters...)})
	}
	return result, nil
}
func (p *scopedPeer) CallTool(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	for _, tool := range p.declarations {
		if tool.Name == name {
			return p.service.Call(ctx, p.scope, name, arguments)
		}
	}
	return nil, ErrToolDenied
}

// ScopedPeer exposes only the currently enabled set to authenticated MCP clients.
func (s *Service) ScopedPeer(enabled []string, scope Scope) (Peer, error) {
	declarations, err := s.Declarations(enabled)
	if err != nil {
		return nil, err
	}
	return &scopedPeer{service: s, scope: scope, declarations: declarations}, nil
}

type Service struct {
	presets   map[string]catalog.AgentMCPPresetSchema
	byName    map[string]string
	policy    catalog.AgentMCPPolicySchema
	peer      PeerFactory
	available PresetAvailability
}

// New receives validated Product Schema declarations. Tool descriptions and
// schemas from a remote peer never become system instructions or registrations.
func New(presets []catalog.AgentMCPPresetSchema, policy catalog.AgentMCPPolicySchema, peer PeerFactory, availability ...PresetAvailability) (*Service, error) {
	if policy.TimeoutMilliseconds <= 0 || policy.MaxResponseBytes <= 0 || policy.MaxTools <= 0 || peer == nil {
		return nil, ErrUnavailable
	}
	s := &Service{presets: map[string]catalog.AgentMCPPresetSchema{}, byName: map[string]string{}, policy: policy, peer: peer}
	if len(availability) > 1 || len(availability) == 1 && availability[0] == nil {
		return nil, ErrUnavailable
	}
	s.available = func(catalog.AgentMCPPresetSchema) bool { return true }
	if len(availability) == 1 {
		s.available = availability[0]
	}
	for _, preset := range presets {
		if preset.ID == "" || len(preset.Tools) == 0 || len(preset.Tools) > policy.MaxTools {
			return nil, ErrToolDenied
		}
		if _, found := s.presets[preset.ID]; found {
			return nil, ErrToolDenied
		}
		for _, tool := range preset.Tools {
			if tool.Name == "" || tool.RemoteName == "" || tool.Execution.ImplementationKey != "mcp/read" || !slices.Equal(tool.Execution.Effects, []string{"READ"}) || tool.Execution.Consent != "none" || tool.Execution.Scope != "session" {
				return nil, ErrToolDenied
			}
			if _, found := s.byName[tool.Name]; found {
				return nil, ErrToolDenied
			}
			if err := validateSchema(tool.Parameters); err != nil {
				return nil, err
			}
			s.byName[tool.Name] = preset.ID
		}
		// Copy declarations so callers cannot mutate the tool allowlist.
		var copied catalog.AgentMCPPresetSchema
		data, err := json.Marshal(preset)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &copied); err != nil {
			return nil, err
		}
		s.presets[preset.ID] = copied
	}
	return s, nil
}

func (s *Service) Declarations(enabled []string) ([]catalog.ChatAgentToolSchema, error) {
	result := []catalog.ChatAgentToolSchema{}
	seen := map[string]bool{}
	for _, id := range enabled {
		preset, found := s.presets[id]
		if !found || seen[id] {
			return nil, ErrToolDenied
		}
		seen[id] = true
		if !s.PresetAvailable(id) {
			continue
		}
		for _, tool := range preset.Tools {
			result = append(result, catalog.ChatAgentToolSchema{Name: tool.Name, Description: tool.Description, Parameters: append(json.RawMessage(nil), tool.Parameters...), Execution: tool.Execution})
		}
	}
	// Return an independent nested copy (Execution contains slices).
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) HasTool(name string) bool { _, ok := s.byName[name]; return ok }

// PresetAvailable reflects the assembled runtime, not mere Schema registration.
func (s *Service) PresetAvailable(id string) bool {
	preset, found := s.presets[id]
	return found && s.available(preset)
}

func (s *Service) Call(ctx context.Context, scope Scope, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if scope.OwnerID == "" || scope.SessionID == "" {
		return nil, ErrToolDenied
	}
	id, found := s.byName[name]
	if !found {
		return nil, ErrToolDenied
	}
	if !s.PresetAvailable(id) {
		return nil, ErrUnavailable
	}
	preset := s.presets[id]
	var declaration catalog.AgentMCPToolSchema
	for _, tool := range preset.Tools {
		if tool.Name == name {
			declaration = tool
			break
		}
	}
	if err := validateArguments(declaration.Parameters, arguments); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.policy.TimeoutMilliseconds)*time.Millisecond)
	defer cancel()
	peer, err := s.peer(ctx, preset, scope)
	if err != nil {
		return nil, ErrUnavailable
	}
	tools, err := peer.ListTools(ctx)
	if err != nil || len(tools) > s.policy.MaxTools {
		return nil, ErrUnavailable
	}
	matched := false
	seen := map[string]bool{}
	for _, tool := range tools {
		if seen[tool.Name] {
			return nil, ErrProtocol
		}
		seen[tool.Name] = true
		if tool.Name == declaration.RemoteName {
			if !equivalentJSON(tool.InputSchema, declaration.Parameters) {
				return nil, ErrToolDenied
			}
			matched = true
		}
	}
	if !matched {
		return nil, ErrToolDenied
	}
	result, err := peer.CallTool(ctx, declaration.RemoteName, arguments)
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(result)) > s.policy.MaxResponseBytes {
		return nil, ErrProtocol
	}
	return safeResult(result)
}

func equivalentJSON(left, right json.RawMessage) bool {
	var a, b any
	if json.Unmarshal(left, &a) != nil || json.Unmarshal(right, &b) != nil {
		return false
	}
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

// Only the small declarative subset supported by this adapter is admitted.
// Unsupported JSON Schema constructs fail closed instead of being ignored.
type argumentSchema struct {
	Type                 string                     `json:"type"`
	Properties           map[string]json.RawMessage `json:"properties,omitempty"`
	Required             []string                   `json:"required,omitempty"`
	AdditionalProperties *bool                      `json:"additionalProperties,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Enum                 []json.RawMessage          `json:"enum,omitempty"`
	Minimum              *float64                   `json:"minimum,omitempty"`
	Maximum              *float64                   `json:"maximum,omitempty"`
	MinLength            *int                       `json:"minLength,omitempty"`
	MaxLength            *int                       `json:"maxLength,omitempty"`
}

func parseSchema(data json.RawMessage) (argumentSchema, error) {
	var value argumentSchema
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, ErrToolDenied
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return value, ErrToolDenied
	}
	return value, nil
}
func validateSchema(data json.RawMessage) error {
	schema, err := parseSchema(data)
	if err != nil {
		return err
	}
	if schema.Type != "object" || schema.AdditionalProperties == nil || *schema.AdditionalProperties {
		return ErrToolDenied
	}
	if len(schema.Enum) > 0 || schema.Minimum != nil || schema.Maximum != nil || schema.MinLength != nil || schema.MaxLength != nil {
		return ErrToolDenied
	}
	required := map[string]bool{}
	for _, key := range schema.Required {
		if _, ok := schema.Properties[key]; !ok || required[key] {
			return ErrToolDenied
		}
		required[key] = true
	}
	for key, raw := range schema.Properties {
		if key == "owner_id" || key == "session_id" || key == "scope" {
			return ErrToolDenied
		}
		property, err := parseSchema(raw)
		if err != nil {
			return err
		}
		if property.Type != "string" && property.Type != "integer" && property.Type != "boolean" {
			return ErrToolDenied
		}
		if len(property.Properties) > 0 || len(property.Required) > 0 || property.AdditionalProperties != nil {
			return ErrToolDenied
		}
		if property.Minimum != nil && property.Maximum != nil && *property.Minimum > *property.Maximum || property.MinLength != nil && *property.MinLength < 0 || property.MaxLength != nil && *property.MaxLength < 0 || property.MinLength != nil && property.MaxLength != nil && *property.MinLength > *property.MaxLength {
			return ErrToolDenied
		}
		if property.Type != "integer" && (property.Minimum != nil || property.Maximum != nil) || property.Type != "string" && (property.MinLength != nil || property.MaxLength != nil) {
			return ErrToolDenied
		}
		for _, candidate := range property.Enum {
			testSchema, _ := json.Marshal(argumentSchema{Type: "object", Properties: map[string]json.RawMessage{key: raw}})
			input, _ := json.Marshal(map[string]json.RawMessage{key: candidate})
			if err := validateArguments(testSchema, input); err != nil {
				return ErrToolDenied
			}
		}
	}
	return nil
}
func validateArguments(schemaJSON, data json.RawMessage) error {
	schema, err := parseSchema(schemaJSON)
	if err != nil {
		return err
	}
	var values map[string]json.RawMessage
	if json.Unmarshal(data, &values) != nil || values == nil {
		return ErrInvalidArguments
	}
	for _, key := range schema.Required {
		if _, ok := values[key]; !ok {
			return ErrInvalidArguments
		}
	}
	for key, raw := range values {
		propertyJSON, found := schema.Properties[key]
		if !found {
			return ErrInvalidArguments
		}
		property, err := parseSchema(propertyJSON)
		if err != nil {
			return err
		}
		if len(property.Enum) > 0 {
			found := false
			for _, candidate := range property.Enum {
				if equivalentJSON(candidate, raw) {
					found = true
					break
				}
			}
			if !found {
				return ErrInvalidArguments
			}
		}
		switch property.Type {
		case "string":
			var value string
			if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
				return ErrInvalidArguments
			}
			length := len([]rune(value))
			if property.MinLength != nil && length < *property.MinLength || property.MaxLength != nil && length > *property.MaxLength {
				return ErrInvalidArguments
			}
		case "integer":
			var value int64
			if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
				return ErrInvalidArguments
			}
			if property.Minimum != nil && float64(value) < *property.Minimum || property.Maximum != nil && float64(value) > *property.Maximum {
				return ErrInvalidArguments
			}
		case "boolean":
			var value bool
			if json.Unmarshal(raw, &value) != nil || string(raw) == "null" {
				return ErrInvalidArguments
			}
		default:
			return ErrInvalidArguments
		}
	}
	return nil
}

// ValidateArguments is also used by the authenticated MCP server boundary.
func ValidateArguments(schemaJSON, data json.RawMessage) error {
	return validateArguments(schemaJSON, data)
}

var absoluteAddress = regexp.MustCompile(`(?i)https?://[^\s"<>]+`)

func safeResult(data json.RawMessage) (json.RawMessage, error) {
	var value map[string]any
	if json.Unmarshal(data, &value) != nil || value == nil {
		return nil, ErrProtocol
	}
	if kind, ok := value["resultType"].(string); !ok || kind != "complete" {
		return nil, ErrProtocol
	}
	if failed, _ := value["isError"].(bool); failed {
		return nil, ErrUnavailable
	}
	result := map[string]any{"resultType": "complete"}
	var content []any
	for _, raw := range asArray(value["content"]) {
		item, ok := raw.(map[string]any)
		if !ok || item["type"] != "text" {
			continue
		}
		if text, ok := item["text"].(string); ok {
			var embedded any
			if json.Unmarshal([]byte(text), &embedded) == nil {
				encoded, _ := json.Marshal(redact(embedded))
				text = string(encoded)
			} else {
				text = absoluteAddress.ReplaceAllString(text, "[external resource]")
			}
			content = append(content, map[string]string{"type": "text", "text": text})
		}
	}
	if structured, ok := value["structuredContent"]; ok {
		result["structuredContent"] = redact(structured)
	}
	if len(content) == 0 && result["structuredContent"] == nil {
		return nil, ErrProtocol
	}
	result["content"] = content
	return json.Marshal(result)
}
func asArray(value any) []any { result, _ := value.([]any); return result }
func redact(value any) any {
	switch item := value.(type) {
	case map[string]any:
		result := map[string]any{}
		for key, child := range item {
			normalized := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(key))
			switch normalized {
			case "apikey", "authorization", "accesstoken", "token", "opaque", "remoteid", "remotejobid", "providerdata", "checkpoint", "privatecheckpoint", "upstreamurl", "signedurl":
				continue
			}
			result[key] = redact(child)
		}
		return result
	case []any:
		result := make([]any, len(item))
		for i, child := range item {
			result[i] = redact(child)
		}
		return result
	case string:
		return absoluteAddress.ReplaceAllString(item, "[external resource]")
	default:
		return value
	}
}

func (s *Service) String() string {
	return fmt.Sprintf("MCP read-only service (%d presets)", len(s.presets))
}
