package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrNotConfigured marks a live Agent whose deployment has no usable model
// setting (no API key or no model): the person is told the Agent is not
// configured instead of an unknown error (#253).
var ErrNotConfigured = errors.New("agent model is not configured")

// NotConfigured stands in for the live Agent while its settings are missing,
// so the application still starts; every call reports ErrNotConfigured.
// Missing holds the names of the missing settings, never their values.
type NotConfigured struct {
	Missing []string
}

func (adapter NotConfigured) Stream(
	context.Context,
	Request,
	func(Delta) error,
) (Completion, error) {
	return Completion{}, adapter.err()
}

func (adapter NotConfigured) Complete(context.Context, Request) (Completion, error) {
	return Completion{}, adapter.err()
}

func (adapter NotConfigured) err() error {
	if len(adapter.Missing) == 0 {
		return ErrNotConfigured
	}
	return fmt.Errorf("%w: missing %s", ErrNotConfigured, strings.Join(adapter.Missing, ", "))
}

// IsNotConfigured reports whether adapter is the stand-in for a live Agent
// without settings; such an adapter needs no model name.
func IsNotConfigured(adapter Adapter) bool {
	_, standIn := adapter.(NotConfigured)
	return standIn
}
