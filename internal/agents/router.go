package agents

import (
	"context"
	"fmt"
)

// StatusError is an upstream HTTP refusal reduced to its status code, so a
// caller can classify it without reading (or showing) the upstream body.
type StatusError struct {
	Status int
}

func (err *StatusError) Error() string { return fmt.Sprintf("agent returned status %d", err.Status) }

// ConnectionRouter serves each request from the chat model connection it
// names (#322 Scope 5). Every connection has its own adapter, built from its
// own address and key; an empty name is the deployment's own connection.
type ConnectionRouter struct {
	defaultID string
	adapters  map[string]Adapter
}

// NewConnectionRouter needs the default connection among adapters.
func NewConnectionRouter(defaultID string, adapters map[string]Adapter) (*ConnectionRouter, error) {
	if _, found := adapters[defaultID]; !found {
		return nil, fmt.Errorf("agents: default connection %q has no adapter", defaultID)
	}
	copied := make(map[string]Adapter, len(adapters))
	for id, adapter := range adapters {
		if adapter == nil {
			return nil, fmt.Errorf("agents: connection %q has no adapter", id)
		}
		copied[id] = adapter
	}
	return &ConnectionRouter{defaultID: defaultID, adapters: copied}, nil
}

func (router *ConnectionRouter) adapter(connection string) Adapter {
	if connection == "" {
		connection = router.defaultID
	}
	if adapter, found := router.adapters[connection]; found {
		return adapter
	}
	return NotConfigured{Missing: []string{connection}}
}

func (router *ConnectionRouter) Stream(ctx context.Context, request Request, emit func(Delta) error) (Completion, error) {
	return router.adapter(request.Connection).Stream(ctx, request, emit)
}

func (router *ConnectionRouter) Complete(ctx context.Context, request Request) (Completion, error) {
	return router.adapter(request.Connection).Complete(ctx, request)
}
