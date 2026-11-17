package mcppublic

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/LeXwDeX/Plinth/internal/catalog"
	"github.com/LeXwDeX/Plinth/internal/chat"
)

// The assets of the external MCP service (#332).
//
// A task must belong to one of the person's chat sessions, and the 3D steps are
// counted per asset, so an outside caller names the thing it works on (an
// "asset", say 木桶) and the service keeps one ordinary chat session for each
// name. The session is made the first time the name is used, is titled from the
// Schema's template and is marked as coming from MCP; after that the same name
// reaches the same session, so a later processing step lands next to the model
// it processes and a person sees the whole asset, with its result cards and
// downloads, in the session list. A call that names no asset uses the person's
// one default asset.
//
// The mapping lives in mcp_assets, which is also the marker: a session that is
// in it is an asset, whatever its title says now.

var (
	// ErrInvalidAssetName is a name that is empty once whitespace is folded, too
	// long, not valid text or holds a control character.
	ErrInvalidAssetName = errors.New("mcppublic: invalid asset name")
	// ErrProjectMismatch means the asset already exists in another project (or
	// in none) than the call names. The asset is never moved.
	ErrProjectMismatch = errors.New("mcppublic: the asset belongs to another project")
	// ErrAssetNotFound is a name that has no asset yet, for a call that may not
	// make one.
	ErrAssetNotFound = errors.New("mcppublic: no such asset")
)

// AssetSessions is what the asset service needs of the chat service.
type AssetSessions interface {
	// NewSessionInProject builds a session as CreateSessionInProject would, in
	// the owner's project when one is named (chat.ErrProjectNotFound for an
	// unknown or foreign one), without storing it.
	NewSessionInProject(ctx context.Context, ownerID, ownerName, title, projectID string) (chat.Session, error)
	Session(ctx context.Context, ownerID, sessionID string) (chat.Session, bool, error)
}

// AssetsConfig assembles Assets.
type AssetsConfig struct {
	// Schema is application.mcp_public.assets: the name bound and the titles.
	Schema   catalog.MCPPublicAssets
	Sessions AssetSessions
	Store    chat.MCPAssetStore
	// Localize turns the Schema's title copy into the request's language; nil
	// keeps it.
	Localize func(locale, text string) string
}

// Assets maps asset names to chat sessions.
type Assets struct {
	config AssetsConfig
}

// NewAssets builds the asset service; the sessions and the store are required.
func NewAssets(config AssetsConfig) (*Assets, error) {
	if config.Sessions == nil || config.Store == nil || config.Schema.NameMaxLength < 1 ||
		!strings.Contains(config.Schema.TitleTemplate, "{name}") || strings.TrimSpace(config.Schema.DefaultTitle) == "" {
		return nil, ErrInvalidConfig
	}
	if config.Localize == nil {
		config.Localize = func(_, text string) string { return text }
	}
	return &Assets{config: config}, nil
}

// NamedAsset is one asset of a person.
type NamedAsset struct {
	SessionID string
	// Name is the name the caller gave it, empty for the default asset.
	Name string
	// Title is the session's title in the person's session list.
	Title     string
	ProjectID string
	// Created is true when this call made the asset.
	Created bool
}

// NormalizeAssetName folds a caller's name into the display name and the key
// the mapping is looked up by: whitespace runs become one space, the key is
// lower-cased, so "Barrel" and " barrel  " are one asset. A name that is empty
// afterwards, longer than max characters, not valid UTF-8 or holding a control
// character other than whitespace is refused.
func NormalizeAssetName(name string, maximum int) (key, display string, err error) {
	if !utf8.ValidString(name) {
		return "", "", ErrInvalidAssetName
	}
	for _, character := range name {
		if unicode.IsControl(character) && !unicode.IsSpace(character) {
			return "", "", ErrInvalidAssetName
		}
	}
	display = strings.Join(strings.Fields(name), " ")
	if display == "" || utf8.RuneCountInString(display) > maximum {
		return "", "", ErrInvalidAssetName
	}
	return strings.ToLower(display), display, nil
}

// defaultProjectKeyPrefix keys the default asset of a project.
const defaultProjectKeyPrefix = "Project:"

// AssetRequest is what a call names. Name empty is the default asset: one
// outside any project, or one per project the call names.
type AssetRequest struct {
	OwnerID   string
	OwnerName string
	Locale    string
	Name      string
	// ProjectID, when set, puts a new asset in that project of the owner and is
	// checked against an existing one.
	ProjectID string
	// FindOnly makes the call a lookup: a name with no asset is
	// ErrAssetNotFound instead of a new session. A step that continues an
	// asset's earlier work (processing a model it made) names one that exists.
	FindOnly bool
}

