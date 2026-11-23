package agentparams

import (
	"encoding/json"
	"strconv"
	"strings"
)

// SetImageCount records the 2D image count the person set for this session
// (#486). A count below one clears it: the person went back to whatever the
// Offering declares, and the prompt then says nothing about a count instead of
// naming a number nobody chose.
func (s *Store) SetImageCount(scope string, count int) {
	if count < 1 {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.scopes[scope], ImageCountKey)
		delete(s.userOverrides[scope], ImageCountKey)
		s.persistDelete(scope, ImageCountKey)
		return
	}
	encoded, err := json.Marshal(count)
	if err != nil {
		return
	}
	params := map[string]json.RawMessage{ImageCountKey: encoded}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := boundedParams(len(s.scopes), s.scopes[scope], params)
	if !ok {
		return
	}
	s.scopes[scope] = current
	s.persistSet(scope, params)
}

// ImageCount reports the session's own image count. The second result is false
// when the person set none, which is not a count of zero.
func (s *Store) ImageCount(scope string) (int, bool) {
	encoded, exists := s.rawParams(scope)[ImageCountKey]
	if !exists {
		return 0, false
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(encoded)))
	if err != nil || count < 1 {
		return 0, false
	}
	return count, true
}