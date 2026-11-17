package chat

import (
	"context"
	"time"
)

// MCPAsset is one asset of the external MCP service (#332): the chat session an
// outside caller reaches by a name. The mapping is also the marker that says a
// session came from MCP, so a person can tell those sessions apart in the
// session list; the sessions themselves are ordinary ones the person can open.
type MCPAsset struct {
	OwnerID string
	// NameKey is the normalized name the owner's calls resolve to this session
	// by; empty for the owner's default asset (the calls that name none).
	NameKey string
	// Name is the name as the caller gave it, whitespace folded; empty for the
	// default asset.
	Name      string
	SessionID string
	CreatedAt time.Time
}

// MCPAssetStore is the repository extension that keeps the asset mapping. It
// is optional for a chat Repository: only the external MCP service uses it.
type MCPAssetStore interface {
	// FindMCPAsset reads the owner's asset of a normalized name.
	FindMCPAsset(ctx context.Context, owner, nameKey string) (MCPAsset, bool, error)
	// CreateMCPAsset stores a new session and its asset mapping in one
	// transaction, unless the owner already has an asset of that name: then
	// nothing is stored and the existing asset is returned with created false.
	// Two callers creating the same name at once therefore get one session.
	CreateMCPAsset(ctx context.Context, session Session, nameKey, name string) (asset MCPAsset, created bool, err error)
	// MCPAssetsOfSessions returns, by session ID, those of the sessions that are
	// assets of the owner. A session of another owner or a plain session is
	// left out.
	MCPAssetsOfSessions(ctx context.Context, owner string, sessionIDs []string) (map[string]MCPAsset, error)
}