// Resolve returns the asset the call names and makes it the first time: the
// session is made in the project when one is named (inheriting its style guide
// and conventions as any session made there does) and stored together with the
// mapping, so two calls that name the same asset at once get one session. An
// existing asset in another project than the one named is ErrProjectMismatch;
// an unknown or foreign project is chat.ErrProjectNotFound.
func (a *Assets) Resolve(ctx context.Context, request AssetRequest) (NamedAsset, error) {
	key, display := "", ""
	if strings.TrimSpace(request.Name) != "" {
		var err error
		if key, display, err = NormalizeAssetName(request.Name, a.config.Schema.NameMaxLength); err != nil {
			return NamedAsset{}, err
		}
	}
	project := strings.TrimSpace(request.ProjectID)
	if key == "" && project != "" {
		// The default asset is one per project: a caller that works in two
		// projects without naming assets gets one asset in each. The key holds
		// an upper-case letter, which no normalized (lower-cased) name can.
		key = defaultProjectKeyPrefix + project
	}
	existing, found, err := a.config.Store.FindMCPAsset(ctx, request.OwnerID, key)
	if err != nil {
		return NamedAsset{}, err
	}
	if found {
		return a.existing(ctx, request.OwnerID, existing, project)
	}
	if request.FindOnly {
		return NamedAsset{}, ErrAssetNotFound
	}
	session, err := a.config.Sessions.NewSessionInProject(ctx, request.OwnerID, request.OwnerName, a.title(request.Locale, display), project)
	if err != nil {
		return NamedAsset{}, err
	}
	stored, created, err := a.config.Store.CreateMCPAsset(ctx, session, key, display)
	if err != nil {
		return NamedAsset{}, err
	}
	if !created {
		// A call that named the same asset won the race: its session is the
		// asset, and the one built here was never stored.
		return a.existing(ctx, request.OwnerID, stored, project)
	}
	return NamedAsset{SessionID: stored.SessionID, Name: stored.Name, Title: session.Title, ProjectID: session.ProjectID, Created: true}, nil
}

func (a *Assets) existing(ctx context.Context, owner string, asset chat.MCPAsset, project string) (NamedAsset, error) {
	session, found, err := a.config.Sessions.Session(ctx, owner, asset.SessionID)
	if err != nil {
		return NamedAsset{}, err
	}
	if !found {
		return NamedAsset{}, errors.New("mcppublic: the session of an asset is gone")
	}
	if project != "" && session.ProjectID != project {
		return NamedAsset{}, ErrProjectMismatch
	}
	return NamedAsset{SessionID: asset.SessionID, Name: asset.Name, Title: session.Title, ProjectID: session.ProjectID}, nil
}

func (a *Assets) title(locale, display string) string {
	if display == "" {
		return a.config.Localize(locale, a.config.Schema.DefaultTitle)
	}
	return strings.ReplaceAll(a.config.Localize(locale, a.config.Schema.TitleTemplate), "{name}", display)
}

// Describe reads an asset by its session ID; found is false for a session that
// is no asset of the owner.
func (a *Assets) Describe(ctx context.Context, owner, sessionID string) (NamedAsset, bool, error) {
	marked, err := a.config.Store.MCPAssetsOfSessions(ctx, owner, []string{sessionID})
	if err != nil {
		return NamedAsset{}, false, err
	}
	asset, isAsset := marked[sessionID]
	if !isAsset {
		return NamedAsset{}, false, nil
	}
	session, found, err := a.config.Sessions.Session(ctx, owner, sessionID)
	if err != nil || !found {
		return NamedAsset{}, false, err
	}
	return NamedAsset{SessionID: sessionID, Name: asset.Name, Title: session.Title, ProjectID: session.ProjectID}, true, nil
}

// Marked says which of the sessions are assets of the owner.
func (a *Assets) Marked(ctx context.Context, owner string, sessionIDs []string) (map[string]bool, error) {
	assets, err := a.config.Store.MCPAssetsOfSessions(ctx, owner, sessionIDs)
	if err != nil {
		return nil, err
	}
	marked := make(map[string]bool, len(assets))
	for id := range assets {
		marked[id] = true
	}
	return marked, nil
}
