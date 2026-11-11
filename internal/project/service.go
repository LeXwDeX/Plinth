package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
)

// Repository is the owner-scoped persistence port. Every method returns
// ErrNotFound for a missing or foreign project/asset.
type Repository interface {
	CreateProject(ctx context.Context, project Project, maximum int) (Project, error)
	ListProjects(ctx context.Context, ownerID string, limit int) ([]Project, error)
	GetProject(ctx context.Context, ownerID, projectID string) (Project, error)
	RenameProject(ctx context.Context, ownerID, projectID, name string, at time.Time) (Project, error)
	DeleteProject(ctx context.Context, ownerID, projectID string) error
	StyleGuide(ctx context.Context, ownerID, projectID string) (StyleGuide, error)
	SetStyleGuide(ctx context.Context, ownerID, projectID string, guide StyleGuide) (StyleGuide, error)
	Conventions(ctx context.Context, ownerID, projectID string) (Conventions, error)
	SetConventions(ctx context.Context, ownerID, projectID string, conventions Conventions) (Conventions, error)
	ListAssets(ctx context.Context, ownerID, projectID string, limit int) ([]Asset, error)
	CreateAsset(ctx context.Context, projectID string, asset Asset, maximum int) (Asset, error)
	GetAsset(ctx context.Context, ownerID, projectID, assetID string) (Asset, error)
	UpdateAsset(ctx context.Context, ownerID, projectID, assetID, kind, name, description string, at time.Time) (Asset, error)
	DeleteAsset(ctx context.Context, ownerID, projectID, assetID string) (bool, error)
}

// AssetSource verifies that a reference belongs to the owner and names a
// usable picture or model. It is implemented by the SQLite adapter and never
// copies bytes: promotion stores the reference only.
type AssetSource interface {
	Picture(ctx context.Context, ownerID string, reference Reference) (Reference, error)
	Model(ctx context.Context, ownerID string, reference Reference) (Reference, error)
}

// Config is the Schema-derived policy of a project service.
type Config struct {
	Schema catalog.ProjectSchema
	// Profiles and ImageProfiles are the declared purpose IDs of
	// chat.model3d_usage and chat.image2d_usage (#240). A convention's
	// profile/image_profile value must name one of them; undeclared values are
	// refused instead of being seeded into a new session's brief.
	Profiles      []string
	ImageProfiles []string
	// Sources verifies owner references. Required when references may be
	// written; nil makes every reference invalid.
	Sources AssetSource
	Clock   func() time.Time
	NewID   func(prefix string) (string, error)
}

// PurposeProfiles returns the declared 3D and 2D purpose IDs a project
// convention may name (#240).
func PurposeProfiles(chat catalog.ChatSchema) (profiles, imageProfiles []string) {
	profiles = make([]string, 0, len(chat.Model3DUsage.Profiles))
	for _, profile := range chat.Model3DUsage.Profiles {
		profiles = append(profiles, profile.ID)
	}
	imageProfiles = make([]string, 0, len(chat.Image2DUsage.Profiles))
	for _, profile := range chat.Image2DUsage.Profiles {
		imageProfiles = append(imageProfiles, profile.ID)
	}
	return profiles, imageProfiles
}

type Service struct {
	repository     Repository
	limits         catalog.ProjectLimitsSchema
	style          catalog.ProjectStyleGuideSchema
	palettePattern *regexp.Regexp
	kinds          map[string]bool
	fields         map[string]catalog.ChatProductionBriefField
	texts          map[string]catalog.ProjectConventionTextSchema
	profiles       map[string]bool
	imageProfiles  map[string]bool
	sources        AssetSource
	clock          func() time.Time
	newID          func(string) (string, error)
}

