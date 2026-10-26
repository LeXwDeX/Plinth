package sqlite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

var _ agentparams.OfferingStatePersister = (*Repository)(nil)

// PatchOfferingState changes the existing session-state rows in one transaction.
// Comparing the read snapshot prevents a concurrent writer from invalidating
// bounded admission or replacing half of a value/reason/clear operation.
func (r *Repository) PatchOfferingState(ownerID, scope string, expected, values map[string]json.RawMessage, deleted []string) error {
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT state_key, value_json FROM go_agent_state WHERE owner_id = ? AND scope = ?`, ownerID, scope)
	if err != nil {
		return err
	}
	current := map[string]json.RawMessage{}
	for rows.Next() {
		var key, value string
		if err = rows.Scan(&key, &value); err != nil {
			rows.Close()
			return err
		}
		current[key] = json.RawMessage(value)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if len(current) != len(expected) {
		return errors.New("offering parameter state changed")
	}
	for key, value := range current {
		if !bytes.Equal(value, expected[key]) {
			return errors.New("offering parameter state changed")
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, err = tx.Exec(`INSERT INTO go_agent_state (owner_id, scope, state_key, value_json, updated_at)
 VALUES (?, ?, ?, ?, ?) ON CONFLICT (owner_id, scope, state_key) DO UPDATE SET
 value_json = excluded.value_json, updated_at = excluded.updated_at`, ownerID, scope, key, string(values[key]), time.Now().UTC()); err != nil {
			return err
		}
	}
	for _, key := range deleted {
		if _, err = tx.Exec(`DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ? AND state_key = ?`, ownerID, scope, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}
