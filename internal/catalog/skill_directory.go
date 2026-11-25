package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

// The Skill shape follows the Agent Skills standard (#548): a name that is a
// lower-case slug, a description that says what the Skill does and when to use
// it, and a body of instructions (SkillSchema.Prompt). The display name stays
// in SkillSchema.Name; only the slug is the standard's name.
const (
	// SkillApplyGenerationPrompt marks a style Skill: the server writes its
	// prompt into the final generation prompt (the Skill execution templates).
	SkillApplyGenerationPrompt = "generation-prompt"
	// SkillApplyInstruction marks an instruction Skill: the Agent reads its
	// body with use_skill; it never styles a generation prompt.
	SkillApplyInstruction = "instruction"
	// PersonalSkillIDPrefix starts every server-generated personal Skill ID.
	PersonalSkillIDPrefix = "personal-"
	// SkillSlugMaxCharacters and SkillDescriptionMaxCharacters are the Agent
	// Skills limits of name and description.
	SkillSlugMaxCharacters        = 64
	SkillDescriptionMaxCharacters = 1024
)

// SkillKind is how a Skill takes effect.
type SkillKind string

const (
	SkillKindTool        SkillKind = "tool"
	SkillKindStyle       SkillKind = "style"
	SkillKindInstruction SkillKind = "instruction"
)

var skillSlugPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// ValidSkillSlug applies the Agent Skills name rule: 1-64 characters of a-z,
// 0-9 and single hyphens, neither leading nor trailing, and none of the
// reserved words.
func ValidSkillSlug(slug string) bool {
	if slug == "" || len(slug) > SkillSlugMaxCharacters || !skillSlugPattern.MatchString(slug) {
		return false
	}
	return !strings.Contains(slug, "anthropic") && !strings.Contains(slug, "claude")
}

// ValidateSkillDescription applies the Agent Skills description rule plus the
// directory's own: one line, 1-1024 characters, no angle brackets.
func ValidateSkillDescription(description string) error {
	if strings.TrimSpace(description) == "" {
		return errors.New("description 不能为空")
	}
	if utf8.RuneCountInString(description) > SkillDescriptionMaxCharacters {
		return fmt.Errorf("description 超过 %d 字符", SkillDescriptionMaxCharacters)
	}
	if strings.ContainsAny(description, "<>\r\n") {
		return errors.New("description 必须是单行且不能含 < 或 >")
	}
	return nil
}

// Kind tells how the Skill takes effect. A Skill that binds a tool is a tool
// Skill; apply=instruction is an instruction Skill. A stored Skill without
// apply is a style Skill: every Skill written before #548 (the style library,
// shared and personal Skills) styles a generation prompt, and it is read so
// without a data migration.
func (skill SkillSchema) Kind() SkillKind {
	switch {
	case skill.Tool != "":
		return SkillKindTool
	case skill.Apply == SkillApplyInstruction:
		return SkillKindInstruction
	default:
		return SkillKindStyle
	}
}

// SkillSlug is the Skill's standard name: its own slug, or one derived from the
// ID for a Skill stored before slugs existed (skill-<8 hex>).
func SkillSlug(skill SkillSchema) string {
	if skill.Slug != "" {
		return skill.Slug
	}
	digest := sha256.Sum256([]byte(skill.ID))
	return "skill-" + hex.EncodeToString(digest[:4])
}

// SkillDisplayName is the name shown to people: the Chinese name when set.
func SkillDisplayName(skill SkillSchema) string {
	if name := strings.TrimSpace(skill.NameZH); name != "" {
		return name
	}
	return skill.Name
}

// SkillSummary is the first sentence of a Skill prompt, at most maximum
// characters (no bound when maximum is 0).
func SkillSummary(prompt string, maximum int) string {
	text := strings.TrimSpace(prompt)
	if line, _, found := strings.Cut(text, "\n"); found {
		text = strings.TrimSpace(line)
	}
	if end := strings.IndexAny(text, "。！？!?"); end >= 0 {
		_, size := utf8.DecodeRuneInString(text[end:])
		text = text[:end+size]
	} else if end := strings.Index(text, ". "); end >= 0 {
		text = text[:end+1]
	}
	return truncateSkillRunes(text, maximum)
}

