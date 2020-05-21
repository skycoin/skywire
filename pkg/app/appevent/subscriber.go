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

// TCPDial subscribes to the TCPDial event channel.
// This should only be called once.
func (s *Subscriber) TCPDial(action func(data TCPDialData)) {
	s.mx.Lock()
	ch := make(chan *Event, s.chanSize)
	s.m[TCPDial] = ch
	s.mx.Unlock()

	go func() {
		for ev := range ch {
			var data TCPDialData
			ev.Unmarshal(&data)
			action(data)
			ev.Done()
		}
	}()
}

// TCPClose subscribes to the TCPClose event channel.
// This should only be called once.
func (s *Subscriber) TCPClose(action func(data TCPCloseData)) {
	s.mx.Lock()
	ch := make(chan *Event, s.chanSize)
	s.m[TCPClose] = ch
	s.mx.Unlock()

	go func() {
		for ev := range ch {
			var data TCPCloseData
			ev.Unmarshal(&data)
			action(data)
			ev.Done()
		}
	}()
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
