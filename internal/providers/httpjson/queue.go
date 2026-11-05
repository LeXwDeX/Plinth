package httpjson

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

// UpstreamQueue reads the upstream queue a connection declares for one status
// object (#131): the status word's rule says whether the job waits in the
// upstream's own queue, and status_mapping.queue_ahead_field names the
// object's count of jobs ahead of it. It returns nil when the rule is not
// queued, and a queue without a count when the count is absent or invalid.
func UpstreamQueue(
	mapping *generation.StatusMapping,
	rule generation.StatusRule,
	object map[string]json.RawMessage,
) *generation.UpstreamQueue {
	if !rule.Queued {
		return nil
	}
	queue := &generation.UpstreamQueue{}
	if mapping == nil || strings.TrimSpace(mapping.QueueAheadField) == "" {
		return queue
	}
	segments := strings.Split(strings.TrimSpace(mapping.QueueAheadField), ".")
	current := object
	for index, segment := range segments {
		raw, ok := current[segment]
		if !ok {
			return queue
		}
		if index == len(segments)-1 {
			if ahead, ok := queueCount(raw); ok {
				queue.Ahead = &ahead
			}
			return queue
		}
		var next map[string]json.RawMessage
		if json.Unmarshal(raw, &next) != nil {
			return queue
		}
		current = next
	}
	return queue
}

// queueCount accepts a non-negative whole JSON number.
func queueCount(raw json.RawMessage) (int, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var number json.Number
	if decoder.Decode(&number) != nil {
		return 0, false
	}
	value, err := number.Float64()
	if err != nil || value < 0 || value != math.Trunc(value) || value > math.MaxInt32 {
		return 0, false
	}
	return int(value), true
}
