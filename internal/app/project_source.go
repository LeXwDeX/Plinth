package app

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
	"github.com/LeXwDeX/Plinth/internal/project"
)

// projectChatSource adapts the owner-scoped project service to the narrow
// ports chat consumes (#240). It is the only place that turns stored project
// references into the canonical picture URLs a turn freezes: a project never
// stores bytes and the browser never builds these URLs for an agent handle.
type projectChatSource struct {
	service         *project.Service
	schema          catalog.ProjectSchema
	inputAssetRoute string
	taskAssetRoute  string
}

func newProjectChatSource(service *project.Service, schema catalog.ProjectSchema,
	routes catalog.CoreHTTPRoutesSchema) *projectChatSource {
	return &projectChatSource{
		service:         service,
		schema:          schema,
		inputAssetRoute: routes.AssetItem,
		taskAssetRoute:  routes.TaskAsset,
	}
}

func (source *projectChatSource) Exists(ctx context.Context, ownerID, projectID string) (bool, error) {
	return source.service.Exists(ctx, ownerID, projectID)
}

// Context reads one owner project and freezes the parts a turn needs. A
// missing or foreign project maps to chat.ErrProjectNotFound so the session
// boundary never leaks existence. Every reference is converted to its
// canonical server URL; a reference that cannot form one is skipped rather
// than handed to a generation tool as a guess.
func (source *projectChatSource) Context(ctx context.Context, ownerID, projectID string) (chat.ProjectContext, error) {
	value, err := source.service.Get(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, projectSourceError(err)
	}
	guide, err := source.service.StyleGuide(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, projectSourceError(err)
	}
	conventions, err := source.service.Conventions(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, projectSourceError(err)
	}
	assets, err := source.service.ListAssets(ctx, ownerID, projectID)
	if err != nil {
		return chat.ProjectContext{}, projectSourceError(err)
	}
	prefix := "@project"
	if source.schema.Agent != nil && strings.TrimSpace(source.schema.Agent.HandlePrefix) != "" {
		prefix = source.schema.Agent.HandlePrefix
	}
	result := chat.ProjectContext{
		ID:   value.ID,
		Name: value.Name,
		StyleGuide: chat.ProjectStyleGuideContext{
			Enabled:    guide.Enabled,
			Text:       guide.Text,
			References: source.pictureURLs(guide.References),
			Palette:    append([]string(nil), guide.Palette...),
		},
		Conventions: chat.ProjectConventionsContext{
			Brief: chat.DecodeProductionBrief(string(conventions.Brief)),
			Texts: cloneProjectTexts(conventions.Texts),
		},
		Assets: make([]chat.ProjectAssetContext, 0, len(assets)),
	}
	if result.Conventions.Brief.Empty() {
		result.Conventions.Brief = nil
	}
	for index, asset := range assets {
		models := make([]chat.ProjectModelContext, 0, len(asset.Models))
		for _, reference := range asset.Models {
			if reference.TaskID != "" && reference.AssetID != "" {
				models = append(models, chat.ProjectModelContext{
					TaskID: reference.TaskID, AssetID: reference.AssetID,
				})
			}
		}
		result.Assets = append(result.Assets, chat.ProjectAssetContext{
			Handle:      prefix + strconv.Itoa(index+1),
			Kind:        asset.Kind,
			Name:        asset.Name,
			Description: asset.Description,
			Images:      source.pictureURLs(asset.Images),
			Models:      models,
		})
	}
	return result, nil
}

// pictureURLs maps stored owner references to their canonical URLs, in order.
func (source *projectChatSource) pictureURLs(references []project.Reference) []string {
	urls := make([]string, 0, len(references))
	for _, reference := range references {
		switch {
		case reference.InputAssetID != "" && source.inputAssetRoute != "":
			urls = append(urls, strings.ReplaceAll(source.inputAssetRoute,
				"{assetID}", url.PathEscape(reference.InputAssetID)))
		case reference.TaskID != "" && reference.AssetID != "" && source.taskAssetRoute != "":
			urls = append(urls, strings.NewReplacer(
				"{taskID}", url.PathEscape(reference.TaskID),
				"{assetID}", url.PathEscape(reference.AssetID),
			).Replace(source.taskAssetRoute))
		}
	}
	return urls
}

func projectSourceError(err error) error {
	if errors.Is(err, project.ErrNotFound) {
		return chat.ErrProjectNotFound
	}
	return err
}

func cloneProjectTexts(texts map[string]string) map[string]string {
	if len(texts) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(texts))
	for name, value := range texts {
		cloned[name] = value
	}
	return cloned
}
