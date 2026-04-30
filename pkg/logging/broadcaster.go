// Package logging pkg/logging/broadcaster.go
package logging

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
)

// Broadcaster is a logrus.Hook that fans out entries to a set of
// subscriber channels. Subscribers register a Filter; only entries
// that pass the filter are delivered.
//
// The hook is on the hot logging path, so delivery is non-blocking:
// each subscriber owns a bounded channel, and entries are dropped on
// full rather than back-pressuring the producer. Drops are counted
// per-subscriber so the consumer can tell how lossy its window was.
type Broadcaster struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

// NewBroadcaster constructs an empty Broadcaster.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{subs: make(map[*subscription]struct{})}
}

// Filter selects which entries reach a subscriber.
//
// Module is matched as a prefix on the _module field (e.g. "router"
// matches "router", "router_setup", "router/foo"). Multiple modules
// are OR'd together. Empty Modules means "all modules".
//
// AppName, when set, also matches entries whose "app" field equals
// AppName, and entries whose _module equals AppName. Together with
// Modules this is OR-merged: an entry passes if either the module
// prefix or the app match fires.
//
// MinLevel is inclusive — entries strictly less severe than MinLevel
// are dropped. logrus levels descend from Panic (0) to Trace (6), so
// "less severe" means "higher numerically".
type Filter struct {
	AppName  string
	Modules  []string
	MinLevel logrus.Level
}

type subscription struct {
	filter  Filter
	ch      chan *logrus.Entry
	dropped uint64 // atomic
	closed  atomic.Bool
}

// Levels reports all logrus levels — Broadcaster does its own level
// filtering per-subscriber so the hook itself runs on every level.
func (b *Broadcaster) Levels() []logrus.Level {
	return logrus.AllLevels
}

// Fire delivers e to every subscriber whose filter matches. Never
// blocks on a slow consumer: full subscriber channels increment a
// drop counter and continue.
func (b *Broadcaster) Fire(e *logrus.Entry) error {
	b.mu.RLock()
	for sub := range b.subs {
		if !sub.matches(e) {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			atomic.AddUint64(&sub.dropped, 1)
		}
	}
	b.mu.RUnlock()
	return nil
}

// Subscribe returns a receive-only channel of matching entries plus a
// cancel func. The channel is closed when cancel is called. Capacity
// is the per-subscriber buffer — entries beyond it are dropped (see
// Dropped).
func (b *Broadcaster) Subscribe(f Filter, capacity int) (<-chan *logrus.Entry, func() uint64) {
	if capacity <= 0 {
		capacity = 256
	}
	if f.MinLevel == 0 {
		f.MinLevel = logrus.DebugLevel
	}
	sub := &subscription{
		filter: f,
		ch:     make(chan *logrus.Entry, capacity),
	}
	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	cancel := func() uint64 {
		if !sub.closed.CompareAndSwap(false, true) {
			return atomic.LoadUint64(&sub.dropped)
		}
		// Take the write lock so any in-flight Fire holding RLock has
		// finished iterating before we delete and close — no chance of
		// sending to a closed channel.
		b.mu.Lock()
		delete(b.subs, sub)
		b.mu.Unlock()
		close(sub.ch)
		return atomic.LoadUint64(&sub.dropped)
	}
	return sub.ch, cancel
}

func (s *subscription) matches(e *logrus.Entry) bool {
	if e.Level > s.filter.MinLevel {
		return false
	}

	module := ""
	if v, ok := e.Data[logModuleKey]; ok {
		if m, ok := v.(string); ok {
			module = m
		}
	}

	app := ""
	if v, ok := e.Data["app"]; ok {
		if a, ok := v.(string); ok {
			app = a
		}
	}

	if s.filter.AppName != "" {
		if app == s.filter.AppName || module == s.filter.AppName {
			return true
		}
	}

	if len(s.filter.Modules) == 0 {
		// No module filter: AppName-only mode.
		return s.filter.AppName == ""
	}

	for _, m := range s.filter.Modules {
		if m == "" {
			continue
		}
		if strings.HasPrefix(module, m) {
			return true
		}
	}
	return false
}
