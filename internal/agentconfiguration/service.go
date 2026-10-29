// Package agentconfiguration manages bounded, versioned Agent content. It never
// edits executable code, provider connections, task states or execution grants.
package agentconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrConflict = errors.New("agent configuration revision conflict")
var ErrNotFound = errors.New("agent configuration revision not found")

type Configuration struct {
	SystemPrompt        string                `json:"system_prompt"`
	DomainContext       map[string]string     `json:"domain_context"`
	Skills              []catalog.SkillSchema `json:"skills"`
	EnabledToolNames    []string              `json:"enabled_tool_names"`
	EnabledMCPPresetIDs []string              `json:"enabled_mcp_preset_ids"`
}
type Snapshot struct {
	Revision      string        `json:"revision"`
	Configuration Configuration `json:"configuration"`
}
type Version struct {
	Snapshot
	CreatedAt time.Time `json:"created_at"`
	ActorID   string    `json:"actor_id"`
	Source    string    `json:"source"`
}
type Store interface {
	LatestAgentConfiguration(context.Context) (Version, error)
	AgentConfigurationVersion(context.Context, string) (Version, error)
	AgentConfigurationHistory(context.Context, int) ([]Version, error)
	AppendAgentConfiguration(context.Context, string, Version) (Version, error)
}
type Tool struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Domains     []string `json:"domains"`
}
type Management struct {
	Snapshot
	Presets    Configuration `json:"presets"`
	Tools      []Tool        `json:"tools"`
	MCPPresets []MCPPreset   `json:"mcp_presets"`
	History    []History     `json:"history"`
}
type MCPPreset struct {
	Available       bool     `json:"available"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ToolNames       []string `json:"tool_names"`
	Transport       string   `json:"transport"`
	ProtocolVersion string   `json:"protocol_version"`
}
type History struct {
	Revision  string    `json:"revision"`
	CreatedAt time.Time `json:"created_at"`
	ActorID   string    `json:"actor_id"`
	Source    string    `json:"source"`
}
type Service struct {
	store               Store
	schema              catalog.ApplicationSchema
	defaults            Configuration
	tools               []Tool
	active              catalog.CapabilityActive
	hiddenTools         map[string]bool
	offerings           []schemacontract.Offering
	retiredBuiltIns     map[string]string
	packagedSkillImages map[string]bool
	personalSkills      PersonalSkillStore
}

type Option func(*Service)

// WithRetiredBuiltInSkills enables the one-time migration for the packaged
// catalog. Applications loading an external Skill catalog do not use it.
func WithRetiredBuiltInSkills() Option {
	return func(s *Service) { s.retiredBuiltIns = map[string]string{} }
}

func New(store Store, schema catalog.ApplicationSchema, skills catalog.SkillCatalogSchema, offerings []schemacontract.Offering, options ...Option) (*Service, error) {
	if store == nil {
		return nil, errors.New("agent configuration: missing store")
	}
	s := &Service{store: store, schema: schema, offerings: offerings, active: catalog.ActiveCapabilities(offerings), hiddenTools: map[string]bool{}}
	for _, option := range options {
		option(s)
	}
	s.packagedSkillImages = map[string]bool{}
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil {
		return nil, err
	}
	for _, skill := range retired {
		s.packagedSkillImages[skill.ThumbnailPath] = true
		if s.retiredBuiltIns != nil {
			s.retiredBuiltIns[skill.ID] = skill.Fingerprint
		}
	}
	s.defaults = Configuration{SystemPrompt: schema.Chat.Agent.SystemPrompt, DomainContext: schema.Chat.Agent.DomainContext, Skills: skills.Skills, EnabledToolNames: []string{}, EnabledMCPPresetIDs: []string{}}
	// A tool whose capabilities have no enabled Offering is declared in the
	// Schema but not offered: it is not listed, not enabled by default and
	// dropped from every projection (#115). Enabling an Offering brings it
	// back without any other change.
	declare := func(name, description string, execution catalog.ChatToolExecutionSchema) {
		if !catalog.AnyCapabilityActive(execution.Capabilities, s.active) {
			s.hiddenTools[name] = true
			return
		}
		s.tools = append(s.tools, Tool{name, description, execution.Domains})
		s.defaults.EnabledToolNames = append(s.defaults.EnabledToolNames, name)
	}
	for _, tool := range schema.Chat.Tools {
		declare(tool.Name, tool.Description, tool.Execution)
	}
	for _, tool := range schema.Chat.AgentTools {
		declare(tool.Name, tool.Description, tool.Execution)
	}
	s.defaults = s.project(s.defaults)
	// The legacy source URL is provenance only; management and public reads use
	// packaged same-origin imagery rather than publishing a remote source URL.
	s.defaults = clone(s.defaults)
	for i := range s.defaults.Skills {
		s.defaults.Skills[i].RawImageURL = s.defaults.Skills[i].ThumbnailPath
	}
	for _, preset := range schema.AgentConfiguration.MCPPresets {
		s.defaults.EnabledMCPPresetIDs = append(s.defaults.EnabledMCPPresetIDs, preset.ID)
	}
	if err := s.Validate(s.defaults); err != nil {
		return nil, err
	}
	return s, nil
}

// project is the one view of a configuration that is shown or sent to the
// model: tools without an enabled Offering are dropped, and prompt sections
// of capabilities without one are removed from the system prompt and the
// domain contexts. A published version freezes the prompt as text and the
// tool list, so this is applied when it is read and when a new version is
// published; the stored version is never rewritten (#115).
func (s *Service) project(c Configuration) Configuration {
	result := clone(c)
	sections := s.schema.Chat.Agent.PromptSections
	result.SystemPrompt = catalog.StripInactivePromptSections(result.SystemPrompt, sections, s.active)
	for key, value := range result.DomainContext {
		result.DomainContext[key] = catalog.StripInactivePromptSections(value, sections, s.active)
	}
	kept := make([]string, 0, len(result.EnabledToolNames))
	for _, name := range result.EnabledToolNames {
		if !s.hiddenTools[name] {
			kept = append(kept, name)
		}
	}
	result.EnabledToolNames = kept
	return result
}

// Effective returns what a configuration amounts to once tools and prompt
// sections without an enabled Offering are left out.
func (s *Service) Effective(c Configuration) Configuration { return s.project(c) }

func clone(c Configuration) Configuration {
	b, _ := json.Marshal(c)
	var result Configuration
	_ = json.Unmarshal(b, &result)
	if result.EnabledToolNames == nil {
		result.EnabledToolNames = []string{}
	}
	if result.Skills == nil {
		result.Skills = []catalog.SkillSchema{}
	}
	if result.EnabledMCPPresetIDs == nil {
		result.EnabledMCPPresetIDs = []string{}
	}
	if result.DomainContext == nil {
		result.DomainContext = map[string]string{}
	}
	return result
}
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	v, err := s.store.LatestAgentConfiguration(ctx)
	if errors.Is(err, ErrNotFound) {
		return Snapshot{"0", clone(s.defaults)}, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	if err = s.Validate(v.Configuration); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{v.Revision, s.project(v.Configuration)}, nil
}
func (s *Service) Management(ctx context.Context) (Management, error) {
	v, err := s.Snapshot(ctx)
	if err != nil {
		return Management{}, err
	}
	history, err := s.store.AgentConfigurationHistory(ctx, 100)
	if err != nil {
		return Management{}, err
	}
	result := Management{Snapshot: v, Presets: clone(s.defaults), Tools: append([]Tool(nil), s.tools...), MCPPresets: []MCPPreset{}, History: []History{}}
	for _, preset := range s.schema.AgentConfiguration.MCPPresets {
		names := []string{}
		for _, tool := range preset.Tools {
			names = append(names, tool.Name)
		}
		result.MCPPresets = append(result.MCPPresets, MCPPreset{Available: false, ID: preset.ID, Name: preset.Name, Description: preset.Description, ToolNames: names, Transport: preset.Transport, ProtocolVersion: preset.ProtocolVersion})
	}

	for i := range result.Tools {
		result.Tools[i].Domains = append([]string(nil), result.Tools[i].Domains...)
	}
	for _, h := range history {
		result.History = append(result.History, History{h.Revision, h.CreatedAt, h.ActorID, h.Source})
	}
	return result, nil
}
func (s *Service) Validate(c Configuration) error {
	limits := s.schema.AgentConfiguration.Limits
	if strings.TrimSpace(c.SystemPrompt) == "" || utf8.RuneCountInString(c.SystemPrompt) > limits.MaxPromptCharacters {
		return errors.New("invalid system_prompt length")
	}
	for key, value := range c.DomainContext {
		if key != "image" && key != "model-3d" {
			return errors.New("unknown domain context")
		}
		if utf8.RuneCountInString(value) > limits.MaxPromptCharacters {
			return errors.New("domain context exceeds limit")
		}
	}
	if len(c.Skills) > limits.MaxSkillCount {
		return errors.New("invalid skill count")
	}
	if err := catalog.ValidateSkillCatalogAgainstProductSchema(catalog.SkillCatalogSchema{Revision: "managed", Skills: c.Skills}, s.schema, s.offerings); err != nil {
		return err
	}

	categories := map[string]bool{}
	for _, category := range s.schema.Chat.Skills.Categories {
		categories[category.ID] = true
	}
	allowedResources := map[string]bool{}
	for path := range s.packagedSkillImages {
		allowedResources[path] = true
	}
	for _, preset := range s.defaults.Skills {
		allowedResources[preset.ThumbnailPath] = true
	}
	systemSkillIDs := map[string]bool{}
	for _, skill := range s.schema.Chat.Skills.SystemSkills {
		systemSkillIDs[skill.ID] = true
	}
	for _, skill := range c.Skills {
		// Tool-binding Skills are declared by the Product Schema only; a
		// published or personal Skill can neither bind a tool nor reuse the ID.
		if skill.Tool != "" || skill.RequiresImage || skill.DefaultMessage != "" || skill.OneShot || systemSkillIDs[skill.ID] {
			return errors.New("skill must not bind a tool or reuse a system skill id")
		}
		if skill.ImageToImageTemplate != "" && (utf8.RuneCountInString(skill.ImageToImageTemplate) > limits.MaxSkillPromptCharacters || !strings.Contains(skill.ImageToImageTemplate, s.schema.Chat.Skills.Execution.PromptTemplateFields.UserInput) || !strings.Contains(skill.ImageToImageTemplate, s.schema.Chat.Skills.Execution.PromptTemplateFields.SkillPrompt)) {
			return errors.New("skill image template must contain declared user and style fields")
		}
		if (skill.Category != "" && !categories[skill.Category]) || utf8.RuneCountInString(skill.Prompt) > limits.MaxSkillPromptCharacters || utf8.RuneCountInString(skill.Name) > 256 || utf8.RuneCountInString(skill.ID) > 256 {
			return errors.New("invalid skill content")
		}
		for _, path := range []string{skill.RawImageURL, skill.ThumbnailPath} {
			if path == "" {
				continue
			}
			if !allowedResources[path] || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.ContainsAny(path, "\\?#\r\n") {
				return errors.New("skill images must be packaged same-origin paths")
			}
		}
	}
	allowed := map[string]bool{}
	for _, t := range s.tools {
		allowed[t.Name] = true
	}
	// A declared tool without an enabled Offering stays acceptable, so an older
	// client or version can still be validated; Publish drops it.
	for name := range s.hiddenTools {
		allowed[name] = true
	}
	if err := validateIDs(c.EnabledToolNames, allowed); err != nil {
		return err
	}
	allowed = map[string]bool{}
	for _, p := range s.schema.AgentConfiguration.MCPPresets {
		allowed[p.ID] = true
	}
	return validateIDs(c.EnabledMCPPresetIDs, allowed)
}
func validateIDs(ids []string, allowed map[string]bool) error {
	seen := map[string]bool{}
	for _, id := range ids {
		if !allowed[id] || seen[id] {
			return fmt.Errorf("unknown or duplicate selection %q", id)
		}
		seen[id] = true
	}
	return nil
}
func (s *Service) Publish(ctx context.Context, expected, actor, source string, c Configuration) (Snapshot, error) {
	if err := s.Validate(c); err != nil {
		return Snapshot{}, err
	}
	if _, err := strconv.ParseUint(expected, 10, 64); err != nil {
		return Snapshot{}, ErrConflict
	}
	v, err := s.store.AppendAgentConfiguration(ctx, expected, Version{Snapshot: Snapshot{Configuration: s.project(c)}, CreatedAt: time.Now().UTC(), ActorID: actor, Source: source})
	return v.Snapshot, err
}
func (s *Service) Restore(ctx context.Context, expected, target, actor string) (Snapshot, error) {
	if target == "0" {
		return s.Publish(ctx, expected, actor, "reset", s.defaults)
	}
	v, err := s.store.AgentConfigurationVersion(ctx, target)
	if err != nil {
		return Snapshot{}, err
	}
	cleaned, _, err := s.withoutRetiredBuiltIns(v.Configuration)
	if err != nil {
		return Snapshot{}, err
	}
	return s.Publish(ctx, expected, actor, "restore:"+target, cleaned)
}

func (s *Service) withoutRetiredBuiltIns(c Configuration) (Configuration, bool, error) {
	if s.retiredBuiltIns == nil {
		return clone(c), false, nil
	}
	cleaned := clone(c)
	kept := make([]catalog.SkillSchema, 0, len(c.Skills))
	removed := false
	for _, skill := range c.Skills {
		oldFingerprint, known := s.retiredBuiltIns[skill.ID]
		if known {
			fingerprint, err := catalog.RetiredSkillFingerprint(skill)
			if err != nil {
				return Configuration{}, false, err
			}
			if fingerprint == oldFingerprint {
				removed = true
				continue
			}
		}
		kept = append(kept, skill)
	}
	cleaned.Skills = kept
	return cleaned, removed, nil
}

// ReconcileRetiredBuiltInSkills publishes a new version before any new Agent
// turn can freeze a configuration. Historical versions and checkpoints stay
// intact. A concurrent writer is handled through the store's revision CAS.
func (s *Service) ReconcileRetiredBuiltInSkills(ctx context.Context) error {
	if s.retiredBuiltIns == nil {
		return nil
	}
	for attempt := 0; attempt < 8; attempt++ {
		latest, err := s.store.LatestAgentConfiguration(ctx)
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		cleaned, changed, err := s.withoutRetiredBuiltIns(latest.Configuration)
		if err != nil {
			return err
		}
		if !changed {
			return s.Validate(latest.Configuration)
		}
		_, err = s.Publish(ctx, latest.Revision, "system", "retire_builtin_skills", cleaned)
		if errors.Is(err, ErrConflict) {
			continue
		}
		return err
	}
	return ErrConflict
}

// ToolSummaries returns only immutable declarations; ordinary effective reads
// do not load management history or private content from historical versions.
func (s *Service) ToolSummaries() []Tool {
	result := make([]Tool, len(s.tools))
	for i, tool := range s.tools {
		result[i] = tool
		result[i].Domains = append([]string(nil), tool.Domains...)
	}
	return result
}