// SkillDescriptionFor is the Skill's description, or for a Skill stored before
// descriptions existed one derived from the directory template, its display
// name and the first sentence of its prompt. The result is one line without
// angle brackets.
func SkillDescriptionFor(skill SkillSchema, directory ChatSkillDirectorySchema) string {
	description := strings.TrimSpace(skill.Description)
	if description == "" && directory.DerivedDescriptionTemplate != "" {
		description = strings.NewReplacer(
			"{name}", SkillDisplayName(skill),
			"{summary}", SkillSummary(skill.Prompt, directory.EntryDescriptionMaxCharacters),
		).Replace(directory.DerivedDescriptionTemplate)
	}
	description = strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ", "<", "", ">", "").Replace(description)
	return truncateSkillRunes(strings.TrimSpace(description), SkillDescriptionMaxCharacters)
}

func truncateSkillRunes(value string, maximum int) string {
	if maximum <= 0 || utf8.RuneCountInString(value) <= maximum {
		return value
	}
	return string([]rune(value)[:maximum])
}

// ChatSkillDirectorySchema declares the Skill directory the Agent reads (#548):
// one line per Skill in the system context, the use_skill tool that reads a
// body, and the generation argument that applies a style Skill to one call.
type ChatSkillDirectorySchema struct {
	// Template wraps the directory block; it holds {entries}.
	Template string `json:"template"`
	// EntryTemplate is one line, with {slug}, {name} and {description}.
	EntryTemplate string `json:"entry_template"`
	// EntryDescriptionMaxCharacters cuts the description shown in a line.
	EntryDescriptionMaxCharacters int `json:"entry_description_max_characters"`
	// MaxSystemCharacters and MaxPersonalCharacters bound the system and the
	// personal part of the block, each counted as the lines it renders (the
	// personal part includes its omitted line). Personal Skills fill their
	// part newest first.
	MaxSystemCharacters   int `json:"max_system_characters"`
	MaxPersonalCharacters int `json:"max_personal_characters"`
	// OmittedTemplate, with {count}, closes the personal part when some
	// personal Skills did not fit.
	OmittedTemplate string `json:"omitted_template"`
	// DerivedDescriptionTemplate, with {name} and {summary}, stands in for the
	// description of a Skill stored without one.
	DerivedDescriptionTemplate string `json:"derived_description_template"`
	// Tool is the read-only Agent tool that returns a Skill's body.
	Tool string `json:"tool"`
	// GenerationArgument is the optional argument of a 2D generation tool
	// that applies one style Skill (by slug) to that call only.
	GenerationArgument string `json:"generation_argument"`
	// MaxInstructionCharacters bounds the body use_skill returns.
	MaxInstructionCharacters int `json:"max_instruction_characters"`
	// InstructionPreamble is the fixed server text in front of every body
	// use_skill returns.
	InstructionPreamble string `json:"instruction_preamble"`
	// StyleInstructionTemplate is what use_skill returns for a style Skill,
	// with {name}, {summary}, {slug} and {argument}.
	StyleInstructionTemplate string `json:"style_instruction_template"`
	// Messages are the Agent-facing refusals and notes.
	Messages ChatSkillDirectoryMessages `json:"messages"`
}

type ChatSkillDirectoryMessages struct {
	// Unknown answers a slug that names no Skill of the turn ({name}).
	Unknown string `json:"unknown"`
	// NotStyle answers a generation call whose skill names a Skill that does
	// not style a prompt ({name}).
	NotStyle string `json:"not_style"`
	// Unsupported answers a generation call whose model cannot apply a style
	// Skill ({name}).
	Unsupported string `json:"unsupported"`
	// UserChoiceKept notes that the person's own style Skill was applied
	// instead of the one the Agent named ({name} the person's, {slug} the
	// ignored one).
	UserChoiceKept string `json:"user_choice_kept"`
	// Picked answers use_skill for another Skill bound to the same tool as the
	// Skill the person picked for the turn (#611): the pick's text is already
	// in the turn ({name} the pick, {slug} the Skill not read).
	Picked string `json:"picked"`
}

// Enabled reports whether the Schema declares a directory at all.
func (directory ChatSkillDirectorySchema) Enabled() bool {
	return directory.Tool != ""
}

