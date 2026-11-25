package agentconfiguration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

var ErrPersonalSkillLimit = errors.New("personal Skill limit reached")
var ErrInvalidPersonalSkill = errors.New("invalid personal Skill")
var ErrPersonalSkillNotFound = errors.New("personal Skill not found")

// Personal Skill fields a validation error names (#548), so a client that
// saves one field at a time can show the error next to it.
const (
	PersonalSkillFieldName        = "name"
	PersonalSkillFieldPrompt      = "prompt"
	PersonalSkillFieldSlug        = "slug"
	PersonalSkillFieldDescription = "description"
	PersonalSkillFieldApply       = "apply"
)

// PersonalSkillFieldError is an ErrInvalidPersonalSkill that names the field.
type PersonalSkillFieldError struct{ Field string }

func (e PersonalSkillFieldError) Error() string {
	return ErrInvalidPersonalSkill.Error() + ": " + e.Field
}

func (e PersonalSkillFieldError) Unwrap() error { return ErrInvalidPersonalSkill }

// InvalidPersonalSkillField is the field a validation error names, or "".
func InvalidPersonalSkillField(err error) string {
	var field PersonalSkillFieldError
	if errors.As(err, &field) {
		return field.Field
	}
	return ""
}

// PersonalSkillStore accepts only server-created IDs and scopes every read and
// write by the authenticated owner. It never changes shared configuration.
type PersonalSkillStore interface {
	InsertPersonalSkill(context.Context, string, catalog.SkillSchema, int) error
	UpdatePersonalSkill(context.Context, string, string, catalog.SkillSchema) error
	// ModifyPersonalSkill reads the owner's Skill and the owner's other
	// Skills, applies change and writes the result back in one transaction.
	ModifyPersonalSkill(ctx context.Context, owner, id string, change func(current catalog.SkillSchema, others []catalog.SkillSchema) (catalog.SkillSchema, error)) (catalog.SkillSchema, error)
	ListPersonalSkills(context.Context, string) ([]catalog.SkillSchema, error)
	DeletePersonalSkill(context.Context, string, string) error
}

type CreatePersonalSkillInput struct {
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Category        string `json:"category"`
	RecommendedSize string `json:"recommended_size"`
	// Slug, Description and Apply are the standard fields (#548). An empty
	// slug takes one derived from the new ID; an empty description reads a
	// derived one; an empty apply reads as a style Skill.
	Slug        string `json:"slug,omitempty"`
	Description string `json:"description,omitempty"`
	Apply       string `json:"apply,omitempty"`
}

// PatchPersonalSkillInput changes only the members it carries (#548).
type PatchPersonalSkillInput struct {
	Name        *string `json:"name,omitempty"`
	Prompt      *string `json:"prompt,omitempty"`
	Slug        *string `json:"slug,omitempty"`
	Description *string `json:"description,omitempty"`
}

func WithPersonalSkills(store PersonalSkillStore) Option {
	return func(s *Service) { s.personalSkills = store }
}

func (s *Service) CreatePersonalSkill(ctx context.Context, owner string, input CreatePersonalSkillInput) (catalog.SkillSchema, error) {
	if s.personalSkills == nil {
		return catalog.SkillSchema{}, errors.New("personal Skill store unavailable")
	}
	if strings.TrimSpace(owner) == "" {
		return catalog.SkillSchema{}, errors.New("personal Skill owner is empty")
	}
	content, err := s.personalSkillContent(input)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return catalog.SkillSchema{}, err
	}
	skill := catalog.SkillSchema{
		ID:   "personal-" + hex.EncodeToString(random[:]),
		Name: content.Name, Category: content.Category,
		RecommendedSize: content.RecommendedSize, Prompt: content.Prompt,
		Slug: content.Slug, Description: content.Description, Apply: content.Apply,
	}
	// A Skill saved without a slug keeps the one derived from its ID, so the
	// person sees and can edit the name the Agent's directory lists.
	if skill.Slug == "" {
		skill.Slug = catalog.SkillSlug(skill)
	}
	others, err := s.personalSkills.ListPersonalSkills(ctx, owner)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	if err := s.validatePersonalSkill(skill, others); err != nil {
		return catalog.SkillSchema{}, err
	}
	if err := s.personalSkills.InsertPersonalSkill(ctx, owner, skill, s.schema.AgentConfiguration.Limits.MaxSkillCount); err != nil {
		return catalog.SkillSchema{}, err
	}
	return skill, nil
}

