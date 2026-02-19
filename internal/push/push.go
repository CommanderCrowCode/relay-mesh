package push

import "fmt"

// Adapter handles push delivery for a specific harness type.
type Adapter interface {
	// HarnessType returns the harness identifier (e.g., "opencode", "claude-code", "codex").
	HarnessType() string
	// Enabled returns whether this adapter is configured and ready.
	Enabled() bool
	// Push delivers a message notification to the target agent's session.
	Push(sessionID string, agentID string, msg Message) error
}

// Message is a minimal envelope for push delivery.
type Message struct {
	ID        string
	From      string
	To        string
	Body      string
	CreatedAt string
}

// Registry holds adapters indexed by harness type and dispatches push calls.
type Registry struct {
	adapters map[string]Adapter
}

// NewRegistry returns an empty Registry ready for adapter registration.
func NewRegistry() *Registry {
	return &Registry{adapters: make(map[string]Adapter)}
}

// Register adds an adapter to the registry, keyed by its HarnessType.
func (r *Registry) Register(a Adapter) {
	r.adapters[a.HarnessType()] = a
}

// Push dispatches a push to the adapter matching the given harness type.
func (r *Registry) Push(harness, sessionID, agentID string, msg Message) error {
	a, ok := r.adapters[harness]
	if !ok {
		return fmt.Errorf("unknown harness type: %s", harness)
	}
	if !a.Enabled() {
		return nil
	}
	return a.Push(sessionID, agentID, msg)
}

// PushAny tries all enabled adapters and returns the first error encountered.
func (r *Registry) PushAny(sessionID, agentID string, msg Message) error {
	for _, a := range r.adapters {
		if !a.Enabled() {
			continue
		}
		if err := a.Push(sessionID, agentID, msg); err != nil {
			return fmt.Errorf("%s push: %w", a.HarnessType(), err)
		}
	}
	return nil
}
