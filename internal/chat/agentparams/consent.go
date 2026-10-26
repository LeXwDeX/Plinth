package agentparams

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
)

// Consent state is private server state shared by the harness and tool gate.
// A claim survives uncertain submissions; only a fresh human decision may
// replace it for another paid call.
const (
	ConsentPendingKey   = "consent_pending"
	ConsentDecisionKey  = "consent_decision"
	ConsentApprovedKey  = "consent_approved"
	ConsentSnapshotKey  = "consent_snapshot"
	ConsentClaimKey     = "consent_claim"
	ConsentRequestIDKey = "consent_request_id"
	AgentTaskIDKey      = "task_id"
)

func isPrivateAgentStateKey(key string) bool {
	switch key {
	case ConsentPendingKey, ConsentDecisionKey, ConsentApprovedKey, ConsentRequestIDKey,
		ConsentSnapshotKey, ConsentClaimKey, AgentTaskIDKey:
		return true
	default:
		return false
	}
}

type ConsentPersister interface {
	BeginAgentConsent(ownerID, scope, requestID string, request, snapshot json.RawMessage) error
	DecideAgentConsent(ownerID, scope, requestID string, approved bool) (bool, error)
	ClaimAgentConsent(ownerID, scope, callID, idempotencyKey string, snapshot json.RawMessage) (taskID string, allowed bool, err error)
	ReleaseAgentConsent(ownerID, scope, callID, idempotencyKey string) error
	AcceptAgentConsent(ownerID, scope, callID, taskID string) error
}

func (s *Store) consentPersister(scope string) (ConsentPersister, string, string, error) {
	ownerID, subScope, ok := splitScope(scope)
	if !ok || ownerID == "" || subScope == "" {
		return nil, "", "", errors.New("invalid consent scope")
	}
	bridge, ok := s.persister.(ConsentPersister)
	if !ok {
		return nil, "", "", errors.New("durable consent storage unavailable")
	}
	return bridge, ownerID, subScope, nil
}

func (s *Store) BeginConsent(scope string, request, snapshot json.RawMessage) error {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return fmt.Errorf("create consent request identity: %w", err)
	}
	requestID := fmt.Sprintf("%x", nonce[:])
	if s.persister == nil {
		s.Delete(scope, ConsentClaimKey, ConsentSnapshotKey)
		s.Set(scope, map[string]json.RawMessage{
			ConsentPendingKey: request, ConsentDecisionKey: json.RawMessage(`"pending"`),
			ConsentApprovedKey: json.RawMessage(`false`), ConsentSnapshotKey: snapshot,
			ConsentRequestIDKey: json.RawMessage(`"` + requestID + `"`),
		})
		return nil
	}
	bridge, ownerID, subScope, err := s.consentPersister(scope)
	if err != nil {
		return err
	}
	return bridge.BeginAgentConsent(ownerID, subScope, requestID, request, snapshot)
}

func (s *Store) DecideConsent(scope, requestID string, approved bool) (bool, error) {
	if requestID == "" {
		return false, nil
	}
	if s.persister == nil {
		state := s.Get(scope)
		if state[ConsentPendingKey] == nil || string(state[ConsentRequestIDKey]) != `"`+requestID+`"` ||
			string(state[ConsentDecisionKey]) != `"pending"` {
			return false, nil
		}
		decision := json.RawMessage(`"denied"`)
		if approved {
			decision = json.RawMessage(`"approved"`)
		}
		s.Set(scope, map[string]json.RawMessage{ConsentDecisionKey: decision})
		return true, nil
	}
	bridge, ownerID, subScope, err := s.consentPersister(scope)
	if err != nil {
		return false, err
	}
	return bridge.DecideAgentConsent(ownerID, subScope, requestID, approved)
}

func (s *Store) ClaimConsent(scope, callID, idempotencyKey string, snapshot json.RawMessage) (string, bool, error) {
	bridge, ownerID, subScope, err := s.consentPersister(scope)
	if err != nil {
		return "", false, err
	}
	return bridge.ClaimAgentConsent(ownerID, subScope, callID, idempotencyKey, snapshot)
}

func (s *Store) AcceptConsent(scope, callID, taskID string) error {
	bridge, ownerID, subScope, err := s.consentPersister(scope)
	if err != nil {
		return err
	}
	return bridge.AcceptAgentConsent(ownerID, subScope, callID, taskID)
}

func (s *Store) ReleaseConsent(scope, callID, idempotencyKey string) error {
	bridge, ownerID, subScope, err := s.consentPersister(scope)
	if err != nil {
		return err
	}
	return bridge.ReleaseAgentConsent(ownerID, subScope, callID, idempotencyKey)
}
