// Package commands cmd/apps/skychat/commands/presence.go c4-app-chat
//
// Peer presence (the online/offline dot next to a contact).
//
// A peer is "online" when its VISOR answers over dmsg right now. The probe is
// the visor's own dmsg-ping surface (DialDmsgPing → DmsgPingOnce →
// StopDmsgPing, relayed through the pairRPCCall seam like the group and voice
// endpoints), so nothing new is needed on the peer's side and no route is set
// up — a dmsg stream to the peer's visor either establishes or it doesn't.
//
// Probing happens in a BACKGROUND loop, never in the request path: dialing an
// offline peer can take seconds, and a browser poll must not wait for it. The
// UI posts the contact list it cares about, gets the latest snapshot back
// immediately, and asks again a minute later. Endpoints 503 without
// --pair-enable (no visor RPC to relay through), so the UI just hides the dots.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

const (
	// presenceRefresh is how often the background loop re-probes the watch set.
	// Matches the UI's poll interval; the browser reads whatever the last sweep
	// left behind.
	presenceRefresh = 60 * time.Second

	// presenceProbeTimeout bounds one peer's dial+ping. The visor's RPC client
	// has its own 30s ceiling; this keeps a single unreachable peer from eating
	// the whole sweep.
	presenceProbeTimeout = 8 * time.Second

	// presenceWorkers is how many peers are probed concurrently. Small on
	// purpose: each probe opens a dmsg stream, and a contact list is not a
	// load-test target.
	presenceWorkers = 4

	// presenceMaxWatch caps the watch set so a runaway client can't turn this
	// into an unbounded dialer. Dropped entries simply report unknown.
	presenceMaxWatch = 64

	// presenceStale is how long a result stays reportable. Past this the UI is
	// told "unknown" rather than a stale online/offline.
	presenceStale = 5 * time.Minute
)

// peerPresence is one peer's last probe result.
type peerPresence struct {
	Online bool  `json:"online"`
	RTTMs  int64 `json:"rtt_ms,omitempty"`
	At     int64 `json:"at"` // unix seconds of the probe
}

// presenceStore holds the watch set and the last sweep's results.
type presenceStore struct {
	mu    sync.Mutex
	watch []cipher.PubKey
	seen  map[cipher.PubKey]peerPresence
	// sweeping guards against overlapping sweeps: an unreachable peer can hold
	// a probe for seconds, so a long sweep must not have the next tick (or a
	// new-contact POST) start a second one alongside it.
	sweeping bool
	// probe is the per-peer prober. A field (not a direct call) so tests can
	// drive the store without a visor.
	probe func(cipher.PubKey) peerPresence
}

var presence = &presenceStore{
	seen:  make(map[cipher.PubKey]peerPresence),
	probe: probePeerViaVisor,
}

// setWatch replaces the watch set (the UI is the source of truth for which
// contacts matter) and reports whether anything is new, so a fresh contact can
// be probed without waiting for the next tick.
func (p *presenceStore) setWatch(pks []cipher.PubKey) bool {
	if len(pks) > presenceMaxWatch {
		pks = pks[:presenceMaxWatch]
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	fresh := false
	for _, pk := range pks {
		if _, ok := p.seen[pk]; !ok {
			fresh = true
			break
		}
	}
	p.watch = pks
	return fresh
}

func (p *presenceStore) watchSet() []cipher.PubKey {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]cipher.PubKey, len(p.watch))
	copy(out, p.watch)
	return out
}

func (p *presenceStore) record(pk cipher.PubKey, res peerPresence) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.seen[pk] = res
	// Forget peers that dropped off the watch set, so the map tracks the
	// contact list rather than growing for the process's lifetime.
	if len(p.seen) > presenceMaxWatch*2 {
		keep := make(map[cipher.PubKey]bool, len(p.watch))
		for _, w := range p.watch {
			keep[w] = true
		}
		for k := range p.seen {
			if !keep[k] {
				delete(p.seen, k)
			}
		}
	}
}

// snapshot renders the results for the requested peers, omitting anything too
// old to stand behind.
func (p *presenceStore) snapshot(pks []cipher.PubKey) map[string]peerPresence {
	cutoff := time.Now().Add(-presenceStale).Unix()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string]peerPresence, len(pks))
	for _, pk := range pks {
		if res, ok := p.seen[pk]; ok && res.At >= cutoff {
			out[pk.Hex()] = res
		}
	}
	return out
}

