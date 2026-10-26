package agentparams

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

// The scope remains the real session so session deletion removes these rows.
// Separate user/agent keys preserve harness priority across process restarts.
const offeringStatePrefix = "__offering_params_v1:"

func isOfferingStateKey(key string) bool { return strings.HasPrefix(key, offeringStatePrefix) }

func offeringPrefix(offeringID string, user bool) string {
	layer := "agent:"
	if user {
		layer = "user:"
	}
	return offeringStatePrefix + base64.RawURLEncoding.EncodeToString([]byte(offeringID)) + ":" + layer
}

// GetOffering returns legacy defaults overlaid by this exact offering only.
// Catalog defaults are deliberately not persisted by reads.
func (s *Store) GetOffering(scope, offeringID string) map[string]json.RawMessage {
	if offeringID == "" {
		return s.Get(scope)
	}
	state := s.rawParams(scope)
	out := offeringOverrides(state, offeringID)
	legacy := s.visibleParams(cloneParameterValues(state))
	for key, value := range legacy {
		if _, exists := out[key]; !exists && !offeringCleared(state, offeringID, key) {
			out[key] = value
		}
	}
	return out
}

// OfferingOverrides excludes legacy defaults, which the harness merges into
// Options for compatibility. It is consulted only when preparing a new input.
func (s *Store) OfferingOverrides(scope, offeringID string) map[string]json.RawMessage {
	state := s.rawParams(scope)
	out := offeringOverrides(state, offeringID)
	for name, value := range s.offeringStates(state, offeringID) {
		if value.Source == "default" {
			out[name] = json.RawMessage("null")
		}
	}
	return out
}

func offeringOverrides(state map[string]json.RawMessage, offeringID string) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage)
	if offeringID == "" {
		return out
	}
	for _, user := range []bool{false, true} {
		prefix := offeringPrefix(offeringID, user)
		for key, value := range state {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, prefix))
			if err != nil || isPrivateAgentStateKey(string(decoded)) || isOfferingStateKey(string(decoded)) {
				continue
			}
			if len(value) > 0 && string(value) != "null" {
				out[string(decoded)] = append(json.RawMessage(nil), value...)
			}
		}
	}
	return out
}

func (s *Store) SetOffering(scope, offeringID string, params map[string]json.RawMessage) error {
	return s.setOffering(scope, offeringID, params, false, nil)
}

func (s *Store) SetUserOffering(scope, offeringID string, params map[string]json.RawMessage) error {
	return s.setOffering(scope, offeringID, params, true, nil)
}