// New builds a project service from the Catalog policy.
func New(repository Repository, config Config) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	palettePattern, err := regexp.Compile(config.Schema.StyleGuide.PaletteColorPattern)
	if err != nil {
		return nil, fmt.Errorf("%w: palette pattern: %v", ErrInvalid, err)
	}
	limits := config.Schema.Limits
	if limits.MaximumProjectsPerOwner <= 0 ||
		limits.MaximumNameCharacters <= 0 ||
		limits.MaximumAssetsPerProject <= 0 ||
		limits.MaximumListItems <= 0 {
		return nil, fmt.Errorf("%w: project limits", ErrInvalid)
	}
	if len(config.Schema.Assets.Kinds) == 0 {
		return nil, fmt.Errorf("%w: project asset kinds", ErrInvalid)
	}
	kinds := make(map[string]bool, len(config.Schema.Assets.Kinds))
	for _, kind := range config.Schema.Assets.Kinds {
		kinds[kind.ID] = true
	}
	fields := make(map[string]catalog.ChatProductionBriefField, len(config.Schema.Conventions.Fields))
	for _, field := range config.Schema.Conventions.Fields {
		fields[field.Name] = field
	}
	texts := make(map[string]catalog.ProjectConventionTextSchema, len(config.Schema.Conventions.Texts))
	for _, text := range config.Schema.Conventions.Texts {
		texts[text.Name] = text
	}
	profiles := make(map[string]bool, len(config.Profiles))
	for _, profile := range config.Profiles {
		profiles[profile] = true
	}
	imageProfiles := make(map[string]bool, len(config.ImageProfiles))
	for _, profile := range config.ImageProfiles {
		imageProfiles[profile] = true
	}
	clock := config.Clock
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	newID := config.NewID
	if newID == nil {
		newID = randomID
	}
	return &Service{
		repository:     repository,
		limits:         limits,
		style:          config.Schema.StyleGuide,
		palettePattern: palettePattern,
		kinds:          kinds,
		fields:         fields,
		texts:          texts,
		profiles:       profiles,
		imageProfiles:  imageProfiles,
		sources:        config.Sources,
		clock:          clock,
		newID:          newID,
	}, nil
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

// Create adds one project for the owner.
func (service *Service) Create(ctx context.Context, ownerID, name string) (Project, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Project{}, ErrInvalid
	}
	name, err := service.projectName(name)
	if err != nil {
		return Project{}, err
	}
	id, err := service.newID("prj_")
	if err != nil {
		return Project{}, err
	}
	now := service.clock()
	return service.repository.CreateProject(ctx, Project{
		ID: id, OwnerID: ownerID, Name: name, CreatedAt: now, UpdatedAt: now,
	}, service.limits.MaximumProjectsPerOwner)
}

// List reads the owner's projects in update order.
func (service *Service) List(ctx context.Context, ownerID string) ([]Project, error) {
	if strings.TrimSpace(ownerID) == "" {
		return nil, ErrInvalid
	}
	return service.repository.ListProjects(ctx, ownerID, service.limits.MaximumListItems)
}

// Get reads one owner project. A foreign project is ErrNotFound.
func (service *Service) Get(ctx context.Context, ownerID, projectID string) (Project, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return Project{}, ErrNotFound
	}
	return service.repository.GetProject(ctx, ownerID, projectID)
}

// Exists reports whether an owner project exists. It is the narrow port the
// chat service uses when a session is assigned to a project.
func (service *Service) Exists(ctx context.Context, ownerID, projectID string) (bool, error) {
	_, err := service.Get(ctx, ownerID, projectID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Rename changes the owner's project name.
func (service *Service) Rename(ctx context.Context, ownerID, projectID, name string) (Project, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return Project{}, ErrNotFound
	}
	name, err := service.projectName(name)
	if err != nil {
		return Project{}, err
	}
	return service.repository.RenameProject(ctx, ownerID, projectID, name, service.clock())
}

// Delete removes the project and cascades to its style guide, conventions and
// assets. Sessions keep their history and become unassigned; finished Task
// assets are never touched.
func (service *Service) Delete(ctx context.Context, ownerID, projectID string) error {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return ErrNotFound
	}
	return service.repository.DeleteProject(ctx, ownerID, projectID)
}

