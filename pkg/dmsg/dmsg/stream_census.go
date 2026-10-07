//go:build !tinygo

package dmsg

import (
	"sort"
	"sync"
	"time"
	"weak"
)

// streamCensus holds a weak reference to every stream this process made, so
// a stream that stays reachable after it is finished shows up by port and state.
var streamCensus = struct {
	mu sync.Mutex
	m  map[*censusEntry]weak.Pointer[Stream]
}{m: make(map[*censusEntry]weak.Pointer[Stream])}

type censusEntry struct {
	mu        sync.Mutex
	initiator bool
	port      uint16
	state     string
	created   time.Time
}

func censusTrack(s *Stream, initiator bool) {
	if s == nil {
		return
	}
	e := &censusEntry{initiator: initiator, state: "open", created: time.Now()}
	s.census.Store(e)
	streamCensus.mu.Lock()
	streamCensus.m[e] = weak.Make(s)
	streamCensus.mu.Unlock()
}

func (e *censusEntry) set(port uint16, state string) {
	if e == nil {
		return
	}
	e.mu.Lock()
	if port != 0 {
		e.port = port
	}
	if state != "" {
		e.state = state
	}
	e.mu.Unlock()
}

// StreamCensusRow counts the streams still in memory that share a direction,
// a service port and a lifecycle state.
type StreamCensusRow struct {
	Initiator bool    `json:"initiator"`
	Port      uint16  `json:"port"`
	State     string  `json:"state"`
	Count     int     `json:"count"`
	OldestS   float64 `json:"oldest_s"`
}

// StreamCensus drops the entries of collected streams and groups the rest.
func StreamCensus() []StreamCensusRow {
	type key struct {
		init  bool
		port  uint16
		state string
	}
	now := time.Now()
	rows := map[key]*StreamCensusRow{}
	streamCensus.mu.Lock()
	for e, wp := range streamCensus.m {
		if wp.Value() == nil {
			delete(streamCensus.m, e)
			continue
		}
		e.mu.Lock()
		k := key{e.initiator, e.port, e.state}
		age := now.Sub(e.created).Seconds()
		e.mu.Unlock()
		r := rows[k]
		if r == nil {
			r = &StreamCensusRow{Initiator: k.init, Port: k.port, State: k.state}
			rows[k] = r
		}
		r.Count++
		if age > r.OldestS {
			r.OldestS = age
		}
	}
	streamCensus.mu.Unlock()
	out := make([]StreamCensusRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, *r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Count > out[j].Count })
	return out
}