func (s *Store) setOffering(scope, offeringID string, params map[string]json.RawMessage, user bool, reasons map[string]string) error {
	if offeringID == "" {
		return errors.New("offering ID missing")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Read durable state under the write lock: patches never replace another
	// offering's values, and bounds apply after restart as well as in memory.
	current := make(map[string]json.RawMessage)
	var durable OfferingStatePersister
	var ownerID, subScope string
	if s.persister != nil {
		var ok bool
		ownerID, subScope, ok = splitScope(scope)
		if !ok {
			return errors.New("invalid offering parameter scope")
		}
		durable, ok = s.persister.(OfferingStatePersister)
		if !ok {
			return errors.New("atomic offering parameter persistence unavailable")
		}
		persisted, err := s.persister.LoadAgentState(ownerID, subScope)
		if err != nil {
			return err
		}
		current = cloneParameterValues(persisted)
	} else {
		for key, value := range s.scopes[scope] {
			current[key] = append(json.RawMessage(nil), value...)
		}
		for key, value := range s.userOverrides[scope] {
			current[key] = append(json.RawMessage(nil), value...)
		}
	}
	if s.scopes[scope] == nil && len(s.scopes) >= maxScopes {
		return errors.New("offering parameter scope limit exceeded")
	}
	expected := cloneParameterValues(current)
	prefix := offeringPrefix(offeringID, user)
	accepted := make(map[string]json.RawMessage)
	var deleted []string
	for name, value := range params {
		if name == "" || isPrivateAgentStateKey(name) || isOfferingStateKey(name) {
			return errors.New("invalid offering parameter key")
		}
		key := prefix + base64.RawURLEncoding.EncodeToString([]byte(name))
		if len(key) > maxValueKeyBytes || len(value) > maxValueKeyBytes || (len(value) > 0 && !json.Valid(value)) {
			return errors.New("invalid offering parameter value")
		}
		if len(value) == 0 || string(value) == "null" {
			// A bounded null tombstone suppresses this offering's legacy
			// fallback, while allowing the next Agent suggestion in auto mode.
			if _, exists := current[key]; !exists && len(current) >= maxKeysPerScope {
				return errors.New("offering parameter key limit exceeded")
			}
			if user {
				old, existed := current[key]
				current[key] = json.RawMessage("null")
				if totalBytes(current) > maxScopeBytes {
					if existed {
						current[key] = old
					} else {
						delete(current, key)
					}
					return errors.New("offering parameter byte limit exceeded")
				}
				accepted[key] = current[key]
			} else {
				delete(current, key)
				deleted = append(deleted, key)
			}
			reasonKey := offeringReasonKey(offeringID, name)
			delete(current, reasonKey)
			deleted = append(deleted, reasonKey)
			// Clearing a harness override must not resurrect an older agent value.
			if user {
				agentKey := offeringPrefix(offeringID, false) + base64.RawURLEncoding.EncodeToString([]byte(name))
				delete(current, agentKey)
				deleted = append(deleted, agentKey)
			}
			continue
		}
		old, exists := current[key]
		if !exists && len(current) >= maxKeysPerScope {
			return errors.New("offering parameter key limit exceeded")
		}
		current[key] = append(json.RawMessage(nil), value...)
		if totalBytes(current) > maxScopeBytes {
			if exists {
				current[key] = old
			} else {
				delete(current, key)
			}
			return errors.New("offering parameter byte limit exceeded")
		}
		reasonKey := offeringReasonKey(offeringID, name)
		if !user && strings.TrimSpace(reasons[name]) != "" {
			encoded, _ := json.Marshal(strings.TrimSpace(reasons[name]))
			oldReason, hadReason := current[reasonKey]
			current[reasonKey] = encoded
			if len(encoded) > maxValueKeyBytes || len(current) > maxKeysPerScope || totalBytes(current) > maxScopeBytes {
				// A reason and its value are one suggestion. Preserve the old
				// pair if the bounded store cannot accept the new pair.
				if exists {
					current[key] = old
				} else {
					delete(current, key)
				}
				if hadReason {
					current[reasonKey] = oldReason
				} else {
					delete(current, reasonKey)
				}
				return errors.New("offering parameter reason limit exceeded")
			}
			accepted[reasonKey] = encoded
		} else {
			delete(current, reasonKey)
			deleted = append(deleted, reasonKey)
		}
		accepted[key] = current[key]
	}
	// Persist the complete patch before publishing its in-memory view. The
	// expected state also fences concurrent writers across Store instances.
	if durable != nil {
		if err := durable.PatchOfferingState(ownerID, subScope, expected, accepted, deleted); err != nil {
			return err
		}
	}
	s.scopes[scope] = current
	return nil
}

// ParameterState is a projection of the existing user/Agent layers, not a
// second store. Default means no user pin and no current Agent suggestion.
type ParameterState struct {
	Value  json.RawMessage `json:"value,omitempty"`
	Source string          `json:"source"`
	Reason string          `json:"reason,omitempty"`
}

func offeringReasonKey(id, name string) string {
	return offeringStatePrefix + base64.RawURLEncoding.EncodeToString([]byte(id)) + ":reason:" + base64.RawURLEncoding.EncodeToString([]byte(name))
}
func offeringCleared(state map[string]json.RawMessage, id, name string) bool {
	return string(state[offeringPrefix(id, true)+base64.RawURLEncoding.EncodeToString([]byte(name))]) == "null"
}
func (s *Store) SetAgentOffering(scope, id string, values map[string]json.RawMessage, reasons map[string]string) error {
	return s.setOffering(scope, id, values, false, reasons)
}
func (s *Store) OfferingStates(scope, id string) map[string]ParameterState {
	return s.offeringStates(s.rawParams(scope), id)
}
func (s *Store) offeringStates(state map[string]json.RawMessage, id string) map[string]ParameterState {
	out := map[string]ParameterState{}
	for name, value := range s.visibleParams(cloneParameterValues(state)) {
		if !offeringCleared(state, id, name) {
			out[name] = ParameterState{Value: append(json.RawMessage(nil), value...), Source: "user"}
		}
	}
	for _, user := range []bool{false, true} {
		prefix := offeringPrefix(id, user)
		for key, value := range state {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(key, prefix))
			if err != nil {
				continue
			}
			name := string(decoded)
			if isPrivateAgentStateKey(name) || isOfferingStateKey(name) {
				continue
			}
			if string(value) == "null" {
				if _, exists := out[name]; !exists {
					out[name] = ParameterState{Source: "default"}
				}
				continue
			}
			source := "agent"
			reason := ""
			if user {
				source = "user"
			} else {
				_ = json.Unmarshal(state[offeringReasonKey(id, name)], &reason)
			}
			out[name] = ParameterState{Value: append(json.RawMessage(nil), value...), Source: source, Reason: reason}
		}
	}
	return out
}
func cloneParameterValues(in map[string]json.RawMessage) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(in))
	for k, v := range in {
		out[k] = append(json.RawMessage(nil), v...)
	}
	return out
}