// StyleGuide reads the project's guide. A project without a saved guide reads
// as a disabled, empty guide rather than an error.
func (service *Service) StyleGuide(ctx context.Context, ownerID, projectID string) (StyleGuide, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return StyleGuide{}, ErrNotFound
	}
	guide, err := service.repository.StyleGuide(ctx, ownerID, projectID)
	if err != nil {
		return StyleGuide{}, err
	}
	guide.References = append([]Reference(nil), guide.References...)
	guide.Palette = append([]string(nil), guide.Palette...)
	return guide, nil
}

// SetStyleGuide replaces the whole guide after enforcing the Schema bounds and
// verifying every reference belongs to the owner.
func (service *Service) SetStyleGuide(ctx context.Context, ownerID, projectID string, guide StyleGuide) (StyleGuide, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return StyleGuide{}, ErrNotFound
	}
	if _, err := service.Get(ctx, ownerID, projectID); err != nil {
		return StyleGuide{}, err
	}
	text := strings.TrimSpace(guide.Text)
	if utf8.RuneCountInString(text) > service.style.MaximumTextCharacters {
		return StyleGuide{}, ErrStyleTextTooLong
	}
	if len(guide.Palette) > service.style.MaximumPaletteColors {
		return StyleGuide{}, ErrLimit
	}
	palette := make([]string, 0, len(guide.Palette))
	seenColors := make(map[string]bool, len(guide.Palette))
	for _, color := range guide.Palette {
		color = strings.TrimSpace(color)
		if !service.palettePattern.MatchString(color) {
			return StyleGuide{}, ErrPaletteInvalid
		}
		if seenColors[color] {
			continue
		}
		seenColors[color] = true
		palette = append(palette, color)
	}
	references, err := service.verifyReferences(ctx, ownerID, guide.References, service.style.MaximumReferences, true)
	if err != nil {
		return StyleGuide{}, err
	}
	stored := StyleGuide{
		ProjectID: projectID, Enabled: guide.Enabled, Text: text, Palette: palette,
		References: references, UpdatedAt: service.clock(),
	}
	return service.repository.SetStyleGuide(ctx, ownerID, projectID, stored)
}

// Conventions reads the project's production defaults. A project without
// saved conventions reads as empty defaults.
func (service *Service) Conventions(ctx context.Context, ownerID, projectID string) (Conventions, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return Conventions{}, ErrNotFound
	}
	conventions, err := service.repository.Conventions(ctx, ownerID, projectID)
	if err != nil {
		return Conventions{}, err
	}
	conventions.Texts = cloneTexts(conventions.Texts)
	return conventions, nil
}

// SetConventions replaces the whole convention set. Only declared brief
// fields and text names are accepted; unknown values are refused rather than
// silently dropped.
func (service *Service) SetConventions(ctx context.Context, ownerID, projectID string, conventions Conventions) (Conventions, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return Conventions{}, ErrNotFound
	}
	if _, err := service.Get(ctx, ownerID, projectID); err != nil {
		return Conventions{}, err
	}
	brief, err := service.validateBrief(conventions.Brief)
	if err != nil {
		return Conventions{}, err
	}
	texts, err := service.validateTexts(conventions.Texts)
	if err != nil {
		return Conventions{}, err
	}
	stored := Conventions{
		ProjectID: projectID, Brief: brief, Texts: texts, UpdatedAt: service.clock(),
	}
	return service.repository.SetConventions(ctx, ownerID, projectID, stored)
}

// ListAssets reads the project's assets in update order.
func (service *Service) ListAssets(ctx context.Context, ownerID, projectID string) ([]Asset, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return nil, ErrNotFound
	}
	assets, err := service.repository.ListAssets(ctx, ownerID, projectID, service.limits.MaximumListItems)
	if err != nil {
		return nil, err
	}
	for index := range assets {
		assets[index].Images = append([]Reference(nil), assets[index].Images...)
		assets[index].Models = append([]Reference(nil), assets[index].Models...)
	}
	return assets, nil
}

