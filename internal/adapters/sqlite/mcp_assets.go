package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat"
)

var _ chat.MCPAssetStore = (*Repository)(nil)

// FindMCPAsset reads the owner's asset of a normalized name (#332).
func (r *Repository) FindMCPAsset(ctx context.Context, owner, nameKey string) (chat.MCPAsset, bool, error) {
	if owner == "" {
		return chat.MCPAsset{}, false, errors.New("sqlite: mcp asset without an owner")
	}
	row := r.db.QueryRowContext(ctx, `SELECT owner_id, name_key, name, session_id, created_at FROM mcp_assets
 WHERE owner_id = ? AND name_key = ?`, owner, nameKey)
	asset, err := scanMCPAsset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return chat.MCPAsset{}, false, nil
	}
	if err != nil {
		return chat.MCPAsset{}, false, err
	}
	return asset, true, nil
}

// CreateMCPAsset stores the session and its asset mapping in one transaction.
// The transaction takes the write lock first (BEGIN IMMEDIATE), so two callers
// that create the same name at once are serialized: the second finds the
// first's mapping and stores nothing, and no session is left without its
// mapping or the other way round.
func (r *Repository) CreateMCPAsset(ctx context.Context, session chat.Session, nameKey, name string) (chat.MCPAsset, bool, error) {
	if session.ID == "" || session.OwnerID == "" {
		return chat.MCPAsset{}, false, errors.New("sqlite: mcp asset without a session or an owner")
	}
	conn, err := r.grantWriteConn(ctx)
	if err != nil {
		return chat.MCPAsset{}, false, err
	}
	defer conn.Close()
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()
	existing, err := scanMCPAsset(conn.QueryRowContext(ctx, `SELECT owner_id, name_key, name, session_id, created_at FROM mcp_assets
 WHERE owner_id = ? AND name_key = ?`, session.OwnerID, nameKey))
	switch {
	case err == nil:
		return existing, false, nil
	case !errors.Is(err, sql.ErrNoRows):
		return chat.MCPAsset{}, false, err
	}
	if err := insertChatSession(ctx, conn, session); err != nil {
		return chat.MCPAsset{}, false, err
	}
	asset := chat.MCPAsset{OwnerID: session.OwnerID, NameKey: nameKey, Name: name, SessionID: session.ID, CreatedAt: session.CreatedAt}
	if _, err := conn.ExecContext(ctx, `INSERT INTO mcp_assets (owner_id, name_key, name, session_id, created_at)
 VALUES (?, ?, ?, ?, ?)`, asset.OwnerID, asset.NameKey, asset.Name, asset.SessionID, formatTime(asset.CreatedAt)); err != nil {
		return chat.MCPAsset{}, false, fmt.Errorf("create mcp asset: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return chat.MCPAsset{}, false, err
	}
	committed = true
	return asset, true, nil
}

// MCPAssetsOfSessions returns the sessions among ids that are assets of the
// owner, by session ID.
func (r *Repository) MCPAssetsOfSessions(ctx context.Context, owner string, sessionIDs []string) (map[string]chat.MCPAsset, error) {
	found := make(map[string]chat.MCPAsset, len(sessionIDs))
	if owner == "" || len(sessionIDs) == 0 {
		return found, nil
	}
	args := []any{owner}
	placeholders := make([]string, 0, len(sessionIDs))
	for _, id := range sessionIDs {
		if id == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(placeholders) == 0 {
		return found, nil
	}
	rows, err := r.db.QueryContext(ctx, `SELECT owner_id, name_key, name, session_id, created_at FROM mcp_assets
 WHERE owner_id = ? AND session_id IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		asset, err := scanMCPAsset(rows)
		if err != nil {
			return nil, err
		}
		found[asset.SessionID] = asset
	}
	return found, rows.Err()
}

func scanMCPAsset(row interface{ Scan(...any) error }) (chat.MCPAsset, error) {
	var asset chat.MCPAsset
	var created string
	if err := row.Scan(&asset.OwnerID, &asset.NameKey, &asset.Name, &asset.SessionID, &created); err != nil {
		return chat.MCPAsset{}, err
	}
	parsed, err := time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return chat.MCPAsset{}, err
	}
	asset.CreatedAt = parsed.UTC()
	return asset, nil
}