// sweep probes every watched peer, presenceWorkers at a time. A no-op while
// another sweep is still running.
func (p *presenceStore) sweep(ctx context.Context) {
	pks := p.watchSet()
	if len(pks) == 0 {
		return
	}
	p.mu.Lock()
	if p.sweeping {
		p.mu.Unlock()
		return
	}
	p.sweeping = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.sweeping = false
		p.mu.Unlock()
	}()
	jobs := make(chan cipher.PubKey)
	var wg sync.WaitGroup
	for range presenceWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pk := range jobs {
				p.record(pk, p.probe(pk))
			}
		}()
	}
	for _, pk := range pks {
		select {
		case <-ctx.Done():
			close(jobs)
			wg.Wait()
			return
		case jobs <- pk:
		}
	}
	close(jobs)
	wg.Wait()
}

// probePeerViaVisor dials the peer's visor over dmsg and times one ping. A
// successful dial is itself proof of life, so a peer whose ping round-trip
// fails still counts as online — just without a latency to show.
func probePeerViaVisor(pk cipher.PubKey) peerPresence {
	res := peerPresence{At: time.Now().Unix()}
	if err := pairRPCCall("DialDmsgPing", func(c visor.API) error {
		return c.DialDmsgPing(pk)
	}); err != nil {
		return res
	}
	defer func() {
		_ = pairRPCCall("StopDmsgPing", func(c visor.API) error { //nolint:errcheck
			return c.StopDmsgPing(pk)
		})
	}()
	res.Online = true

	start := time.Now()
	var rtt time.Duration
	if err := pairRPCCall("DmsgPingOnce", func(c visor.API) error {
		var perr error
		rtt, perr = c.DmsgPingOnce(visor.PingConfig{PK: pk, Tries: 1})
		return perr
	}); err != nil {
		return res
	}
	if rtt <= 0 || rtt > presenceProbeTimeout {
		rtt = time.Since(start) // the visor reported nothing usable; time it ourselves
	}
	res.RTTMs = rtt.Milliseconds()
	return res
}

// startPresenceLoop runs the background sweeps until ctx is done. No-op
// without --pair-enable: there is no visor RPC to probe through.
func startPresenceLoop(ctx context.Context) {
	if !pairEnable {
		return
	}
	go func() {
		t := time.NewTicker(presenceRefresh)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				presence.sweep(ctx)
			}
		}
	}()
}

// registerPresenceHTTPHandlers wires /presence onto mux. No-op when
// --pair-enable is off, so the endpoint 404s and the UI hides the dots.
func registerPresenceHTTPHandlers(mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/presence", requireAuthFunc(presenceHandler()))
}

// presenceHandler serves the presence snapshot.
//
//	POST /presence {"pks": ["<hex>", ...]}  -> {"peers": {"<hex>": {...}}}
//
// The posted list becomes the watch set the background loop probes. GET
// returns the current snapshot for the existing watch set, which is handy for
// debugging but not what the UI uses.
func presenceHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Same seam as the voice proxy: no visor RPC, nothing to probe through.
		if !pairRPCAlive() {
			http.Error(w, "presence disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		var pks []cipher.PubKey
		switch r.Method {
		case http.MethodGet:
			pks = presence.watchSet()
		case http.MethodPost:
			var body struct {
				PKs []string `json:"pks"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
			pks = parsePresencePKs(body.PKs)
			// A newly added contact shouldn't sit blank until the next tick.
			if presence.setWatch(pks) {
				go presence.sweep(context.Background())
			}
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
			return
		}
		writeJSON(w, map[string]any{
			"peers":      presence.snapshot(pks),
			"refresh_ms": presenceRefresh.Milliseconds(),
		})
	}
}

// parsePresencePKs turns the posted hex strings into keys, dropping anything
// malformed (a contact list can carry junk) and de-duplicating. Sorted so the
// watch set — and therefore the sweep order — is stable across polls.
func parsePresencePKs(in []string) []cipher.PubKey {
	seen := make(map[cipher.PubKey]bool, len(in))
	out := make([]cipher.PubKey, 0, len(in))
	for _, s := range in {
		var pk cipher.PubKey
		if err := pk.Set(strings.TrimSpace(s)); err != nil || pk.Null() {
			continue
		}
		if seen[pk] {
			continue
		}
		seen[pk] = true
		out = append(out, pk)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Hex() < out[j].Hex() })
	return out
}
