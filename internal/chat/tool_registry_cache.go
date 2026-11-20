package chat

import (
	"sync"

	"github.com/LeXwDeX/Plinth/internal/agents"
	"github.com/LeXwDeX/Plinth/internal/generation"
)

// registryCache memoizes what one Agent request derives from the runtime
// Offerings (#413): the Offering source per capability, an offering_argument or
// model_choices enum per tool, and the offered tool list per scope. A registry
// carries it only on the per-request fork a turn is served from
// (Orchestrator.registryForRequest), never on the Orchestrator's long-lived
// registry, so a cached value never outlives the request that read it. A turn
// builds its request and runs its tools on one goroutine; the mutex keeps the
// cache safe if a later change ever reads it from more than one.
type registryCache struct {
	mu              sync.Mutex
	offerings       map[string][]generation.Offering
	modelChoices    map[string]choiceSet
	offeringChoices map[string]choiceSet
	offered         map[string]offeredTools
}

// choiceSet is one narrowed enum and whether the tool declares it at all.
type choiceSet struct {
	choices  []string
	narrowed bool
}

// offeredTools is one scope's evaluation: the tool list the model is offered
// and the tools the Agent scope withholds. describe and withheld both read it,
// so a turn evaluates permits once per tool for both lists instead of once per
// caller.
type offeredTools struct {
	tools    []agents.Tool
	withheld []string
}

func newRegistryCache() *registryCache {
	return &registryCache{
		offerings:       map[string][]generation.Offering{},
		modelChoices:    map[string]choiceSet{},
		offeringChoices: map[string]choiceSet{},
		offered:         map[string]offeredTools{},
	}
}

func (cache *registryCache) offeringsFor(
	source func(string) []generation.Offering,
	capability string,
) []generation.Offering {
	cache.mu.Lock()
	offerings, known := cache.offerings[capability]
	cache.mu.Unlock()
	if known {
		return offerings
	}
	offerings = source(capability)
	cache.mu.Lock()
	cache.offerings[capability] = offerings
	cache.mu.Unlock()
	return offerings
}

func (cache *registryCache) loadModelChoices(tool string) (choiceSet, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	choices, ok := cache.modelChoices[tool]
	return choices, ok
}

func (cache *registryCache) storeModelChoices(tool string, choices choiceSet) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.modelChoices[tool] = choices
}

func (cache *registryCache) loadOfferingChoices(tool string) (choiceSet, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	choices, ok := cache.offeringChoices[tool]
	return choices, ok
}

func (cache *registryCache) storeOfferingChoices(tool string, choices choiceSet) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.offeringChoices[tool] = choices
}

func (cache *registryCache) loadOffered(scope string) (offeredTools, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	offered, ok := cache.offered[scope]
	return offered, ok
}

func (cache *registryCache) storeOffered(scope string, offered offeredTools) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.offered[scope] = offered
}
