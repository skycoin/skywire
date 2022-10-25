// Package appevent pkg/app/appevent/subscriber.go
package appevent

import (
	"errors"
	"sync"
)

// subChanSize is used so that incoming events are kept in order
const subChanSize = 5

// Errors associated with the Subscriber type.
var (
	ErrSubscriptionsClosed = errors.New("event subscriptions is closed")
)

// Subscriber is used by apps and contain subscription channels to different event types.
type Subscriber struct {
	chanSize int // config: event channel size

	m      map[string]chan *Event
	mx     sync.RWMutex
	closed bool
}

// NewSubscriber returns a new Subscriber struct.
func NewSubscriber() *Subscriber {
	return &Subscriber{
		chanSize: subChanSize,
		m:        make(map[string]chan *Event),
		closed:   false,
	}
}

// OnTCPDial subscribes to the OnTCPDial event channel (if not already).
// And triggers the contained action func on each subsequent event.
func (s *Subscriber) OnTCPDial(action func(data TCPDialData)) {
	evCh := s.ensureEventChan(TCPDial)

	go func() {
		for ev := range evCh {
			var data TCPDialData
			ev.Unmarshal(&data)
			action(data)
			ev.Done()
		}
	}()
}

// OnTCPClose subscribes to the OnTCPClose event channel (if not already).
// And triggers the contained action func on each subsequent event.
func (s *Subscriber) OnTCPClose(action func(data TCPCloseData)) {
	evCh := s.ensureEventChan(TCPClose)

	go func() {
		for ev := range evCh {
			var data TCPCloseData
			ev.Unmarshal(&data)
			action(data)
			ev.Done()
		}
	}()
}

func (s *Subscriber) ensureEventChan(eventType string) chan *Event {
	s.mx.Lock()
	ch, ok := s.m[eventType]
	if !ok {
		ch = make(chan *Event, s.chanSize)
		s.m[eventType] = ch
	}
	s.mx.Unlock()

	return ch
}

// Subscriptions returns a map of all subscribed event types.
func (s *Subscriber) Subscriptions() map[string]bool {
	s.mx.RLock()
	subs := make(map[string]bool, len(s.m))
	for t := range s.m {
		subs[t] = true
	}
	s.mx.RUnlock()

	return subs
}

// Count returns the number of subscriptions.
func (s *Subscriber) Count() int {
	s.mx.RLock()
	n := len(s.m)
	s.mx.RUnlock()
	return n
}

// Close implements io.Closer
func (s *Subscriber) Close() error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if s.closed {
		return ErrSubscriptionsClosed
	}

	for _, ch := range s.m {
		close(ch)
	}
	s.m = nil

	return nil
}

// PushEvent pushes an event to the relevant subscription channel.
func PushEvent(s *Subscriber, e *Event) error {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.closed {
		return ErrSubscriptionsClosed
	}

	if ch, ok := s.m[e.Type]; ok {
		e.InitDone()
		ch <- e
		e.Wait() // wait until event is fully handled by app before returning
	}

	return nil
}
