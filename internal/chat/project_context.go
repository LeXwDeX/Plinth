package chat

import (
	"context"
	"errors"
	"strings"
)

// ProjectContext is the project snapshot frozen when a turn starts (#240).
// The generation tools never read the mutable project rows for a running turn:
// style references, conventions and the @projectN handles all come from here.
//
// Pictures are canonical server URLs, not asset IDs: a project asset may be an
// owner upload or a finished Task picture, and only the server builds either
// URL. The references resolver accepts a Task picture outside this session only
// because it is registered in this project.
type ProjectContext struct {
	ID          string
	Name        string
	StyleGuide  ProjectStyleGuideContext
	Conventions ProjectConventionsContext
	Assets      []ProjectAssetContext
}

// ProjectStyleGuideContext is the project's reusable 2D style guide. Disabled
// or empty guides inject nothing.
type ProjectStyleGuideContext struct {
	Enabled    bool
	Text       string
	References []string
}

// ProjectConventionsContext is the project's production defaults. Brief uses
// the same declared fields as the session production brief; Texts are the
// free-form naming/coordinate conventions.
type ProjectConventionsContext struct {
	Brief ProductionBrief
	Texts map[string]string
}

// ProjectAssetContext is one project asset as the Agent sees it: a stable
// handle plus the metadata, with picture URLs it may reuse. Models are stored
// references only in this version and never become a paid step's source.
type ProjectAssetContext struct {
	Handle      string
	Kind        string
	Name        string
	Description string
	Images      []string
	Models      []ProjectModelContext
}

// ProjectModelContext is one saved 3D model reference of a project asset.
type ProjectModelContext struct {
	TaskID  string
	AssetID string
}

// ProjectStyleSource reports whether the effective lock really carries the
// project style (text or pictures).
func (context *ProjectContext) ProjectStyleSource() bool {
	if context == nil {
		return false
	}
	return context.StyleGuide.Enabled &&
		(strings.TrimSpace(context.StyleGuide.Text) != "" || len(context.StyleGuide.References) > 0)
}

// ProjectAssetByHandle finds the frozen asset a @projectN token names.
func (context *ProjectContext) ProjectAssetByHandle(handle string) *ProjectAssetContext {
	if context == nil {
		return nil
	}
	for index := range context.Assets {
		if context.Assets[index].Handle == handle {
			return &context.Assets[index]
		}
	}
	return nil
}

// ProjectStylePictureURLs returns the project's style guide pictures, in
// their stored order.
func (lock StyleLock) ProjectStylePictureURLs() []string {
	return append([]string(nil), lock.ProjectReferences...)
}

// effectiveStyleLock resolves the three-state source against the frozen
// project snapshot. An empty historical source reads as project: every new
// session stores an explicit source, and an upgraded session with an enabled
// lock was backfilled to session by migration 0090.
func effectiveStyleLock(stored StyleLock, projectContext *ProjectContext) StyleLock {
	switch stored.Source {
	case StyleLockSourceOff:
		return StyleLock{Source: StyleLockSourceOff}
	case StyleLockSourceSession:
		stored.Source = StyleLockSourceSession
		return stored
	default:
		if projectContext == nil {
			// A session outside any project has no guide to inherit. Its own
			// lock keeps working as before; there is no second table to merge.
			stored.Source = StyleLockSourceSession
			return stored
		}
		effective := StyleLock{Source: StyleLockSourceProject}
		if projectContext.StyleGuide.Enabled {
			effective.Enabled = true
			effective.Text = strings.TrimSpace(projectContext.StyleGuide.Text)
			effective.ProjectReferences = append([]string(nil), projectContext.StyleGuide.References...)
			effective.ProjectName = projectContext.Name
			effective.ProjectID = projectContext.ID
		}
		return effective
	}
}

// EffectiveStyleLock returns the lock a new turn of the owner's session would
// freeze. Direct prepare uses it so a project session's direct task inherits
// exactly the chat turn's style source (#240).
func (service *Service) EffectiveStyleLock(ctx context.Context, ownerID, sessionID string) (StyleLock, error) {
	stored, found, err := service.repository.StyleLock(ctx, ownerID, sessionID)
	if err != nil {
		return StyleLock{}, err
	}
	if !found {
		return StyleLock{}, ErrSessionNotFound
	}
	projectID, found, err := service.repository.SessionProject(ctx, ownerID, sessionID)
	if err != nil {
		return StyleLock{}, err
	}
	if !found {
		return StyleLock{}, ErrSessionNotFound
	}
	var projectContext *ProjectContext
	if projectID != "" && service.projects != nil {
		frozen, projectErr := service.projects.Context(ctx, ownerID, projectID)
		switch {
		case projectErr == nil:
			projectContext = &frozen
		case errors.Is(projectErr, ErrProjectNotFound):
		default:
			return StyleLock{}, projectErr
		}
	}
	return effectiveStyleLock(stored, projectContext), nil
}