// validateSkillDirectory checks the directory declaration and that every
// system Skill has a standard name and description that fit the directory.
func validateSkillDirectory(skills ChatSkillsSchema, agentTools map[string]struct{}) error {
	directory := skills.Directory
	if !directory.Enabled() {
		return nil
	}
	if _, exists := agentTools[directory.Tool]; !exists {
		return fmt.Errorf("skills.directory.tool %q 不是已声明的 Agent 工具", directory.Tool)
	}
	for field, text := range map[string]string{
		"template": directory.Template, "entry_template": directory.EntryTemplate,
		"omitted_template": directory.OmittedTemplate, "derived_description_template": directory.DerivedDescriptionTemplate,
		"generation_argument": directory.GenerationArgument, "instruction_preamble": directory.InstructionPreamble,
		"style_instruction_template": directory.StyleInstructionTemplate,
		"messages.unknown":           directory.Messages.Unknown, "messages.not_style": directory.Messages.NotStyle,
		"messages.unsupported": directory.Messages.Unsupported, "messages.user_choice_kept": directory.Messages.UserChoiceKept,
		"messages.picked": directory.Messages.Picked,
	} {
		if strings.TrimSpace(text) == "" {
			return fmt.Errorf("skills.directory.%s 不能为空", field)
		}
	}
	for _, check := range []struct {
		text, placeholder, field string
	}{
		{directory.Template, "{entries}", "template"},
		{directory.EntryTemplate, "{slug}", "entry_template"},
		{directory.EntryTemplate, "{name}", "entry_template"},
		{directory.EntryTemplate, "{description}", "entry_template"},
		{directory.OmittedTemplate, "{count}", "omitted_template"},
		{directory.DerivedDescriptionTemplate, "{summary}", "derived_description_template"},
		{directory.StyleInstructionTemplate, "{slug}", "style_instruction_template"},
		{directory.StyleInstructionTemplate, "{argument}", "style_instruction_template"},
		{directory.Messages.Picked, "{name}", "messages.picked"},
		{directory.Messages.Picked, "{slug}", "messages.picked"},
	} {
		if !strings.Contains(check.text, check.placeholder) {
			return fmt.Errorf("skills.directory.%s 必须包含 %s", check.field, check.placeholder)
		}
	}
	if directory.EntryDescriptionMaxCharacters <= 0 || directory.MaxSystemCharacters <= 0 ||
		directory.MaxPersonalCharacters <= 0 || directory.MaxInstructionCharacters <= 0 {
		return errors.New("skills.directory 的字符上限必须为正数")
	}
	slugs := map[string]struct{}{}
	system := 0
	for index, skill := range skills.SystemSkills {
		if !ValidSkillSlug(skill.Slug) {
			return fmt.Errorf("skills.system_skills[%d] 的 slug %q 不符合 Skill 名称规范", index, skill.Slug)
		}
		if _, exists := slugs[skill.Slug]; exists {
			return fmt.Errorf("skills.system_skills slug 重复: %q", skill.Slug)
		}
		slugs[skill.Slug] = struct{}{}
		if err := ValidateSkillDescription(skill.Description); err != nil {
			return fmt.Errorf("skills.system_skills[%d]: %w", index, err)
		}
		if utf8.RuneCountInString(skill.Prompt) > directory.MaxInstructionCharacters {
			return fmt.Errorf("skills.system_skills[%d] 的 prompt 超过 max_instruction_characters", index)
		}
		system += utf8.RuneCountInString(SkillDirectoryEntry(skill, directory))
	}
	// Every system Skill is listed: the system part must hold all of them.
	if system > directory.MaxSystemCharacters {
		return fmt.Errorf("skills.system_skills 的目录行共 %d 字符，超过 max_system_characters %d", system, directory.MaxSystemCharacters)
	}
	return nil
}

// SkillDirectoryEntry renders one directory line, newline included.
func SkillDirectoryEntry(skill SkillSchema, directory ChatSkillDirectorySchema) string {
	description := truncateSkillRunes(SkillDescriptionFor(skill, directory), directory.EntryDescriptionMaxCharacters)
	return strings.NewReplacer(
		"{slug}", SkillSlug(skill),
		"{name}", SkillDisplayName(skill),
		"{description}", description,
	).Replace(directory.EntryTemplate) + "\n"
}
