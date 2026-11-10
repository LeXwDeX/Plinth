package project_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/project"
)

var testIDCounter atomic.Uint64

type memoryRepository struct {
	projects    map[string]project.Project
	guides      map[string]project.StyleGuide
	conventions map[string]project.Conventions
	assets      map[string]project.Asset
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		projects:    map[string]project.Project{},
		guides:      map[string]project.StyleGuide{},
		conventions: map[string]project.Conventions{},
		assets:      map[string]project.Asset{},
	}
}

func (r *memoryRepository) owns(ownerID, projectID string) bool {
	value, exists := r.projects[projectID]
	return exists && value.OwnerID == ownerID
}

func (r *memoryRepository) CreateProject(_ context.Context, value project.Project, maximum int) (project.Project, error) {
	count := 0
	for _, existing := range r.projects {
		if existing.OwnerID == value.OwnerID {
			count++
		}
	}
	if count >= maximum {
		return project.Project{}, project.ErrLimit
	}
	r.projects[value.ID] = value
	return value, nil
}

func (r *memoryRepository) ListProjects(_ context.Context, ownerID string, _ int) ([]project.Project, error) {
	var result []project.Project
	for _, value := range r.projects {
		if value.OwnerID == ownerID {
			result = append(result, value)
		}
	}
	return result, nil
}

func (r *memoryRepository) GetProject(_ context.Context, ownerID, projectID string) (project.Project, error) {
	value, exists := r.projects[projectID]
	if !exists || value.OwnerID != ownerID {
		return project.Project{}, project.ErrNotFound
	}
	return value, nil
}

func (r *memoryRepository) RenameProject(_ context.Context, ownerID, projectID, name string, at time.Time) (project.Project, error) {
	value, exists := r.projects[projectID]
	if !exists || value.OwnerID != ownerID {
		return project.Project{}, project.ErrNotFound
	}
	value.Name, value.UpdatedAt = name, at
	r.projects[projectID] = value
	return value, nil
}

func (r *memoryRepository) DeleteProject(_ context.Context, ownerID, projectID string) error {
	if !r.owns(ownerID, projectID) {
		return project.ErrNotFound
	}
	delete(r.projects, projectID)
	delete(r.guides, projectID)
	delete(r.conventions, projectID)
	for id, asset := range r.assets {
		if asset.ProjectID == projectID {
			delete(r.assets, id)
		}
	}
	return nil
}

func (r *memoryRepository) StyleGuide(_ context.Context, ownerID, projectID string) (project.StyleGuide, error) {
	if !r.owns(ownerID, projectID) {
		return project.StyleGuide{}, project.ErrNotFound
	}
	return r.guides[projectID], nil
}

func (r *memoryRepository) SetStyleGuide(_ context.Context, ownerID, projectID string, guide project.StyleGuide) (project.StyleGuide, error) {
	if !r.owns(ownerID, projectID) {
		return project.StyleGuide{}, project.ErrNotFound
	}
	guide.ProjectID = projectID
	r.guides[projectID] = guide
	return guide, nil
}

func (r *memoryRepository) Conventions(_ context.Context, ownerID, projectID string) (project.Conventions, error) {
	if !r.owns(ownerID, projectID) {
		return project.Conventions{}, project.ErrNotFound
	}
	return r.conventions[projectID], nil
}

func (r *memoryRepository) SetConventions(_ context.Context, ownerID, projectID string, conventions project.Conventions) (project.Conventions, error) {
	if !r.owns(ownerID, projectID) {
		return project.Conventions{}, project.ErrNotFound
	}
	conventions.ProjectID = projectID
	r.conventions[projectID] = conventions
	return conventions, nil
}

func (r *memoryRepository) ListAssets(_ context.Context, ownerID, projectID string, _ int) ([]project.Asset, error) {
	if !r.owns(ownerID, projectID) {
		return nil, project.ErrNotFound
	}
	var result []project.Asset
	for _, value := range r.assets {
		if value.ProjectID == projectID {
			result = append(result, value)
		}
	}
	return result, nil
}

