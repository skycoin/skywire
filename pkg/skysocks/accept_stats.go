// Package skysocks pkg/skysocks/accept_stats.go c4-app-proxy
//
// Accounting for the SOCKS accept path — the stage BEFORE a stream is tracked.
//
// The per-stream table (streamSnapshot) only starts at handleStream, which is
// reached after Accept -> pickSession -> Open -> the SOCKS sniff. A connection
// that dies earlier leaves no row anywhere, so "the browser got nothing and the
// stream table is empty" was indistinguishable from "no browser ever
// connected". Measured in the wasm desk: six concurrent SOCKS dials, three
// closed with a network error and three never progressed, and the stream table
// showed zero rows the whole time — which said only that handleStream was never
// reached, not why.
//
// These counters close that gap: they say how many connections were accepted,
// how many found no live tunnel to put a stream on, how many failed to open one,
// and how long the slowest open took.
package skysocks

import (
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/proxystatus"
)

// acceptStats counts the accept path's outcomes. Every field is written from
// the accept loop or from one connection's goroutine and read by the status
// snapshot, so all of it is atomic.
type acceptStats struct {
	accepted atomic.Uint64
	pickNil  atomic.Uint64
	openErr  atomic.Uint64
	opened   atomic.Uint64
	// maxOpenNanos is the longest a single Open took. Open used to run inline
	// in the accept loop, where one slow open stalled every other pending
	// connection; this is what says whether opens are slow at all.
	maxOpenNanos atomic.Int64
	lastOpenErr  atomic.Value // string
}

// observeOpen records one completed Open attempt.
func (a *acceptStats) observeOpen(d time.Duration, err error) {
	if n := d.Nanoseconds(); n > 0 {
		for {
			prev := a.maxOpenNanos.Load()
			if n <= prev || a.maxOpenNanos.CompareAndSwap(prev, n) {
				break
			}
		}
	}
	if err != nil {
		a.openErr.Add(1)
		a.lastOpenErr.Store(err.Error())
		return
	}
	a.opened.Add(1)
}

// snapshot renders the counters for the status surface. It returns nil before
// anything has been accepted, so the section is absent on an idle proxy rather
// than a row of zeros.
func (a *acceptStats) snapshot() *proxystatus.Accept {
	accepted := a.accepted.Load()
	if accepted == 0 {
		return nil
	}
	out := &proxystatus.Accept{
		Accepted:    accepted,
		NoTunnel:    a.pickNil.Load(),
		OpenFailed:  a.openErr.Load(),
		Opened:      a.opened.Load(),
		MaxOpenMS:   float64(a.maxOpenNanos.Load()) / float64(time.Millisecond),
		LastOpenErr: "",
	}
	if s, ok := a.lastOpenErr.Load().(string); ok {
		out.LastOpenErr = s
	}
	return out
}
