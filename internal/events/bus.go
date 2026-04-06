package events

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/openaxiom/axiom/internal/state"
)

// subscriber holds a channel and optional filter for event fan-out.
type subscriber struct {
	ch     chan EngineEvent
	filter func(EngineEvent) bool
}

// Bus is the central event emitter. It persists authoritative events to SQLite
// and fans out all events (including view-model events) to in-memory subscribers.
type Bus struct {
	db      *state.DB
	log     *slog.Logger
	mu      sync.RWMutex
	writeMu sync.Mutex // serializes SQLite writes
	subs    map[uint64]subscriber
	nextID  uint64
}

// New creates a new event bus backed by the given database.
func New(db *state.DB, log *slog.Logger) *Bus {
	if log == nil {
		log = slog.Default()
	}
	return &Bus{
		db:   db,
		log:  log,
		subs: make(map[uint64]subscriber),
	}
}

// Subscribe registers a new subscriber. If filter is nil, all events are received.
// Returns the event channel and a subscription ID for unsubscribing.
func (b *Bus) Subscribe(filter func(EngineEvent) bool) (<-chan EngineEvent, uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := b.nextID
	b.nextID++

	ch := make(chan EngineEvent, 64)
	b.subs[id] = subscriber{ch: ch, filter: filter}
	return ch, id
}

// Unsubscribe removes a subscriber by ID. The channel is not closed;
// the subscriber simply stops receiving new events.
func (b *Bus) Unsubscribe(id uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subs, id)
}

// Publish emits an event. Authoritative events are persisted to SQLite.
// All events are fanned out to matching subscribers.
func (b *Bus) Publish(ev EngineEvent) error {
	if ev.Timestamp.IsZero() {
		ev.Timestamp = time.Now().UTC()
	}

	// Persist authoritative events to SQLite
	// Some phase-19 diagnostics are emitted before any run exists; those are
	// fanned out only and intentionally skipped from persistence.
	if !IsViewModelEvent(ev.Type) && ev.RunID != "" {
		if err := b.persist(ev); err != nil {
			b.log.Error("failed to persist event", "type", ev.Type, "error", err)
			return err
		}
	}

	// Fan out to all subscribers
	b.fanOut(ev)
	return nil
}

// persist writes an authoritative event to the SQLite events table.
// Serialized via writeMu to avoid SQLite busy errors under concurrency.
func (b *Bus) persist(ev EngineEvent) error {
	b.writeMu.Lock()
	defer b.writeMu.Unlock()

	dbEvent := &state.Event{
		RunID:     ev.RunID,
		EventType: string(ev.Type),
	}

	if ev.TaskID != "" {
		dbEvent.TaskID = &ev.TaskID
	}
	if ev.AgentType != "" {
		dbEvent.AgentType = &ev.AgentType
	}
	if ev.AgentID != "" {
		dbEvent.AgentID = &ev.AgentID
	}
	if ev.Details != nil {
		data, err := json.Marshal(ev.Details)
		if err != nil {
			return err
		}
		s := string(data)
		dbEvent.Details = &s
	}

	_, err := b.db.CreateEvent(dbEvent)
	return err
}

// fanOut sends the event to all matching subscribers without blocking.
func (b *Bus) fanOut(ev EngineEvent) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	for id, sub := range b.subs {
		if sub.filter != nil && !sub.filter(ev) {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			b.log.Warn("subscriber channel full, dropping event",
				"subscriber", id, "type", ev.Type)
		}
	}
}