func (r *memoryRepository) CreateAsset(_ context.Context, projectID string, value project.Asset, maximum int) (project.Asset, error) {
	if !r.owns(value.OwnerID, projectID) {
		return project.Asset{}, project.ErrNotFound
	}
	count := 0
	for _, existing := range r.assets {
		if existing.ProjectID == projectID {
			count++
		}
	}
	if count >= maximum {
		return project.Asset{}, project.ErrLimit
	}
	value.ProjectID = projectID
	r.assets[value.ID] = value
	return value, nil
}

func (r *memoryRepository) GetAsset(_ context.Context, ownerID, projectID, assetID string) (project.Asset, error) {
	value, exists := r.assets[assetID]
	if !exists || value.ProjectID != projectID || !r.owns(ownerID, projectID) {
		return project.Asset{}, project.ErrNotFound
	}
	return value, nil
}

func (r *memoryRepository) UpdateAsset(_ context.Context, ownerID, projectID, assetID, kind, name, description string, at time.Time) (project.Asset, error) {
	value, exists := r.assets[assetID]
	if !exists || value.ProjectID != projectID || !r.owns(ownerID, projectID) {
		return project.Asset{}, project.ErrNotFound
	}
	value.Kind, value.Name, value.Description, value.UpdatedAt = kind, name, description, at
	r.assets[assetID] = value
	return value, nil
}

func (r *memoryRepository) DeleteAsset(_ context.Context, ownerID, projectID, assetID string) (bool, error) {
	value, exists := r.assets[assetID]
	if !exists || value.ProjectID != projectID || value.OwnerID != ownerID {
		return false, nil
	}
	delete(r.assets, assetID)
	return true, nil
}

type sourceVerifier struct {
	pictures map[string]bool
	models   map[string]bool
}

func sourceKey(reference project.Reference) string {
	if reference.InputAssetID != "" {
		return "input:" + reference.InputAssetID
	}
	return "task:" + reference.TaskID + "/" + reference.AssetID
}

