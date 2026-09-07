// Package api pkg/deployment/ar/api/dial_assist_log.go c4-net-discovery
//
// Rate accounting for the SUDPH reverse dial-assist, whose "peer is not
// connected" outcome used to be logged once per attempt.
//
// On a /resolve of type sudph the AR also nudges the RECEIVER to dial the
// sender, so both sides punch at once. That nudge needs a live UDP control
// connection from the receiver; when the receiver is offline (or its AR
// record is stale) there is none and askToDialUDP returns ErrNotConnected.
// The sender's /resolve response has already been written at that point —
// nothing is lost but the simultaneous-open optimisation, and the sender
// still dials. It is a routine outcome of a network where visors come and go.
//
// Measured on the live deployment it was also 94,752 log lines in ten
// minutes — ~9,475/min against 2,450/min for the entire transport-discovery
// and 38/min for the route-finder. That is a real disk and IO cost, and it
// buried every other address-resolver diagnostic.
//
// A per-peer rate limit was rejected: the cardinality IS the signal here
// (thousands of distinct offline peers), so per-peer state would grow with
// the fleet while still emitting a line per newly-seen peer. A plain demotion
// to Debug was rejected too: it makes the condition invisible at the level
// operators actually run, and the RATE is what tells them whether something
// is wrong (a jump from ~9k/min to ~90k/min matters; the individual line does
// not). Aggregation keeps the rate, the distinct-peer count and the worst
// offenders, at one line per window; the individual event stays available at
// Debug for anyone who raises the level.
package api

import (
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// dialAssistSummaryInterval is the window over which skipped dial-assist
// requests are counted before one summary line is emitted.
const dialAssistSummaryInterval = time.Minute

// dialAssistTopPeers is how many of the worst offenders the summary names.
const dialAssistTopPeers = 5

// dialAssistFailures counts dial-assist requests skipped because the peer had
// no live UDP connection, and hands back a summary once per interval.
//
// Flushing is lazy — the window is closed by the next recorded failure rather
// than by a timer — so there is no goroutine to own or shut down. The trailing
// partial window is therefore only reported when the next failure arrives; at
// the observed rate (thousands per minute) that is immediate, and when the
// rate really does fall to zero there is nothing left to warn about.
type dialAssistFailures struct {
	mu       sync.Mutex
	interval time.Duration
	since    time.Time
	count    int
	peers    map[cipher.PubKey]int
}

// dialAssistSummary is one closed window's worth of skipped dial-assists.
type dialAssistSummary struct {
	// Window is the elapsed time the counts cover.
	Window time.Duration
	// Count is the number of skipped dial-assist requests.
	Count int
	// Peers is the number of distinct peers they were addressed to.
	Peers int
	// TopPeers names the worst offenders as "<pk>=<count>", full keys.
	TopPeers []string
}

func newDialAssistFailures(interval time.Duration) *dialAssistFailures {
	return &dialAssistFailures{
		interval: interval,
		peers:    make(map[cipher.PubKey]int),
	}
}

// record notes one skipped dial-assist for peer. It returns a summary of the
// window that just closed, or nil when the window is still open.
func (d *dialAssistFailures) record(now time.Time, peer cipher.PubKey) *dialAssistSummary {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.since.IsZero() {
		d.since = now
	}
	d.count++
	d.peers[peer]++

	if now.Sub(d.since) < d.interval {
		return nil
	}

	summary := &dialAssistSummary{
		Window:   now.Sub(d.since),
		Count:    d.count,
		Peers:    len(d.peers),
		TopPeers: topPeers(d.peers, dialAssistTopPeers),
	}

	d.since = now
	d.count = 0
	d.peers = make(map[cipher.PubKey]int)

	return summary
}

// topPeers renders the n highest-count peers as "<pk>=<count>". Public keys
// are never abbreviated — an operator has to be able to paste one straight
// into `skywire cli`.
func topPeers(counts map[cipher.PubKey]int, n int) []string {
	type entry struct {
		pk cipher.PubKey
		n  int
	}
	entries := make([]entry, 0, len(counts))
	for pk, c := range counts {
		entries = append(entries, entry{pk: pk, n: c})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].n != entries[j].n {
			return entries[i].n > entries[j].n
		}
		return entries[i].pk.Hex() < entries[j].pk.Hex()
	})
	if len(entries) > n {
		entries = entries[:n]
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.pk.Hex()+"="+strconv.Itoa(e.n))
	}
	return out
}