// UpdatePersonalSkill replaces the owner's own Skill content in place. The ID
// and creation order stay; system, shared and other owners' Skills are not
// rows here, so they are reported as not found.
func (s *Service) UpdatePersonalSkill(ctx context.Context, owner, id string, input CreatePersonalSkillInput) (catalog.SkillSchema, error) {
	if s.personalSkills == nil || strings.TrimSpace(owner) == "" || !validPersonalSkillID(id) {
		return catalog.SkillSchema{}, ErrPersonalSkillNotFound
	}
	content, err := s.personalSkillContent(input)
	if err != nil {
		return catalog.SkillSchema{}, err
	}
	skill := catalog.SkillSchema{
		ID:   id,
		Name: content.Name, Category: content.Category,
		RecommendedSize: content.RecommendedSize, Prompt: content.Prompt,
		Slug: content.Slug, Description: content.Description, Apply: content.Apply,
	}
	if skill.Slug == "" {
		skill.Slug = catalog.SkillSlug(skill)
	}
	return s.personalSkills.ModifyPersonalSkill(ctx, owner, id, func(_ catalog.SkillSchema, others []catalog.SkillSchema) (catalog.SkillSchema, error) {
		if err := s.validatePersonalSkill(skill, others); err != nil {
			return catalog.SkillSchema{}, err
		}
		return skill, nil
	})
}

// PatchPersonalSkill changes only the given members of the owner's Skill
// (#548): the stored Skill is read, merged and checked by the same validator
// as creation, and written back in one transaction. Only later turns see the
// change; a turn in flight keeps its frozen copy.
func (s *Service) PatchPersonalSkill(ctx context.Context, owner, id string, input PatchPersonalSkillInput) (catalog.SkillSchema, error) {
	if s.personalSkills == nil || strings.TrimSpace(owner) == "" || !validPersonalSkillID(id) {
		return catalog.SkillSchema{}, ErrPersonalSkillNotFound
	}
	return s.personalSkills.ModifyPersonalSkill(ctx, owner, id, func(current catalog.SkillSchema, others []catalog.SkillSchema) (catalog.SkillSchema, error) {
		next := current
		if input.Name != nil {
			next.Name = *input.Name
		}
		if input.Prompt != nil {
			next.Prompt = *input.Prompt
		}
		if input.Description != nil {
			next.Description = strings.TrimSpace(*input.Description)
			// Clearing it would bring the derived text back silently: the
			// standard requires a description, so an explicit one is kept.
			if next.Description == "" {
				return catalog.SkillSchema{}, PersonalSkillFieldError{PersonalSkillFieldDescription}
			}
		}
		if input.Slug != nil {
			next.Slug = strings.TrimSpace(*input.Slug)
			if next.Slug == "" {
				next.Slug = catalog.SkillSlug(catalog.SkillSchema{ID: id})
			}
		}
		content, err := s.personalSkillContent(CreatePersonalSkillInput{
			Name: next.Name, Prompt: next.Prompt, Category: next.Category, RecommendedSize: next.RecommendedSize,
			Slug: next.Slug, Description: next.Description, Apply: next.Apply,
		})
		if err != nil {
			return catalog.SkillSchema{}, err
		}
		next.Name, next.Prompt, next.Slug, next.Description = content.Name, content.Prompt, content.Slug, content.Description
		if err := s.validatePersonalSkill(next, others); err != nil {
			return catalog.SkillSchema{}, err
		}
		return next, nil
	})
}

