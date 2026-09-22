package core

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds every registered module in declaration order.
//
// Registration is explicit (`Register`) rather than init()-based so that the
// build stays analyzable and module startup order is deterministic.
type Registry struct {
	mu      sync.RWMutex
	modules map[string]Module
	order   []string
}

// NewRegistry creates an empty module registry.
func NewRegistry() *Registry {
	return &Registry{modules: map[string]Module{}}
}

// Register adds a module. Registering the same id twice panics at boot,
// which is a programming error rather than a runtime condition.
func (r *Registry) Register(m Module) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := m.ID()
	if id == "" {
		return fmt.Errorf("core: module with empty id")
	}
	if _, dup := r.modules[id]; dup {
		return fmt.Errorf("core: duplicate module id %q", id)
	}
	r.modules[id] = m
	r.order = append(r.order, id)
	return nil
}

// MustRegister panics on registration failure (boot-time only).
func (r *Registry) MustRegister(m Module) {
	if err := r.Register(m); err != nil {
		panic(err)
	}
}

// Get returns a module by id.
func (r *Registry) Get(id string) (Module, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	m, ok := r.modules[id]
	return m, ok
}

// All returns every module in registration order.
func (r *Registry) All() []Module {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Module, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.modules[id])
	}
	return out
}

// IDs returns the registered module ids in registration order.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// SortedIDs returns module ids sorted alphabetically (used for stable output).
func (r *Registry) SortedIDs() []string {
	ids := r.IDs()
	sort.Strings(ids)
	return ids
}
