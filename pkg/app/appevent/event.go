// Package appevent pkg/app/appevent/event.go
package appevent

import (
	"encoding/json"
)

// Event represents an event that is to be broadcasted.
type Event struct {
	Type string
	Data []byte
	done chan struct{} // to be closed once event is dealt with
}

// NewEvent creates a new Event.
func NewEvent(t string, v interface{}) *Event {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err) // should never happen
	}
	return &Event{Type: t, Data: data}
}

// Unmarshal unmarshals the event data to a given object.
func (e *Event) Unmarshal(v interface{}) {
	if err := json.Unmarshal(e.Data, v); err != nil {
		panic(err) // should never happen
	}
}

// InitDone enables the Done/Wait logic.
func (e *Event) InitDone() { e.done = make(chan struct{}) }

// Done informs that event is handled.
func (e *Event) Done() { close(e.done) }

// Wait waits until event is handled.
func (e *Event) Wait() { <-e.done }
