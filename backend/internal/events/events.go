// Package events is how the parts of the server talk to each other without
// reaching into each other's tables.
//
// A capability never reads another capability's storage. It publishes what
// happened, and whoever cares listens — that is what keeps the automation
// engine, the variable collector and the capabilities separable.
package events

import (
	"sync"
	"time"

	"github.com/google/uuid"
)

const (
	// FileChanged is published for every write, move or delete in a project.
	FileChanged = "file.changed"
	// SchedulerFinished is published after every scheduler run.
	SchedulerFinished = "scheduler.finished"
	// VariableChanged is published when a project's variable takes a new value.
	VariableChanged = "variable.changed"
	// ProjectChanged covers create, rename, move, capability changes.
	ProjectChanged = "project.changed"
	// PushReceived is published after a git push into a group's repository.
	PushReceived = "git.pushed"
)

type Event struct {
	Kind      string         `json:"kind"`
	ProjectID uuid.UUID      `json:"projectId"`
	Path      string         `json:"path,omitempty"`
	Detail    map[string]any `json:"detail,omitempty"`
	At        time.Time      `json:"at"`
}

type Handler func(Event)

type Bus struct {
	mu       sync.RWMutex
	next     int
	handlers map[int]Handler
}

func NewBus() *Bus { return &Bus{handlers: map[int]Handler{}} }

// Subscribe returns the way to stop listening again.
//
// Most listeners are set up once at startup and never leave, and they may
// ignore what comes back. A browser watching the stream is the other case:
// every open page adds one, and without a way off the bus a day of reloads
// would leave a day of handlers behind.
func (b *Bus) Subscribe(h Handler) (stop func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.handlers == nil {
		b.handlers = map[int]Handler{}
	}
	b.next++
	id := b.next
	b.handlers[id] = h
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		delete(b.handlers, id)
	}
}

// Publish hands the event to every listener. Listeners run in their own
// goroutine: a slow automation must not hold up a file write.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	handlers := make([]Handler, 0, len(b.handlers))
	for _, h := range b.handlers {
		handlers = append(handlers, h)
	}
	b.mu.RUnlock()
	for _, h := range handlers {
		go h(e)
	}
}
