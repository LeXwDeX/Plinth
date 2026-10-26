package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LeXwDeX/Plinth/internal/chat/agentparams"
)

type agentConsentClaim struct {
	CallID string `json:"call_id"`
	TaskID string `json:"task_id,omitempty"`
}

func (r *Repository) BeginAgentConsent(ownerID, scope, requestID string, request, snapshot json.RawMessage) error {
	if requestID == "" || len(request) == 0 || len(snapshot) == 0 {
		return errors.New("consent request or snapshot missing")
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := loadConsentState(tx, ownerID, scope)
	if err != nil {
		return err
	}
	if raw := state[agentparams.ConsentClaimKey]; len(raw) > 0 {
		var claim agentConsentClaim
		if err := json.Unmarshal(raw, &claim); err != nil || claim.TaskID == "" {
			return errors.New("previous consent claim requires reconciliation")
		}
	}
	if _, err := tx.Exec(`DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ? AND state_key IN (?, ?, ?, ?, ?, ?)`,
		ownerID, scope, agentparams.ConsentPendingKey, agentparams.ConsentDecisionKey,
		agentparams.ConsentApprovedKey, agentparams.ConsentSnapshotKey, agentparams.ConsentClaimKey,
		agentparams.ConsentRequestIDKey); err != nil {
		return fmt.Errorf("reset consent: %w", err)
	}
	for key, value := range map[string]json.RawMessage{
		agentparams.ConsentPendingKey:   request,
		agentparams.ConsentDecisionKey:  json.RawMessage(`"pending"`),
		agentparams.ConsentApprovedKey:  json.RawMessage(`false`),
		agentparams.ConsentSnapshotKey:  snapshot,
		agentparams.ConsentRequestIDKey: json.RawMessage(`"` + requestID + `"`),
	} {
		if err := putConsentState(tx, ownerID, scope, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) DecideAgentConsent(ownerID, scope, requestID string, approved bool) (bool, error) {
	if requestID == "" {
		return false, nil
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	state, err := loadConsentState(tx, ownerID, scope)
	if err != nil {
		return false, err
	}
	if state[agentparams.ConsentPendingKey] == nil || state[agentparams.ConsentClaimKey] != nil ||
		string(state[agentparams.ConsentRequestIDKey]) != `"`+requestID+`"` ||
		string(state[agentparams.ConsentDecisionKey]) != `"pending"` {
		return false, nil
	}
	decision := json.RawMessage(`"denied"`)
	if approved {
		decision = json.RawMessage(`"approved"`)
	}
	if err := putConsentState(tx, ownerID, scope, agentparams.ConsentDecisionKey, decision); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *Repository) ClaimAgentConsent(ownerID, scope, callID, idempotencyKey string, snapshot json.RawMessage) (string, bool, error) {
	if callID == "" {
		return "", false, errors.New("consent call identity missing")
	}
	// A nil snapshot requests recovery only; it must never create a claim.
	recoveryOnly := len(snapshot) == 0
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	state, err := loadConsentState(tx, ownerID, scope)
	if err != nil {
		return "", false, err
	}
	if raw := state[agentparams.ConsentClaimKey]; len(raw) > 0 {
		var claim agentConsentClaim
		if err := json.Unmarshal(raw, &claim); err != nil {
			return "", false, fmt.Errorf("decode consent claim: %w", err)
		}
		if claim.CallID != callID {
			return "", false, nil
		}
		if claim.TaskID != "" {
			return claim.TaskID, true, nil
		}
		// Submit may have committed while persisting the Task binding or
		// consent consumption failed. Recover by the platform idempotency key
		// before rebuilding uploads or sending another paid request.
		if idempotencyKey != "" {
			var taskID, taskSession string
			err := tx.QueryRow(`SELECT id, session_id FROM generation_tasks WHERE owner_id = ? AND idempotency_key = ?`,
				ownerID, idempotencyKey).Scan(&taskID, &taskSession)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return "", false, err
			}
			if err == nil {
				if taskSession != scope {
					return "", false, errors.New("recovered task belongs to another session")
				}
				claim.TaskID = taskID
				encoded, _ := json.Marshal(claim)
				if err := putConsentState(tx, ownerID, scope, agentparams.ConsentClaimKey, encoded); err != nil {
					return "", false, err
				}
				if err := deleteVisibleConsent(tx, ownerID, scope); err != nil {
					return "", false, err
				}
				if err := tx.Commit(); err != nil {
					return "", false, err
				}
				return taskID, true, nil
			}
		}
		if recoveryOnly {
			return "", false, nil
		}
	} else {
		if recoveryOnly {
			return "", false, nil
		}
		claim, _ := json.Marshal(agentConsentClaim{CallID: callID})
		if state[agentparams.ConsentPendingKey] == nil ||
			string(state[agentparams.ConsentDecisionKey]) != `"approved"` ||
			string(state[agentparams.ConsentSnapshotKey]) != string(snapshot) {
			return "", false, nil
		}
		if err := putConsentState(tx, ownerID, scope, agentparams.ConsentClaimKey, claim); err != nil {
			return "", false, err
		}
	}
	if state[agentparams.ConsentPendingKey] == nil ||
		string(state[agentparams.ConsentDecisionKey]) != `"approved"` ||
		string(state[agentparams.ConsentSnapshotKey]) != string(snapshot) {
		return "", false, nil
	}
	if err := tx.Commit(); err != nil {
		return "", false, err
	}
	return "", true, nil
}

func (r *Repository) AcceptAgentConsent(ownerID, scope, callID, taskID string) error {
	if callID == "" || taskID == "" {
		return errors.New("consent accepted task identity missing")
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := loadConsentState(tx, ownerID, scope)
	if err != nil {
		return err
	}
	var claim agentConsentClaim
	if err := json.Unmarshal(state[agentparams.ConsentClaimKey], &claim); err != nil || claim.CallID != callID ||
		(claim.TaskID != "" && claim.TaskID != taskID) {
		return errors.New("consent claim does not match accepted task")
	}
	claim.TaskID = taskID
	encoded, _ := json.Marshal(claim)
	if err := putConsentState(tx, ownerID, scope, agentparams.ConsentClaimKey, encoded); err != nil {
		return err
	}
	if err := deleteVisibleConsent(tx, ownerID, scope); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repository) ReleaseAgentConsent(ownerID, scope, callID, idempotencyKey string) error {
	if callID == "" {
		return errors.New("consent call identity missing")
	}
	tx, err := r.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := loadConsentState(tx, ownerID, scope)
	if err != nil {
		return err
	}
	if len(state[agentparams.ConsentClaimKey]) == 0 {
		return nil
	}
	var claim agentConsentClaim
	if err := json.Unmarshal(state[agentparams.ConsentClaimKey], &claim); err != nil ||
		claim.CallID != callID || claim.TaskID != "" {
		return errors.New("cannot release another or accepted consent claim")
	}
	if idempotencyKey != "" {
		var taskID string
		err := tx.QueryRow(`SELECT id FROM generation_tasks WHERE owner_id = ? AND idempotency_key = ?`,
			ownerID, idempotencyKey).Scan(&taskID)
		if err == nil {
			return errors.New("cannot release consent with an accepted task")
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ? AND state_key = ?`,
		ownerID, scope, agentparams.ConsentClaimKey); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteVisibleConsent(tx *sql.Tx, ownerID, scope string) error {
	_, err := tx.Exec(`DELETE FROM go_agent_state WHERE owner_id = ? AND scope = ? AND state_key IN (?, ?, ?, ?, ?)`,
		ownerID, scope, agentparams.ConsentPendingKey, agentparams.ConsentDecisionKey,
		agentparams.ConsentApprovedKey, agentparams.ConsentSnapshotKey, agentparams.ConsentRequestIDKey)
	if err != nil {
		return fmt.Errorf("consume consent: %w", err)
	}
	return nil
}

func loadConsentState(tx *sql.Tx, ownerID, scope string) (map[string]json.RawMessage, error) {
	rows, err := tx.Query(`SELECT state_key, value_json FROM go_agent_state WHERE owner_id = ? AND scope = ? AND state_key IN (?, ?, ?, ?, ?, ?)`,
		ownerID, scope, agentparams.ConsentPendingKey, agentparams.ConsentDecisionKey,
		agentparams.ConsentApprovedKey, agentparams.ConsentSnapshotKey, agentparams.ConsentClaimKey,
		agentparams.ConsentRequestIDKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	state := map[string]json.RawMessage{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		state[key] = json.RawMessage(value)
	}
	return state, rows.Err()
}

func putConsentState(tx *sql.Tx, ownerID, scope, key string, value json.RawMessage) error {
	_, err := tx.Exec(`INSERT INTO go_agent_state (owner_id, scope, state_key, value_json, updated_at)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (owner_id, scope, state_key) DO UPDATE SET
		value_json = excluded.value_json, updated_at = excluded.updated_at`,
		ownerID, scope, key, string(value), time.Now().UTC())
	return err
}
