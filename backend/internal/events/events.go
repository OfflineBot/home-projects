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
	handlers []Handler
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe(h Handler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.handlers = append(b.handlers, h)
}

// Publish hands the event to every listener. Listeners run in their own
// goroutine: a slow automation must not hold up a file write.
func (b *Bus) Publish(e Event) {
	if e.At.IsZero() {
		e.At = time.Now()
	}
	b.mu.RLock()
	handlers := make([]Handler, len(b.handlers))
	copy(handlers, b.handlers)
	b.mu.RUnlock()
	for _, h := range handlers {
		go h(e)
	}
}