// validPersonalSkillID accepts only the IDs the server generates. System and
// shared Skill IDs can never name a personal row, so update reports them as
// not found before any content validation.
func validPersonalSkillID(id string) bool {
	const prefix = "personal-"
	if !strings.HasPrefix(id, prefix) || len(id) != len(prefix)+32 {
		return false
	}
	_, err := hex.DecodeString(id[len(prefix):])
	return err == nil
}

// personalSkillContent trims and validates the fields shared by creation and
// whole-object update, so both accept exactly the same text.
func (s *Service) personalSkillContent(input CreatePersonalSkillInput) (CreatePersonalSkillInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	input.Slug = strings.TrimSpace(input.Slug)
	input.Description = strings.TrimSpace(input.Description)
	input.Apply = strings.TrimSpace(input.Apply)
	switch {
	case input.Name == "" || utf8.RuneCountInString(input.Name) > s.schema.Chat.Skills.Creation.MaxNameCharacters:
		return CreatePersonalSkillInput{}, PersonalSkillFieldError{PersonalSkillFieldName}
	case input.Prompt == "" || utf8.RuneCountInString(input.Prompt) > s.schema.AgentConfiguration.Limits.MaxSkillPromptCharacters:
		return CreatePersonalSkillInput{}, PersonalSkillFieldError{PersonalSkillFieldPrompt}
	case input.Slug != "" && !catalog.ValidSkillSlug(input.Slug):
		return CreatePersonalSkillInput{}, PersonalSkillFieldError{PersonalSkillFieldSlug}
	case input.Description != "" && catalog.ValidateSkillDescription(input.Description) != nil:
		return CreatePersonalSkillInput{}, PersonalSkillFieldError{PersonalSkillFieldDescription}
	case input.Apply != "" && input.Apply != catalog.SkillApplyGenerationPrompt && input.Apply != catalog.SkillApplyInstruction:
		return CreatePersonalSkillInput{}, PersonalSkillFieldError{PersonalSkillFieldApply}
	}
	return input, nil
}

// validatePersonalSkill runs the configuration validator on the Skill and
// keeps its slug unique among the owner's other Skills (the validator already
// keeps it off the system Skills' slugs). A stored Skill without a slug takes
// its derived one here too.
func (s *Service) validatePersonalSkill(skill catalog.SkillSchema, others []catalog.SkillSchema) error {
	for _, other := range others {
		if other.ID != skill.ID && catalog.SkillSlug(other) == catalog.SkillSlug(skill) {
			return PersonalSkillFieldError{PersonalSkillFieldSlug}
		}
	}
	candidate := clone(s.defaults)
	candidate.Skills = []catalog.SkillSchema{skill}
	if err := s.Validate(candidate); err != nil {
		if strings.Contains(err.Error(), "slug") {
			return fmt.Errorf("%w: %v", PersonalSkillFieldError{PersonalSkillFieldSlug}, err)
		}
		return fmt.Errorf("%w: %v", ErrInvalidPersonalSkill, err)
	}
	return nil
}

// TakenSkillSlugs are the slugs the owner can no longer give a new Skill: the
// system Skills' and the owner's own (derived ones included).
func (s *Service) TakenSkillSlugs(ctx context.Context, owner string) (map[string]bool, error) {
	taken := map[string]bool{}
	for _, skill := range s.schema.Chat.Skills.SystemSkills {
		taken[catalog.SkillSlug(skill)] = true
	}
	personal, err := s.PersonalSkills(ctx, owner)
	if err != nil {
		return nil, err
	}
	for _, skill := range personal {
		taken[catalog.SkillSlug(skill)] = true
	}
	return taken, nil
}

// DeletePersonalSkill removes the owner's own Skill. System and shared Skills
// are never personal rows, so they are reported as not found.
func (s *Service) DeletePersonalSkill(ctx context.Context, owner, id string) error {
	if s.personalSkills == nil || strings.TrimSpace(owner) == "" || strings.TrimSpace(id) == "" {
		return ErrPersonalSkillNotFound
	}
	return s.personalSkills.DeletePersonalSkill(ctx, owner, id)
}

