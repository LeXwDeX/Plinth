package agents

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

type Registry struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewRegistry() *Registry {
	return &Registry{adapters: map[string]Adapter{}}
}

func (registry *Registry) Register(
	key string,
	adapter Adapter,
) error {
	key = strings.TrimSpace(key)
	if key == "" || adapter == nil {
		return errors.New("agents: adapter key and implementation are required")
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.adapters[key]; exists {
		return fmt.Errorf("agents: adapter %q already registered", key)
	}
	registry.adapters[key] = adapter
	return nil
}

func (registry *Registry) Adapter(key string) (Adapter, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	adapter, found := registry.adapters[key]
	return adapter, found
}