func (v sourceVerifier) Picture(_ context.Context, _ string, reference project.Reference) (project.Reference, error) {
	if !v.pictures[sourceKey(reference)] {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	return reference, nil
}

func (v sourceVerifier) Model(_ context.Context, _ string, reference project.Reference) (project.Reference, error) {
	if !v.models[sourceKey(reference)] {
		return project.Reference{}, project.ErrReferenceInvalid
	}
	return reference, nil
}

func testSchema() catalog.ProjectSchema {
	return catalog.ProjectSchema{
		Limits: catalog.ProjectLimitsSchema{
			MaximumProjectsPerOwner:           2,
			MaximumNameCharacters:             8,
			MaximumAssetsPerProject:           3,
			MaximumAssetNameCharacters:        8,
			MaximumAssetDescriptionCharacters: 20,
			MaximumAssetImages:                2,
			MaximumAssetModels:                1,
			MaximumListItems:                  10,
		},
		StyleGuide: catalog.ProjectStyleGuideSchema{
			MaximumTextCharacters: 10,
			MaximumReferences:     2,
			MaximumPaletteColors:  2,
			PaletteColorPattern:   "^#[0-9a-f]{6}$",
		},
		Assets: catalog.ProjectAssetsSchema{Kinds: []catalog.ProjectAssetKindSchema{
			{ID: "character", LabelKey: "ui.project.kind.character"},
			{ID: "prop", LabelKey: "ui.project.kind.prop"},
		}},
		Conventions: catalog.ProjectConventionsSchema{
			Fields: []catalog.ChatProductionBriefField{
				{Name: "purpose", Type: "profile", Label: "用途"},
				{Name: "style", Type: "text", Label: "风格", MaximumCharacters: 5},
				{Name: "triangle_budget", Type: "integer", Label: "预算", Minimum: 100, Maximum: 1000},
			},
			Texts: []catalog.ProjectConventionTextSchema{
				{Name: "naming", LabelKey: "ui.project.convention.naming", MaximumCharacters: 5},
			},
		},
	}
}

func newService(t *testing.T, repository project.Repository, sources project.AssetSource) *project.Service {
	t.Helper()
	now := time.Date(2026, 11, 11, 10, 0, 0, 0, time.UTC)
	service, err := project.New(repository, project.Config{
		Schema:  testSchema(),
		Sources: sources,
		Clock:   func() time.Time { return now },
		NewID: func(prefix string) (string, error) {
			return prefix + "fixed" + strconv.FormatUint(testIDCounter.Add(1), 10), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestCreateAndRenameAreOwnerScopedAndBounded(t *testing.T) {
	ctx := context.Background()
	service := newService(t, newMemoryRepository(), sourceVerifier{})
	first, err := service.Create(ctx, "owner-a", "  Star  ")
	if err != nil || first.Name != "Star" || first.OwnerID != "owner-a" {
		t.Fatalf("create = %+v err=%v", first, err)
	}
	if _, err := service.Create(ctx, "owner-a", "second"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, "owner-a", "third"); !errors.Is(err, project.ErrLimit) {
		t.Fatalf("third project err = %v", err)
	}
	if _, err := service.Create(ctx, "owner-a", "  "); !errors.Is(err, project.ErrNameRequired) {
		t.Fatalf("empty name err = %v", err)
	}
	if _, err := service.Create(ctx, "owner-a", strings.Repeat("x", 9)); !errors.Is(err, project.ErrNameTooLong) {
		t.Fatalf("long name err = %v", err)
	}
	if ok, err := service.Exists(ctx, "owner-b", first.ID); err != nil || ok {
		t.Fatalf("foreign exists = %v %v", ok, err)
	}
	if _, err := service.Get(ctx, "owner-b", first.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign get err = %v", err)
	}
	if _, err := service.Rename(ctx, "owner-b", first.ID, "Taken"); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign rename err = %v", err)
	}
	if renamed, err := service.Rename(ctx, "owner-a", first.ID, "Renamed"); err != nil || renamed.Name != "Renamed" {
		t.Fatalf("rename = %+v err=%v", renamed, err)
	}
	if err := service.Delete(ctx, "owner-b", first.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
	if err := service.Delete(ctx, "owner-a", first.ID); err != nil {
		t.Fatal(err)
	}
	if ok, _ := service.Exists(ctx, "owner-a", first.ID); ok {
		t.Fatal("deleted project still exists")
	}
}

func TestStyleGuideAndConventionsValidateAndPersist(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	sources := sourceVerifier{pictures: map[string]bool{
		"input:picture-1":     true,
		"task:task-1/asset-1": true,
	}}
	service := newService(t, repository, sources)
	value, err := service.Create(ctx, "owner-a", "Star")
	if err != nil {
		t.Fatal(err)
	}
	guide, err := service.StyleGuide(ctx, "owner-a", value.ID)
	if err != nil || guide.Enabled || len(guide.References) != 0 {
		t.Fatalf("default guide = %+v err=%v", guide, err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-a", value.ID, project.StyleGuide{Text: strings.Repeat("x", 11)}); !errors.Is(err, project.ErrStyleTextTooLong) {
		t.Fatalf("long text err = %v", err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-a", value.ID, project.StyleGuide{Palette: []string{"#GGGGGG"}}); !errors.Is(err, project.ErrPaletteInvalid) {
		t.Fatalf("palette err = %v", err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-a", value.ID, project.StyleGuide{References: []project.Reference{
		{InputAssetID: "picture-1"}, {TaskID: "task-1", AssetID: "asset-1"}, {InputAssetID: "picture-1"},
	}}); err != nil {
		t.Fatalf("duplicate references should not exceed the limit: %v", err)
	}
	stored, err := service.StyleGuide(ctx, "owner-a", value.ID)
	if err != nil || len(stored.References) != 2 || stored.References[0].InputAssetID != "picture-1" || stored.References[1].AssetID != "asset-1" {
		t.Fatalf("stored guide = %+v err=%v", stored, err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-a", value.ID, project.StyleGuide{References: []project.Reference{{InputAssetID: "unknown"}}}); !errors.Is(err, project.ErrReferenceInvalid) {
		t.Fatalf("unknown ref err = %v", err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-a", value.ID, project.StyleGuide{References: []project.Reference{
		{InputAssetID: "picture-1"}, {TaskID: "task-1", AssetID: "asset-1"}, {TaskID: "task-3", AssetID: "asset-3"},
	}}); !errors.Is(err, project.ErrReferenceLimit) {
		t.Fatalf("over-limit refs err = %v", err)
	}
	if _, err := service.SetStyleGuide(ctx, "owner-b", value.ID, project.StyleGuide{}); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign guide err = %v", err)
	}

	if _, err := service.SetConventions(ctx, "owner-a", value.ID, project.Conventions{
		Brief: []byte(`{"unknown":"x"}`),
	}); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("unknown field err = %v", err)
	}
	if _, err := service.SetConventions(ctx, "owner-a", value.ID, project.Conventions{
		Brief: []byte(`{"style":"toolong"}`),
	}); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("long convention text err = %v", err)
	}
	if _, err := service.SetConventions(ctx, "owner-a", value.ID, project.Conventions{
		Brief: []byte(`{"purpose":"game_character","style":"ink","triangle_budget":500}`),
		Texts: map[string]string{"naming": "hero_"},
	}); err != nil {
		t.Fatal(err)
	}
	conventions, err := service.Conventions(ctx, "owner-a", value.ID)
	if err != nil || !strings.Contains(string(conventions.Brief), "game_character") || conventions.Texts["naming"] != "hero_" {
		t.Fatalf("conventions = %+v err=%v", conventions, err)
	}
}

func TestAssetsPromoteAndDeleteKeepReferencesOnly(t *testing.T) {
	ctx := context.Background()
	repository := newMemoryRepository()
	sources := sourceVerifier{
		pictures: map[string]bool{"input:picture-1": true, "task:task-1/asset-1": true},
		models:   map[string]bool{"task:task-2/model-1": true},
	}
	service := newService(t, repository, sources)
	value, err := service.Create(ctx, "owner-a", "Star")
	if err != nil {
		t.Fatal(err)
	}
	asset, err := service.CreateAsset(ctx, "owner-a", value.ID, project.AssetInput{
		Kind: "character", Name: "Hero", Images: []project.Reference{{InputAssetID: "picture-1"}},
		Models: []project.Reference{{TaskID: "task-2", AssetID: "model-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if asset.ProjectID != value.ID || len(asset.Images) != 1 || len(asset.Models) != 1 {
		t.Fatalf("asset = %+v", asset)
	}
	if _, err := service.CreateAsset(ctx, "owner-a", value.ID, project.AssetInput{
		Kind: "character", Name: "Second", Images: []project.Reference{{InputAssetID: "picture-1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateAsset(ctx, "owner-a", value.ID, project.AssetInput{
		Kind: "unknown", Name: "Bad",
	}); !errors.Is(err, project.ErrInvalid) {
		t.Fatalf("kind err = %v", err)
	}
	if _, err := service.CreateAsset(ctx, "owner-b", value.ID, project.AssetInput{
		Kind: "character", Name: "Foreign",
	}); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign create err = %v", err)
	}
	name := "Renamed"
	updated, err := service.UpdateAsset(ctx, "owner-a", value.ID, asset.ID, project.AssetUpdate{Name: &name})
	if err != nil || updated.Name != "Renamed" {
		t.Fatalf("update = %+v err=%v", updated, err)
	}
	promoted, err := service.Promote(ctx, "owner-a", value.ID, project.PromoteInput{
		Kind: "prop", Name: "Chair", Source: project.Reference{TaskID: "task-1", AssetID: "asset-1"},
	})
	if err != nil || len(promoted.Images) != 1 || promoted.Images[0].TaskID != "task-1" {
		t.Fatalf("promote = %+v err=%v", promoted, err)
	}
	if _, err := service.CreateAsset(ctx, "owner-a", value.ID, project.AssetInput{
		Kind: "character", Name: "Fourth",
	}); !errors.Is(err, project.ErrLimit) {
		t.Fatalf("asset limit err = %v", err)
	}
	if _, err := service.Promote(ctx, "owner-a", value.ID, project.PromoteInput{
		Kind: "prop", Name: "Stolen", Source: project.Reference{TaskID: "task-9", AssetID: "asset-9"},
	}); !errors.Is(err, project.ErrReferenceInvalid) {
		t.Fatalf("foreign promote err = %v", err)
	}
	if err := service.DeleteAsset(ctx, "owner-a", value.ID, asset.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.GetAsset(ctx, "owner-a", value.ID, asset.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("deleted asset get err = %v", err)
	}
	if err := service.DeleteAsset(ctx, "owner-b", value.ID, promoted.ID); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("foreign delete err = %v", err)
	}
}
