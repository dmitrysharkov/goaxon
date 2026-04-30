package event

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Registry maps event type names to JSON unmarshallers. Persistent stores
// (Postgres, etc.) use it to reconstruct concrete Event values from stored
// payloads — the in-memory store keeps Go values directly and doesn't need
// a registry.
//
// Registration is by generic Register[T]; one Registry per process is
// usually enough.
type Registry struct {
	mu           sync.RWMutex
	unmarshalers map[string]func([]byte) (Event, error)
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{unmarshalers: make(map[string]func([]byte) (Event, error))}
}

// Register binds the JSON-encoded form of T to T's EventType. Panics on
// duplicate registration — silently overwriting would mask domain bugs
// the same way duplicate command handlers would.
//
// T's zero value is used to read EventType, so EventType must be a
// value-receiver method (or a pointer-receiver method on a pointer T).
func Register[T Event](r *Registry) {
	var zero T
	name := zero.EventType()

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.unmarshalers[name]; exists {
		panic(fmt.Sprintf("event: type %q already registered", name))
	}
	r.unmarshalers[name] = func(data []byte) (Event, error) {
		var v T
		if err := json.Unmarshal(data, &v); err != nil {
			return nil, fmt.Errorf("event: unmarshal %s: %w", name, err)
		}
		return v, nil
	}
}

// Unmarshal decodes data into the Event registered for eventType.
// Returns an error if the type is unknown.
func (r *Registry) Unmarshal(eventType string, data []byte) (Event, error) {
	r.mu.RLock()
	fn, ok := r.unmarshalers[eventType]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("event: type %q not registered", eventType)
	}
	return fn(data)
}