// GetAsset reads one project asset.
func (service *Service) GetAsset(ctx context.Context, ownerID, projectID, assetID string) (Asset, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(assetID) == "" {
		return Asset{}, ErrNotFound
	}
	asset, err := service.repository.GetAsset(ctx, ownerID, projectID, assetID)
	if err != nil {
		return Asset{}, err
	}
	asset.Images = append([]Reference(nil), asset.Images...)
	asset.Models = append([]Reference(nil), asset.Models...)
	return asset, nil
}

// CreateAsset verifies the input references and saves one project asset.
func (service *Service) CreateAsset(ctx context.Context, ownerID, projectID string, input AssetInput) (Asset, error) {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" {
		return Asset{}, ErrNotFound
	}
	if _, err := service.Get(ctx, ownerID, projectID); err != nil {
		return Asset{}, err
	}
	kind, name, description, err := service.assetFields(input.Kind, input.Name, input.Description)
	if err != nil {
		return Asset{}, err
	}
	images, err := service.verifyReferences(ctx, ownerID, input.Images, service.limits.MaximumAssetImages, true)
	if err != nil {
		return Asset{}, err
	}
	models, err := service.verifyReferences(ctx, ownerID, input.Models, service.limits.MaximumAssetModels, false)
	if err != nil {
		return Asset{}, err
	}
	id, err := service.newID("passet_")
	if err != nil {
		return Asset{}, err
	}
	now := service.clock()
	return service.repository.CreateAsset(ctx, projectID, Asset{
		ID: id, ProjectID: projectID, OwnerID: ownerID, Kind: kind, Name: name,
		Description: description, Images: images, Models: models, CreatedAt: now, UpdatedAt: now,
	}, service.limits.MaximumAssetsPerProject)
}

// AssetUpdate carries the optional members of a PATCH. Nil leaves a member
// unchanged; an explicit empty description clears it.
type AssetUpdate struct {
	Kind        *string
	Name        *string
	Description *string
}

// UpdateAsset applies a partial asset update after the owner and bounds are
// checked.
func (service *Service) UpdateAsset(ctx context.Context, ownerID, projectID, assetID string, update AssetUpdate) (Asset, error) {
	asset, err := service.GetAsset(ctx, ownerID, projectID, assetID)
	if err != nil {
		return Asset{}, err
	}
	kind, name, description := asset.Kind, asset.Name, asset.Description
	if update.Kind != nil {
		kind = *update.Kind
	}
	if update.Name != nil {
		name = *update.Name
	}
	if update.Description != nil {
		description = *update.Description
	}
	kind, name, description, err = service.assetFields(kind, name, description)
	if err != nil {
		return Asset{}, err
	}
	return service.repository.UpdateAsset(ctx, ownerID, projectID, assetID, kind, name, description, service.clock())
}

// DeleteAsset removes one project asset row. The referenced input/Task asset
// and every finished Task result stay untouched.
func (service *Service) DeleteAsset(ctx context.Context, ownerID, projectID, assetID string) error {
	if strings.TrimSpace(ownerID) == "" || strings.TrimSpace(projectID) == "" || strings.TrimSpace(assetID) == "" {
		return ErrNotFound
	}
	found, err := service.repository.DeleteAsset(ctx, ownerID, projectID, assetID)
	if err != nil {
		return err
	}
	if !found {
		return ErrNotFound
	}
	return nil
}

// Promote verifies one owner picture and creates a project asset that stores
// only its reference. Promotion always adds a new asset; it never rewrites an
// existing one.
func (service *Service) Promote(ctx context.Context, ownerID, projectID string, input PromoteInput) (Asset, error) {
	if !input.Source.Valid() {
		return Asset{}, ErrReferenceInvalid
	}
	return service.CreateAsset(ctx, ownerID, projectID, AssetInput{
		Kind: input.Kind, Name: input.Name, Description: input.Description,
		Images: []Reference{input.Source},
	})
}

