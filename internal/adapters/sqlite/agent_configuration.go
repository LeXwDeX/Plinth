package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/LeXwDeX/Plinth/internal/agentconfiguration"
	"strconv"
	"time"
)

func scanAgentConfiguration(row interface{ Scan(...any) error }) (agentconfiguration.Version, error) {
	var v agentconfiguration.Version
	var revision int64
	var encoded, created string
	err := row.Scan(&revision, &encoded, &v.ActorID, &v.Source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return v, agentconfiguration.ErrNotFound
	}
	if err != nil {
		return v, err
	}
	v.Revision = strconv.FormatInt(revision, 10)
	v.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
	if err != nil {
		return v, err
	}
	err = json.Unmarshal([]byte(encoded), &v.Configuration)
	return v, err
}
func (r *Repository) LatestAgentConfiguration(ctx context.Context) (agentconfiguration.Version, error) {
	return scanAgentConfiguration(r.db.QueryRowContext(ctx, `SELECT revision,configuration_json,actor_id,source,created_at FROM agent_configuration_versions ORDER BY revision DESC LIMIT 1`))
}
func (r *Repository) AgentConfigurationVersion(ctx context.Context, revision string) (agentconfiguration.Version, error) {
	return scanAgentConfiguration(r.db.QueryRowContext(ctx, `SELECT revision,configuration_json,actor_id,source,created_at FROM agent_configuration_versions WHERE revision=?`, revision))
}
func (r *Repository) AgentConfigurationHistory(ctx context.Context, limit int) ([]agentconfiguration.Version, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT revision,configuration_json,actor_id,source,created_at FROM agent_configuration_versions ORDER BY revision DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []agentconfiguration.Version{}
	for rows.Next() {
		v, err := scanAgentConfiguration(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}
func (r *Repository) AppendAgentConfiguration(ctx context.Context, expected string, v agentconfiguration.Version) (agentconfiguration.Version, error) {
	encoded, err := json.Marshal(v.Configuration)
	if err != nil {
		return v, err
	}
	next, err := strconv.ParseInt(expected, 10, 64)
	if err != nil {
		return v, agentconfiguration.ErrConflict
	}
	next++
	result, err := r.db.ExecContext(ctx, `INSERT INTO agent_configuration_versions(revision,configuration_json,actor_id,source,created_at) SELECT ?,?,?,?,? WHERE COALESCE((SELECT MAX(revision) FROM agent_configuration_versions),0)=?`, next, string(encoded), v.ActorID, v.Source, v.CreatedAt.Format(time.RFC3339Nano), next-1)
	if err != nil {
		return v, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return v, err
	}
	if count != 1 {
		return v, agentconfiguration.ErrConflict
	}
	v.Revision = strconv.FormatInt(next, 10)
	return v, nil
}
