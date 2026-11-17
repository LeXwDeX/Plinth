package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/accountsettings"
	"github.com/LeXwDeX/Plinth/internal/approvalpolicy"
)

var _ accountsettings.Store = (*Repository)(nil)

// AccountSettings reads the person's stored settings (#330). No row means the
// person never saved anything; the caller applies the Schema defaults.
func (r *Repository) AccountSettings(ctx context.Context, ownerID string) (accountsettings.Settings, bool, error) {
	if ownerID == "" {
		return accountsettings.Settings{}, false, accountsettings.ErrInvalid
	}
	settings, found, err := accountSettingsOn(ctx, r.db, ownerID)
	if err != nil {
		return accountsettings.Settings{}, false, fmt.Errorf("read account settings: %w", err)
	}
	return settings, found, nil
}

// accountSettingsOn reads the row through a connection, a pool or an open
// transaction, so the grant decision reads the settings it decides under in
// its own write transaction.
func accountSettingsOn(ctx context.Context, conn interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, ownerID string) (accountsettings.Settings, bool, error) {
	var settings accountsettings.Settings
	var updated string
	err := conn.QueryRowContext(ctx, `SELECT list_step_mode, list_step_cap, mcp_step_mode, mcp_step_cap, version, updated_at
 FROM account_settings WHERE owner_id = ?`, ownerID).Scan(
		&settings.List.Mode, &settings.List.Cap, &settings.MCP.Mode, &settings.MCP.Cap, &settings.Version, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return accountsettings.Settings{}, false, nil
	}
	if err != nil {
		return accountsettings.Settings{}, false, err
	}
	settings.UpdatedAt, err = parseTime(updated)
	if err != nil {
		return accountsettings.Settings{}, false, err
	}
	return settings, true, nil
}

// PutAccountSettings stores the settings while the stored version is still the
// one the caller read. expected 0 inserts version 1 and conflicts when a row
// exists; a positive expected updates exactly that version to the next one.
// Each is a single statement, so two concurrent writers cannot both win.
func (r *Repository) PutAccountSettings(ctx context.Context, ownerID string, expected int64, next accountsettings.Settings, now time.Time) (accountsettings.Settings, error) {
	if ownerID == "" || expected < 0 || now.IsZero() ||
		next.List.Mode == "" || next.MCP.Mode == "" || next.List.Cap < 1 || next.MCP.Cap < 1 {
		return accountsettings.Settings{}, accountsettings.ErrInvalid
	}
	stamp := formatTime(now)
	var result sql.Result
	var err error
	if expected == 0 {
		result, err = r.db.ExecContext(ctx, `INSERT INTO account_settings
 (owner_id, list_step_mode, list_step_cap, mcp_step_mode, mcp_step_cap, version, updated_at)
 VALUES (?, ?, ?, ?, ?, 1, ?) ON CONFLICT(owner_id) DO NOTHING`,
			ownerID, next.List.Mode, next.List.Cap, next.MCP.Mode, next.MCP.Cap, stamp)
	} else {
		result, err = r.db.ExecContext(ctx, `UPDATE account_settings
 SET list_step_mode = ?, list_step_cap = ?, mcp_step_mode = ?, mcp_step_cap = ?, version = version + 1, updated_at = ?
 WHERE owner_id = ? AND version = ?`,
			next.List.Mode, next.List.Cap, next.MCP.Mode, next.MCP.Cap, stamp, ownerID, expected)
	}
	if err != nil {
		return accountsettings.Settings{}, fmt.Errorf("write account settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return accountsettings.Settings{}, err
	}
	if changed != 1 {
		return accountsettings.Settings{}, accountsettings.ErrConflict
	}
	return accountsettings.Settings{
		Version: expected + 1,
		List:    approvalpolicy.StepSetting{Mode: next.List.Mode, Cap: next.List.Cap},
		MCP:     approvalpolicy.StepSetting{Mode: next.MCP.Mode, Cap: next.MCP.Cap},
		// The stored instant has the precision the column keeps.
		UpdatedAt: now.UTC(),
	}, nil
}
