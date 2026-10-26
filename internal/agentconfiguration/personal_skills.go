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

// PersonalSkillStore accepts only server-created IDs and scopes every read and
// write by the authenticated owner. It never changes shared configuration.
type PersonalSkillStore interface {
	InsertPersonalSkill(context.Context, string, catalog.SkillSchema, int) error
	ListPersonalSkills(context.Context, string) ([]catalog.SkillSchema, error)
	DeletePersonalSkill(context.Context, string, string) error
}

type CreatePersonalSkillInput struct {
	Name            string `json:"name"`
	Prompt          string `json:"prompt"`
	Category        string `json:"category"`
	RecommendedSize string `json:"recommended_size"`
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
	input.Name = strings.TrimSpace(input.Name)
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > s.schema.Chat.Skills.Creation.MaxNameCharacters ||
		input.Prompt == "" || utf8.RuneCountInString(input.Prompt) > s.schema.AgentConfiguration.Limits.MaxSkillPromptCharacters {
		return catalog.SkillSchema{}, fmt.Errorf("%w: name or prompt", ErrInvalidPersonalSkill)
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return catalog.SkillSchema{}, err
	}
	skill := catalog.SkillSchema{
		ID:   "personal-" + hex.EncodeToString(random[:]),
		Name: input.Name, Category: input.Category,
		RecommendedSize: input.RecommendedSize, Prompt: input.Prompt,
	}
	candidate := clone(s.defaults)
	candidate.Skills = []catalog.SkillSchema{skill}
	if err := s.Validate(candidate); err != nil {
		return catalog.SkillSchema{}, fmt.Errorf("%w: %v", ErrInvalidPersonalSkill, err)
	}
	if err := s.personalSkills.InsertPersonalSkill(ctx, owner, skill, s.schema.AgentConfiguration.Limits.MaxSkillCount); err != nil {
		return catalog.SkillSchema{}, err
	}
	return skill, nil
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
	system := s.schema.Chat.Skills.SystemSkills
	if len(personal) == 0 && len(system) == 0 {
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
	return snap, nil
}