func (service *Service) projectName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > service.limits.MaximumNameCharacters {
		return "", ErrNameTooLong
	}
	return name, nil
}

func (service *Service) assetFields(kind, name, description string) (string, string, string, error) {
	kind = strings.TrimSpace(kind)
	if !service.kinds[kind] {
		return "", "", "", ErrInvalid
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", "", "", ErrNameRequired
	}
	if utf8.RuneCountInString(name) > service.limits.MaximumAssetNameCharacters {
		return "", "", "", ErrNameTooLong
	}
	description = strings.TrimSpace(description)
	if utf8.RuneCountInString(description) > service.limits.MaximumAssetDescriptionCharacters {
		return "", "", "", ErrInvalid
	}
	return kind, name, description, nil
}

// verifyReferences bounds, verifies and de-duplicates a reference list. Input
// order is preserved. A picture and a model use different owner evidence.
func (service *Service) verifyReferences(
	ctx context.Context,
	ownerID string,
	references []Reference,
	maximum int,
	pictures bool,
) ([]Reference, error) {
	// Duplicates are the same reference; count and verify each unique source.
	unique := make([]Reference, 0, len(references))
	inputSeen := make(map[string]bool, len(references))
	for _, reference := range references {
		if !reference.Valid() {
			return nil, ErrReferenceInvalid
		}
		if inputSeen[reference.key()] {
			continue
		}
		inputSeen[reference.key()] = true
		unique = append(unique, reference)
	}
	if len(unique) > maximum {
		return nil, ErrReferenceLimit
	}
	result := make([]Reference, 0, len(unique))
	seen := make(map[string]bool, len(unique))
	for _, reference := range unique {
		if service.sources == nil {
			return nil, ErrReferenceInvalid
		}
		var verified Reference
		var err error
		if pictures {
			verified, err = service.sources.Picture(ctx, ownerID, reference)
		} else {
			verified, err = service.sources.Model(ctx, ownerID, reference)
		}
		if err != nil {
			if errors.Is(err, ErrReferenceInvalid) || errors.Is(err, ErrNotFound) {
				return nil, ErrReferenceInvalid
			}
			return nil, err
		}
		if seen[verified.key()] {
			continue
		}
		seen[verified.key()] = true
		result = append(result, verified)
	}
	return result, nil
}

func (service *Service) validateBrief(raw []byte) ([]byte, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return []byte("{}"), nil
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, ErrInvalid
	}
	for name, value := range values {
		field, exists := service.fields[name]
		if !exists {
			return nil, ErrInvalid
		}
		if err := service.validateBriefValue(field, value); err != nil {
			return nil, err
		}
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, ErrInvalid
	}
	return encoded, nil
}

func (service *Service) validateBriefValue(field catalog.ChatProductionBriefField, raw json.RawMessage) error {
	switch field.Type {
	case "profile", "image_profile":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalid
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return nil
		}
		declared := service.profiles
		if field.Type == "image_profile" {
			declared = service.imageProfiles
		}
		if !declared[value] {
			return ErrInvalid
		}
	case "text":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalid
		}
		if field.MaximumCharacters > 0 && utf8.RuneCountInString(value) > field.MaximumCharacters {
			return ErrInvalid
		}
	case "boolean":
		var value bool
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalid
		}
	case "integer":
		var value int
		if json.Unmarshal(raw, &value) != nil {
			return ErrInvalid
		}
		if field.Minimum != 0 && value < field.Minimum {
			return ErrInvalid
		}
		if field.Maximum != 0 && value > field.Maximum {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

func (service *Service) validateTexts(texts map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(texts))
	for name, value := range texts {
		declared, exists := service.texts[name]
		if !exists {
			return nil, ErrInvalid
		}
		value = strings.TrimSpace(value)
		if declared.MaximumCharacters > 0 && utf8.RuneCountInString(value) > declared.MaximumCharacters {
			return nil, ErrInvalid
		}
		result[name] = value
	}
	return result, nil
}

func cloneTexts(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
