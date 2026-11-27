// Package agentconfiguration manages bounded, versioned Agent content. It never
// edits executable code, provider connections, task states or execution grants.
package agentconfiguration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	schemacontract "github.com/LeXwDeX/Plinth/internal/schema"
)

var ErrConflict = errors.New("agent configuration revision conflict")
var ErrNotFound = errors.New("agent configuration revision not found")

// ConfigurationFormat marks a stored version that records the tools an
// administrator turned off relative to the Schema (#525), not a frozen copy of
// the list, so a built-in tool a later release adds is on unless someone turns
// it off. A version without the marker is the older frozen form and is
// converted when it is read (see resolve).
const ConfigurationFormat = 2

// Configuration is both the effective configuration the API, the admin page
// and a turn see, and the stored form of a version. The stored-only fields
// (Format, DisabledToolNames) are empty in every effective configuration and
// are rejected when a client submits them.
//
// SystemPrompt and DomainContext are product constants (#554): every read and
// every publish sets them to the current Schema's, so a release replaces them
// everywhere. A version stores the text of its day only as a mirror an older
// build can read; whatever text a stored version or a submission carries is
// ignored.
type Configuration struct {
	SystemPrompt        string                `json:"system_prompt"`
	DomainContext       map[string]string     `json:"domain_context"`
	Skills              []catalog.SkillSchema `json:"skills"`
	EnabledToolNames    []string              `json:"enabled_tool_names"`
	EnabledMCPPresetIDs []string              `json:"enabled_mcp_preset_ids"`
	// Format is ConfigurationFormat for a version stored in the current form.
	// A format 2 version still carries enabled_tool_names, as a compatibility
	// mirror limited to tools the v1.8.0 Schema declared: an older build that
	// does not know the format reads it as an ordinary frozen list and finds
	// no tool name it does not declare, so rolling back never reports an
	// unknown selection. Format 2 readers ignore the mirror.
	Format int `json:"format,omitempty"`
	// DisabledToolNames lists the declared tools the administrator turned off.
	DisabledToolNames []string `json:"disabled_tool_names,omitempty"`
	// SkillOrigins is stored only (#665): for every Skill of a packaged ID (a
	// current default or a retired built-in) whether the version carries the
	// product's own copy (SkillOriginPackaged) or one an administrator edited
	// (SkillOriginEdited). It is written on every publish and never read from
	// a submission or shown; a version stored before it has no entry.
	SkillOrigins map[string]string `json:"skill_origins,omitempty"`
}