func (s *Service) PersonalSkills(ctx context.Context, owner string) ([]catalog.SkillSchema, error) {
	if strings.TrimSpace(owner) == "" {
		return nil, errors.New("personal Skill owner is empty")
	}
	if s.personalSkills == nil {
		return []catalog.SkillSchema{}, nil
	}
	skills, err := s.personalSkills.ListPersonalSkills(ctx, owner)
	if err != nil {
		return nil, err
	}
	if skills == nil {
		skills = []catalog.SkillSchema{}
	}
	return skills, nil
}

// SnapshotForOwner combines current shared content with the owner's personal
// Skills only when a new turn or owner-scoped projection requests it.
func (s *Service) SnapshotForOwner(ctx context.Context, owner string) (Snapshot, error) {
	snap, err := s.Snapshot(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	personal, err := s.PersonalSkills(ctx, owner)
	if err != nil {
		return Snapshot{}, err
	}
	// The tools a version resolves to follow the Schema (#525) and its prompts
	// are the Schema's (#554), so the same stored revision can mean different
	// content after an upgrade. The
	// digest, appended last, keeps a turn's frozen revision from naming two
	// different configurations, the same way the system Skill digest does.
	resolvedDigest, err := resolvedContentDigest(snap.Configuration)
	if err != nil {
		return Snapshot{}, err
	}
	schemaSuffix := ":schema:" + resolvedDigest
	system := s.schema.Chat.Skills.SystemSkills
	if len(personal) == 0 && len(system) == 0 {
		snap.Revision += schemaSuffix
		return snap, nil
	}
	seen := make(map[string]bool, len(snap.Configuration.Skills)+len(personal)+len(system))
	merged := make([]catalog.SkillSchema, 0, len(system)+len(snap.Configuration.Skills)+len(personal))
	// System Skills come first and are always offered: they belong to the
	// Product Schema, not to whichever Agent configuration version is live.
	for _, skill := range system {
		seen[skill.ID] = true
		merged = append(merged, skill)
	}
	for _, skill := range snap.Configuration.Skills {
		if seen[skill.ID] {
			return Snapshot{}, fmt.Errorf("duplicate shared and system Skill ID %q", skill.ID)
		}
		seen[skill.ID] = true
		merged = append(merged, skill)
	}
	for _, skill := range personal {
		if seen[skill.ID] {
			return Snapshot{}, fmt.Errorf("duplicate shared and personal Skill ID %q", skill.ID)
		}
		seen[skill.ID] = true
		merged = append(merged, skill)
	}
	snap.Configuration.Skills = merged
	if len(system) > 0 {
		encoded, err := json.Marshal(system)
		if err != nil {
			return Snapshot{}, err
		}
		digest := sha256.Sum256(encoded)
		snap.Revision += ":system:" + hex.EncodeToString(digest[:8])
	}
	if len(personal) > 0 {
		encoded, err := json.Marshal(personal)
		if err != nil {
			return Snapshot{}, err
		}
		digest := sha256.Sum256(encoded)
		snap.Revision += ":personal:" + hex.EncodeToString(digest[:8])
	}
	snap.Revision += schemaSuffix
	return snap, nil
}

// resolvedContentDigest identifies the parts of an effective configuration
// that come from the Schema rather than the stored version: the system prompt
// and the domain contexts (#554), and the enabled tools (#525).
func resolvedContentDigest(c Configuration) (string, error) {
	encoded, err := json.Marshal(struct {
		SystemPrompt  string            `json:"system_prompt"`
		DomainContext map[string]string `json:"domain_context"`
		Tools         []string          `json:"tools"`
	}{c.SystemPrompt, c.DomainContext, c.EnabledToolNames})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:8]), nil
}
