package schema

import (
	"errors"
	"time"
)

// TaskRetentionPolicy is private runtime policy, never a public UI projection.
// Disabled declarations may have no age; execution never supplies a fallback.
type TaskRetentionPolicy struct {
	Enabled           bool     `json:"enabled"`
	MinimumAgeSeconds int64    `json:"minimum_age_seconds"`
	TaskStates        []string `json:"task_states"`
	TaskTimeField     string   `json:"task_time_field"`
	InputTimeField    string   `json:"input_time_field"`
	ReferenceMode     string   `json:"reference_mode"`
}

func (p TaskRetentionPolicy) Validate() error {
	// Older Catalog documents can omit this entirely. Absence means disabled,
	// never an inferred retention age or permission to run maintenance.
	if !p.Enabled && p.MinimumAgeSeconds == 0 && len(p.TaskStates) == 0 && p.TaskTimeField == "" && p.InputTimeField == "" && p.ReferenceMode == "" {
		return nil
	}
	if p.MinimumAgeSeconds < 0 || p.MinimumAgeSeconds > int64((1<<63-1)/time.Second) || p.Enabled && p.MinimumAgeSeconds == 0 {
		return errors.New("task_retention requires an explicit positive representable minimum age when enabled")
	}
	if p.TaskTimeField != "finished_at" || p.InputTimeField != "created_at" || p.ReferenceMode != "preserve_all" || len(p.TaskStates) == 0 {
		return errors.New("task_retention must declare supported time and reference rules")
	}
	seen := map[string]bool{}
	for _, s := range p.TaskStates {
		if seen[s] || (s != "SUCCEEDED" && s != "FAILED" && s != "CANCELED") {
			return errors.New("task_retention states must be unique terminal states")
		}
		seen[s] = true
	}
	return nil
}
