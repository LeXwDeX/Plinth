package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/LeXwDeX/Plinth/internal/llmsettings"
)

// Administrator LLM settings and connection tests (#322, migration 0066).
// The settings hold connection IDs, switches and levels only; no address,
// key or upstream error text is ever stored here.

const llmSettingsColumns = `revision, settings_json, actor_id, source, created_at`

func scanLLMSettings(row interface{ Scan(...any) error }) (llmsettings.Version, error) {
	var version llmsettings.Version
	var revision int64
	var encoded, created string
	err := row.Scan(&revision, &encoded, &version.ActorID, &version.Source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return version, llmsettings.ErrNotFound
	}
	if err != nil {
		return version, err
	}
	version.Revision = strconv.FormatInt(revision, 10)
	if version.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return version, err
	}
	return version, json.Unmarshal([]byte(encoded), &version.Settings)
}

func (r *Repository) LatestLLMSettings(ctx context.Context) (llmsettings.Version, error) {
	return scanLLMSettings(r.db.QueryRowContext(ctx, `SELECT `+llmSettingsColumns+` FROM llm_settings_versions ORDER BY revision DESC LIMIT 1`))
}

func (r *Repository) LLMSettingsVersion(ctx context.Context, revision string) (llmsettings.Version, error) {
	number, err := strconv.ParseInt(revision, 10, 64)
	if err != nil {
		return llmsettings.Version{}, llmsettings.ErrNotFound
	}
	return scanLLMSettings(r.db.QueryRowContext(ctx, `SELECT `+llmSettingsColumns+` FROM llm_settings_versions WHERE revision = ?`, number))
}

func (r *Repository) LLMSettingsHistory(ctx context.Context, limit int) ([]llmsettings.Version, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+llmSettingsColumns+` FROM llm_settings_versions ORDER BY revision DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read llm settings history: %w", err)
	}
	defer rows.Close()
	result := []llmsettings.Version{}
	for rows.Next() {
		version, err := scanLLMSettings(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, version)
	}
	return result, rows.Err()
}

// AppendLLMSettings inserts the next version only while expected is still the
// newest revision; a concurrent publish makes it ErrConflict.
func (r *Repository) AppendLLMSettings(ctx context.Context, expected string, version llmsettings.Version) (llmsettings.Version, error) {
	encoded, err := json.Marshal(version.Settings)
	if err != nil {
		return version, err
	}
	previous, err := strconv.ParseInt(expected, 10, 64)
	if err != nil || previous < 0 {
		return version, llmsettings.ErrConflict
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO llm_settings_versions (`+llmSettingsColumns+`)
		SELECT ?, ?, ?, ?, ? WHERE COALESCE((SELECT MAX(revision) FROM llm_settings_versions), 0) = ?`,
		previous+1, string(encoded), version.ActorID, version.Source, version.CreatedAt.UTC().Format(time.RFC3339Nano), previous)
	if err != nil {
		return version, fmt.Errorf("append llm settings: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return version, err
	}
	if count != 1 {
		return version, llmsettings.ErrConflict
	}
	version.Revision = strconv.FormatInt(previous+1, 10)
	return version, nil
}

// SaveLLMConnectionCheck replaces the connection's latest test.
func (r *Repository) SaveLLMConnectionCheck(ctx context.Context, check llmsettings.Check) error {
	reachable := 0
	if check.Reachable {
		reachable = 1
	}
	if _, err := r.db.ExecContext(ctx, `INSERT INTO llm_connection_checks
		(connection_id, model, checked_at, reachable, vision, outcome, actor_id) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(connection_id) DO UPDATE SET model = excluded.model, checked_at = excluded.checked_at,
		reachable = excluded.reachable, vision = excluded.vision, outcome = excluded.outcome, actor_id = excluded.actor_id`,
		check.ConnectionID, check.Model, check.CheckedAt.UTC().Format(time.RFC3339Nano), reachable, check.Vision, check.Outcome, check.ActorID); err != nil {
		return fmt.Errorf("save llm connection check: %w", err)
	}
	return nil
}

func (r *Repository) LLMConnectionChecks(ctx context.Context) ([]llmsettings.Check, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT connection_id, model, checked_at, reachable, vision, outcome, actor_id
		FROM llm_connection_checks ORDER BY connection_id`)
	if err != nil {
		return nil, fmt.Errorf("read llm connection checks: %w", err)
	}
	defer rows.Close()
	result := []llmsettings.Check{}
	for rows.Next() {
		var check llmsettings.Check
		var checked string
		var reachable int
		if err := rows.Scan(&check.ConnectionID, &check.Model, &checked, &reachable, &check.Vision, &check.Outcome, &check.ActorID); err != nil {
			return nil, err
		}
		if check.CheckedAt, err = time.Parse(time.RFC3339Nano, checked); err != nil {
			return nil, err
		}
		check.Reachable = reachable == 1
		result = append(result, check)
	}
	return result, rows.Err()
}

// SetSessionChatModel stores the owner's chat model choice for the session
// ("" follows the default). The service has checked it; updated_at is kept.
func (r *Repository) SetSessionChatModel(ctx context.Context, ownerID, sessionID, model string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE go_chat_sessions SET chat_model = ? WHERE id = ? AND owner_id = ?`, model, sessionID, ownerID)
	if err != nil {
		return false, fmt.Errorf("set chat session model: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, nil
}
