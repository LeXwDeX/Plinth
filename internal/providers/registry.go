package providers

import (
	"fmt"
	"sort"
	"sync"

	"github.com/LeXwDeX/Plinth/internal/generation"
)

type Registry struct {
	mu    sync.RWMutex
	ports map[string]entry
}

type entry struct {
	port         generation.ProviderPort
	capabilities map[generation.Capability]struct{}
}

func NewRegistry() *Registry {
	return &Registry{ports: make(map[string]entry)}
}

func (r *Registry) Register(adapterKey string, port generation.ProviderPort) error {
	return r.register(adapterKey, port, nil)
}

func (r *Registry) RegisterCapabilities(
	adapterKey string,
	port generation.ProviderPort,
	capabilities ...generation.Capability,
) error {
	if len(capabilities) == 0 {
		return fmt.Errorf("adapter %q 至少需要一个 capability", adapterKey)
	}
	supported := make(map[generation.Capability]struct{}, len(capabilities))
	for _, capability := range capabilities {
		if !capability.Valid() {
			return fmt.Errorf("adapter %q 的 capability %q 无效", adapterKey, capability)
		}
		supported[capability] = struct{}{}
	}
	return r.register(adapterKey, port, supported)
}

func (r *Registry) register(
	adapterKey string,
	port generation.ProviderPort,
	capabilities map[generation.Capability]struct{},
) error {
	if adapterKey == "" {
		return fmt.Errorf("adapter key 不能为空")
	}
	if port == nil {
		return fmt.Errorf("adapter %q 不能为空", adapterKey)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.ports[adapterKey]; exists {
		return fmt.Errorf("adapter %q 重复注册", adapterKey)
	}
	r.ports[adapterKey] = entry{port: port, capabilities: capabilities}
	return nil
}

func (r *Registry) Provider(
	offering generation.Offering,
) (generation.ProviderPort, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.ports[offering.AdapterKey]
	if !ok {
		return nil, false
	}
	if entry.capabilities != nil {
		if _, supported := entry.capabilities[offering.Capability]; !supported {
			return nil, false
		}
	}
	return entry.port, true
}

func (r *Registry) Keys() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]string, 0, len(r.ports))
	for key := range r.ports {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
