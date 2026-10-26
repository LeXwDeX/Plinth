package schema

import "errors"

// ResultRefetchObserve re-reads the accepted upstream job through the
// adapter's status call (Observe) for the result locators. No create call is
// made and no paid dispatch is allowed.
const ResultRefetchObserve = "observe"

// ResultRefetchPolicy is the private runtime declaration of how a provider
// connection's already accepted results can be fetched again after a Task
// failed to store them (#295). Absence means the provider cannot: its results
// were one-shot (inline bytes, no job to query again).
type ResultRefetchPolicy struct {
	Mode string `json:"mode"`
	// RetentionHours is how long after the upstream finished the provider is
	// documented to keep the result. Zero means no documented limit: the
	// upstream answer decides.
	RetentionHours int `json:"retention_hours,omitempty"`
}

func (p ResultRefetchPolicy) Validate() error {
	if p.Mode != ResultRefetchObserve {
		return errors.New("result_refetch.mode must be observe")
	}
	if p.RetentionHours < 0 || p.RetentionHours > 24*365 {
		return errors.New("result_refetch.retention_hours must be between 0 and 8760")
	}
	return nil
}