// The values of Configuration.SkillOrigins.
const (
	SkillOriginPackaged = "packaged"
	SkillOriginEdited   = "edited"
)

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
	// PromptsReadonly tells the page the prompts in Configuration are shown
	// for reference: they ship with the Schema and are not edited here (#554).
	PromptsReadonly bool          `json:"prompts_readonly"`
	Presets         Configuration `json:"presets"`
	Tools           []Tool        `json:"tools"`
	MCPPresets      []MCPPreset   `json:"mcp_presets"`
	History         []History     `json:"history"`
	// SkippedSkills names the packaged Skills of the published version that
	// fail the current Schema and are left out of every turn (#665). The log
	// warns once per Skill per process; this keeps the skip in front of the
	// administrator for as long as it lasts. Empty when nothing is skipped.
	SkippedSkills []string `json:"skipped_skills,omitempty"`
}
type MCPPreset struct {
	Available       bool     `json:"available"`
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	ToolNames       []string `json:"tool_names"`
	Transport       string   `json:"transport"`
	ProtocolVersion string   `json:"protocol_version"`
	// DefaultEnabled is what a fresh configuration starts from (#445). The
	// published configuration's own selection stays authoritative.
	DefaultEnabled bool `json:"default_enabled"`
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
	// packagedFingerprints identifies a stored Skill as the product's own
	// copy: the exact content a release saved (retired built-ins). A Skill
	// with the same ID but other content was edited by an administrator.
	packagedFingerprints map[string]string
	// skippedWarned keeps the skip warning to one line per Skill per process.
	skippedWarned  sync.Map
	personalSkills PersonalSkillStore
	// legacy and toolBaseline are what a version without a format marker is
	// read against (see resolve).
	legacy       legacyDefaults
	toolBaseline map[string]bool
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
	legacy, err := loadLegacyDefaults()
	if err != nil {
		return nil, err
	}
	s.legacy, s.toolBaseline = legacy, legacy.toolBaseline()
	s.packagedSkillImages = map[string]bool{}
	s.packagedFingerprints = map[string]string{}
	retired, err := catalog.RetiredBuiltInSkills()
	if err != nil {
		return nil, err
	}
	for _, skill := range retired {
		s.packagedSkillImages[skill.ThumbnailPath] = true
		s.packagedFingerprints[skill.ID] = skill.Fingerprint
		if s.retiredBuiltIns != nil {
			s.retiredBuiltIns[skill.ID] = skill.Fingerprint
		}
	}
	s.defaults = Configuration{SystemPrompt: schema.Chat.Agent.SystemPrompt, DomainContext: schema.Chat.Agent.DomainContext, Skills: skills.Skills, EnabledToolNames: []string{}, EnabledMCPPresetIDs: []string{}}
	// The completion tool submit_asset_list_item only finishes its work when the
	// Catalog turns application.asset_list.execution on: without a run no item
	// reaches working, so a default table that offers it invites calls that can
	// never complete (#494). Propose and read do not need a run and stay in the
	// default table. The tool stays declared and publishable, like a tool
	// without an enabled Offering.
	assetListExecution := schema.AssetListExecutionEnabled()
	// A tool whose capabilities have no enabled Offering is declared in the
	// Schema but not offered: it is not listed, not enabled by default and
	// dropped from every projection (#115). Enabling an Offering brings it
	// back without any other change.
	declare := func(name, description string, execution catalog.ChatToolExecutionSchema) {
		if !catalog.AnyCapabilityActive(execution.Capabilities, s.active) ||
			(catalog.RequiresAssetListExecution(name) && !assetListExecution) {
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
	// A preset may declare that a new configuration starts without it (#445):
	// the built-in tool it repeats is already in the default tool table, and
	// offering both splits the model's choice without adding a capability.
	// An absent declaration keeps the historical behavior; a published
	// configuration keeps whichever presets the administrator selected.
	for _, preset := range schema.AgentConfiguration.MCPPresets {
		if !preset.EnabledByDefault() {
			continue
		}
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
	result.SkillOrigins = nil
	return result
}

// Effective returns what a configuration amounts to: the Schema's prompts
// (#554), and tools and prompt sections without an enabled Offering left out.
func (s *Service) Effective(c Configuration) Configuration {
	return s.project(s.withSchemaPrompts(c))
}

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

// Snapshot reads the latest version as the effective configuration.
func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	snapshot, _, err := s.snapshot(ctx)
	return snapshot, err
}

// snapshot is Snapshot with the packaged Skills the read left out.
func (s *Service) snapshot(ctx context.Context) (Snapshot, []catalog.SkillSchema, error) {
	v, err := s.store.LatestAgentConfiguration(ctx)
	if errors.Is(err, ErrNotFound) {
		return Snapshot{"0", clone(s.defaults)}, nil, nil
	}
	if err != nil {
		return Snapshot{}, nil, err
	}
	stored, skipped, err := s.tolerant(v.Revision, s.resolve(v))
	if err != nil {
		return Snapshot{}, nil, err
	}
	return Snapshot{v.Revision, s.project(stored)}, skipped, nil
}

// tolerant validates a stored configuration. A Schema upgrade may tighten
// what a packaged Skill must satisfy; that must not stop every new turn or the
// service from starting (#665). A packaged Skill that no longer passes is
// left out of what is read, with one warning per Skill per process; the stored
// version is not rewritten, so it comes back once the Schema accepts it again.
// A Skill an administrator or a person wrote (including one that reuses a
// packaged ID with edited content, see isPackagedSkill) is never dropped: it
// fails, naming it. A failing part other than the Skills (tool selection, MCP
// presets) is reported as it is, before any Skill is judged.
func (s *Service) tolerant(revision string, stored Configuration) (Configuration, []catalog.SkillSchema, error) {
	err := s.Validate(stored)
	if err == nil {
		return stored, nil, nil
	}
	withoutSkills := stored
	withoutSkills.Skills = nil
	if restErr := s.Validate(withoutSkills); restErr != nil {
		return Configuration{}, nil, fmt.Errorf("agent configuration %s: %w", revision, restErr)
	}
	cleaned, skipped, invalidOwn := s.splitInvalidSkills(stored)
	if len(invalidOwn) > 0 {
		return Configuration{}, nil, fmt.Errorf("agent configuration %s: skills %v fail the current Schema: %w", revision, invalidOwn, err)
	}
	if len(skipped) == 0 {
		return Configuration{}, nil, err
	}
	if cleanedErr := s.Validate(cleaned); cleanedErr != nil {
		return Configuration{}, nil, cleanedErr
	}
	for _, skill := range skipped {
		if _, warned := s.skippedWarned.LoadOrStore(skill.ID, true); !warned {
			slog.Warn("Agent 配置中的内置 Skill 未通过当前 Schema 校验，已跳过",
				"revision", revision, "skill", skill.ID, "error", err)
		}
	}
	return cleaned, skipped, nil
}

// isPackagedSkill reports whether a stored Skill is the product's own copy
// (#665). The origin the publish recorded decides: an administrator who edited
// a Skill of a packaged ID is known by it, not by comparing content, so a copy
// of an older release's text stays the product's after a release edits it. A
// version stored before origins were recorded cannot tell an older packaged
// text from an edit: a Skill of a packaged ID (a current default or a retired
// built-in) is taken as packaged, as the read did before (skip by ID), so it
// cannot stop every turn; the next publish records its origin.
func (s *Service) isPackagedSkill(skill catalog.SkillSchema, origins map[string]string) bool {
	if origins[skill.ID] == SkillOriginEdited {
		return false
	}
	return s.packagedID(skill.ID)
}

// defaultSkillID reports whether id is a Skill of the current default catalog.
func (s *Service) defaultSkillID(id string) bool {
	for _, preset := range s.defaults.Skills {
		if preset.ID == id {
			return true
		}
	}
	return false
}

// packagedID reports whether id is a current default or a retired built-in.
func (s *Service) packagedID(id string) bool {
	_, retired := s.packagedFingerprints[id]
	return retired || s.defaultSkillID(id)
}

// sameAsPackaged reports whether a Skill carries exactly the product's content:
// the current default of its ID, or the retired built-in saved unchanged. The
// comparison is catalog.RetiredSkillFingerprint, which a new SkillSchema field
// left at its zero value does not change.
func (s *Service) sameAsPackaged(skill catalog.SkillSchema) bool {
	got, err := catalog.RetiredSkillFingerprint(skill)
	if err != nil {
		return false
	}
	for _, preset := range s.defaults.Skills {
		if preset.ID == skill.ID {
			if want, err := catalog.RetiredSkillFingerprint(preset); err == nil && want == got {
				return true
			}
		}
	}
	want, known := s.packagedFingerprints[skill.ID]
	return known && got == want
}

// skillOrigins records, for every Skill of a packaged ID that a version will
// store, whether it is the product's copy (#665): equal to the packaged
// content, or carried unchanged from the version it replaces (previous) where
// it was the product's copy. Any other content was written by an
// administrator.
func (s *Service) skillOrigins(skills []catalog.SkillSchema, previous Configuration) map[string]string {
	prior := map[string]catalog.SkillSchema{}
	for _, skill := range previous.Skills {
		prior[skill.ID] = skill
	}
	origins := map[string]string{}
	for _, skill := range skills {
		if !s.packagedID(skill.ID) {
			continue
		}
		origin := SkillOriginEdited
		if s.sameAsPackaged(skill) {
			origin = SkillOriginPackaged
		} else if before, found := prior[skill.ID]; found && sameSkillContent(before, skill) &&
			s.isPackagedSkill(before, previous.SkillOrigins) {
			origin = SkillOriginPackaged
		}
		origins[skill.ID] = origin
	}
	if len(origins) == 0 {
		return nil
	}
	return origins
}

func sameSkillContent(a, b catalog.SkillSchema) bool {
	x, errA := catalog.RetiredSkillFingerprint(a)
	y, errB := catalog.RetiredSkillFingerprint(b)
	return errA == nil && errB == nil && x == y
}

// splitInvalidSkills validates each Skill on its own, apart from the rest of
// the configuration: an invalid packaged Skill is removed from the result and
// returned in skipped; the IDs of other invalid Skills are returned in
// invalidOwn and kept in the result.
func (s *Service) splitInvalidSkills(c Configuration) (Configuration, []catalog.SkillSchema, []string) {
	kept := make([]catalog.SkillSchema, 0, len(c.Skills))
	var skipped []catalog.SkillSchema
	var invalidOwn []string
	for _, skill := range c.Skills {
		alone := Configuration{Skills: []catalog.SkillSchema{skill}}
		if s.Validate(alone) != nil {
			if s.isPackagedSkill(skill, c.SkillOrigins) {
				skipped = append(skipped, skill)
				continue
			}
			invalidOwn = append(invalidOwn, skill.ID)
		}
		kept = append(kept, skill)
	}
	c.Skills = kept
	return c, skipped, invalidOwn
}

func (s *Service) Management(ctx context.Context) (Management, error) {
	v, skipped, err := s.snapshot(ctx)
	if err != nil {
		return Management{}, err
	}
	history, err := s.store.AgentConfigurationHistory(ctx, 100)
	if err != nil {
		return Management{}, err
	}
	result := Management{Snapshot: v, PromptsReadonly: true, Presets: clone(s.defaults), Tools: append([]Tool(nil), s.tools...), MCPPresets: []MCPPreset{}, History: []History{}}
	for _, preset := range s.schema.AgentConfiguration.MCPPresets {
		names := []string{}
		for _, tool := range preset.Tools {
			names = append(names, tool.Name)
		}
		result.MCPPresets = append(result.MCPPresets, MCPPreset{Available: false, ID: preset.ID, Name: preset.Name, Description: preset.Description, ToolNames: names, Transport: preset.Transport, ProtocolVersion: preset.ProtocolVersion, DefaultEnabled: preset.EnabledByDefault()})
	}

	for i := range result.Tools {
		result.Tools[i].Domains = append([]string(nil), result.Tools[i].Domains...)
	}
	for _, h := range history {
		result.History = append(result.History, History{h.Revision, h.CreatedAt, h.ActorID, h.Source})
	}
	for _, skill := range skipped {
		result.SkippedSkills = append(result.SkippedSkills, skill.ID)
	}
	return result, nil
}

// Validate checks a configuration as it would be published: the prompts it
// carries are replaced by the Schema's first (#554), so a client that still
// sends them, or sends none, is judged on its other content only.
func (s *Service) Validate(c Configuration) error {
	if c.Format != 0 || len(c.DisabledToolNames) > 0 {
		return errors.New("format and disabled_tool_names are stored by the server")
	}
	c = s.withSchemaPrompts(c)
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
	slugs := map[string]bool{}
	for _, skill := range s.schema.Chat.Skills.SystemSkills {
		systemSkillIDs[skill.ID] = true
		slugs[skill.Slug] = true
	}
	for _, skill := range c.Skills {
		// #548: the standard name, description and kind of a published or
		// personal Skill keep the Agent Skills rules; a slug is unique and
		// never takes a system Skill's.
		if skill.Apply != "" && skill.Apply != catalog.SkillApplyGenerationPrompt && skill.Apply != catalog.SkillApplyInstruction {
			return errors.New("skill apply must be generation-prompt or instruction")
		}
		if skill.Slug != "" {
			if !catalog.ValidSkillSlug(skill.Slug) || slugs[skill.Slug] {
				return errors.New("skill slug is invalid or already used")
			}
			slugs[skill.Slug] = true
		}
		if skill.Description != "" {
			if err := catalog.ValidateSkillDescription(skill.Description); err != nil {
				return fmt.Errorf("invalid skill description: %w", err)
			}
		}
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
	tools, presets := s.declaredSelections()
	if err := validateIDs(c.EnabledToolNames, tools); err != nil {
		return err
	}
	return validateIDs(c.EnabledMCPPresetIDs, presets)
}

// declaredSelections lists the tool names and MCP preset IDs the current
// Schema declares. A declared tool without an enabled Offering stays
// acceptable, so an older client or version can still be validated; Publish
// drops it.
func (s *Service) declaredSelections() (tools, presets map[string]bool) {
	tools = map[string]bool{}
	for _, t := range s.tools {
		tools[t.Name] = true
	}
	for name := range s.hiddenTools {
		tools[name] = true
	}
	presets = map[string]bool{}
	for _, p := range s.schema.AgentConfiguration.MCPPresets {
		presets[p.ID] = true
	}
	return tools, presets
}

// withoutUndeclaredSelections is the read view of a stored version (#500): a
// tool or MCP preset the Schema no longer declares, such as observe_task and
// request_consent after #491, was valid when the version was published, so it
// is left out instead of making the whole version unreadable. Validate stays
// strict, so a new submission naming it is still rejected. The stored version
// is never rewritten.
func (s *Service) withoutUndeclaredSelections(c Configuration) (Configuration, bool) {
	legacy := c.Format < ConfigurationFormat
	tools, presets := s.declaredSelections()
	cleaned := clone(c)
	keep := func(ids []string, allowed map[string]bool) ([]string, bool) {
		kept := make([]string, 0, len(ids))
		seen := map[string]bool{}
		for _, id := range ids {
			if allowed[id] && !seen[id] {
				seen[id] = true
				kept = append(kept, id)
			}
		}
		return kept, len(kept) != len(ids)
	}
	var droppedTools, droppedPresets bool
	cleaned.EnabledToolNames, droppedTools = keep(cleaned.EnabledToolNames, tools)
	cleaned.EnabledMCPPresetIDs, droppedPresets = keep(cleaned.EnabledMCPPresetIDs, presets)
	// A format 2 version keeps tools as a disabled list and a compatibility
	// mirror that resolve ignores, so a name in the mirror is not a defect.
	return cleaned, (legacy && droppedTools) || droppedPresets
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

// Publish stores what an administrator submitted. A packaged Skill that the
// read view left out as invalid (see tolerant) was never shown, so the
// administrator could not have chosen to delete it: it is carried over from
// the version being replaced unless the submission names the same ID (#665).
func (s *Service) Publish(ctx context.Context, expected, actor, source string, c Configuration) (Snapshot, error) {
	carried, previous, err := s.skippedPackagedSkills(ctx, expected)
	if err != nil {
		return Snapshot{}, err
	}
	return s.publish(ctx, expected, actor, source, c, carried, previous)
}

// publish stores c, plus the carried Skills, which are written as stored
// without validation (they are left out again on every read). The origin of
// each packaged-ID Skill is recorded against previous, the version whose
// Skills c carries over (skillOrigins); what a submission says about origins
// is ignored.
func (s *Service) publish(ctx context.Context, expected, actor, source string, c Configuration, carried []catalog.SkillSchema, previous Configuration) (Snapshot, error) {
	c = s.withSchemaPrompts(c)
	if err := s.Validate(c); err != nil {
		return Snapshot{}, err
	}
	if _, err := strconv.ParseUint(expected, 10, 64); err != nil {
		return Snapshot{}, ErrConflict
	}
	stored := s.toStored(c)
	named := map[string]bool{}
	for _, skill := range c.Skills {
		named[skill.ID] = true
	}
	for _, skill := range carried {
		if !named[skill.ID] {
			stored.Skills = append(stored.Skills, skill)
		}
	}
	stored.SkillOrigins = s.skillOrigins(stored.Skills, previous)
	v, err := s.store.AppendAgentConfiguration(ctx, expected, Version{Snapshot: Snapshot{Configuration: stored}, CreatedAt: time.Now().UTC(), ActorID: actor, Source: source})
	// The caller gets the effective configuration, not the stored form.
	v.Snapshot.Configuration = s.project(c)
	return v.Snapshot, err
}

// skippedPackagedSkills returns the invalid packaged Skills of the version
// with the given revision, the ones its read view leaves out, and that
// version's read view.
func (s *Service) skippedPackagedSkills(ctx context.Context, revision string) ([]catalog.SkillSchema, Configuration, error) {
	if revision == "0" {
		return nil, Configuration{}, nil
	}
	latest, err := s.store.LatestAgentConfiguration(ctx)
	if errors.Is(err, ErrNotFound) {
		return nil, Configuration{}, nil
	}
	if err != nil {
		return nil, Configuration{}, err
	}
	if latest.Revision != revision {
		return nil, Configuration{}, nil // the append below fails with ErrConflict
	}
	resolved := s.resolve(latest)
	_, skipped, _ := s.splitInvalidSkills(resolved)
	return skipped, resolved, nil
}
func (s *Service) Restore(ctx context.Context, expected, target, actor string) (Snapshot, error) {
	if target == "0" {
		return s.publish(ctx, expected, actor, "reset", s.defaults, nil, Configuration{})
	}
	v, err := s.store.AgentConfigurationVersion(ctx, target)
	if err != nil {
		return Snapshot{}, err
	}
	// The old version is restored as the administrator chose it: read through
	// resolve like any other read, so tools the old version predates are on and
	// the prompts are the current Schema's.
	resolved := s.resolve(v)
	cleaned, _, err := s.withoutRetiredBuiltIns(resolved)
	if err != nil {
		return Snapshot{}, err
	}
	return s.publish(ctx, expected, actor, "restore:"+target, cleaned, nil, cleaned)
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
// The same version also leaves out tools and MCP presets the Schema no longer
// declares (#500), so the latest version matches what is read from it.
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
		resolved := s.resolve(latest)
		cleaned, retiredSkills, err := s.withoutRetiredBuiltIns(resolved)
		if err != nil {
			return err
		}
		_, undeclared := s.withoutUndeclaredSelections(latest.Configuration)
		if !retiredSkills && !undeclared {
			// The same tolerance as every read: an invalid packaged Skill is
			// skipped without rewriting the version; an administrator's
			// invalid Skill fails startup, naming it.
			_, _, err := s.tolerant(latest.Revision, cleaned)
			return err
		}
		source := "retire_builtin_skills"
		if !retiredSkills {
			source = "retire_undeclared_selections"
		}
		cleaned, skipped, err := s.tolerant(latest.Revision, cleaned)
		if err != nil {
			return err
		}
		_, err = s.publish(ctx, latest.Revision, "system", source, cleaned, skipped, resolved)
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

// resolve turns a stored version into the effective configuration. A tool is
// on unless the administrator turned it off (#525), and the prompts are the
// Schema's whatever the version stored (#554), so what a release adds or
// changes arrives without a republish. Everything else a version carries is
// returned as stored, minus names the Schema no longer declares.
func (s *Service) resolve(v Version) Configuration {
	resolved, _ := s.withoutUndeclaredSelections(v.Configuration)
	resolved.EnabledToolNames = s.resolveTools(v.Configuration)
	resolved.Format, resolved.DisabledToolNames = 0, nil
	return s.withSchemaPrompts(resolved)
}

// withSchemaPrompts returns c with the system prompt and the domain contexts
// set to the Schema's (#554). They are not part of what an administrator
// chooses, so the text a stored version or a submission carries never counts.
func (s *Service) withSchemaPrompts(c Configuration) Configuration {
	result := clone(c)
	result.SystemPrompt = s.defaults.SystemPrompt
	result.DomainContext = maps.Clone(s.defaults.DomainContext)
	if result.DomainContext == nil {
		result.DomainContext = map[string]string{}
	}
	return result
}

// resolveTools is the declared, offered tools minus the ones turned off. A
// version without the format marker did not record what was turned off, only
// what was on: a tool of the v1.8.0 baseline it does not list was turned off,
// a tool outside the baseline did not exist yet and stays on.
func (s *Service) resolveTools(stored Configuration) []string {
	off := map[string]bool{}
	if stored.Format >= ConfigurationFormat {
		for _, name := range stored.DisabledToolNames {
			off[name] = true
		}
	} else {
		on := map[string]bool{}
		for _, name := range stored.EnabledToolNames {
			on[name] = true
		}
		for name := range s.toolBaseline {
			if !on[name] {
				off[name] = true
			}
		}
	}
	result := make([]string, 0, len(s.tools))
	for _, tool := range s.tools {
		if !off[tool.Name] {
			result = append(result, tool.Name)
		}
	}
	return result
}

// toStored is the form a new version is written in: the effective
// configuration the administrator submitted, with tools recorded as the ones
// turned off and a compatibility mirror of the enabled list an older build can
// still read (see Configuration.Format). The prompts are the Schema's of the
// day, kept only so an older build finds a usable text.
func (s *Service) toStored(c Configuration) Configuration {
	effective := s.project(c)
	stored := effective
	stored.Format = ConfigurationFormat
	on := make(map[string]bool, len(effective.EnabledToolNames))
	stored.EnabledToolNames = []string{}
	for _, name := range effective.EnabledToolNames {
		on[name] = true
		if s.toolBaseline[name] {
			stored.EnabledToolNames = append(stored.EnabledToolNames, name)
		}
	}
	for _, tool := range s.tools {
		if !on[tool.Name] {
			stored.DisabledToolNames = append(stored.DisabledToolNames, tool.Name)
		}
	}
	return stored
}
