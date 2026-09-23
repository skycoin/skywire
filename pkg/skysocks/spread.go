// Package skysocks pkg/skysocks/spread.go — the spread policy.
//
// The object under a range-split download or a striped upload is a set of
// independent chunks, and every chunk is placed on one tunnel. Who places them
// is this file. Today's placement is pickSessionFor(pickRecv): a per-STREAM
// rule — fewest streams, weighted by proven capacity — which knows nothing
// about the object those streams belong to. That is why a 3 MB/s tunnel beside
// an 8 MB/s one drags the pair below the fast tunnel alone: the picker hands
// each of them an equal number of streams, so the object finishes at the pace
// of the slow half of it.
//
// spreadPlanner is the per-OBJECT view the picker lacks. It keeps a ledger of
// the bytes each tunnel has carried for this object and answers "which tunnel
// next" from it, so the shares converge on the tunnels' measured capacities
// instead of on their stream counts — and so a share can be CAPPED, which is
// what criterion 10 asks for and what a privacy-driven spread needs: no single
// route sees most of an object.
//
// The knobs (docs/design/route-spread-policy.md) all default to OFF. With
// nothing set the planner still keeps the ledger — that is where the
// `shares=` line in a completion log comes from — but it leaves the choice to
// pickSessionFor, so an unset client places chunks byte for byte as it does
// today.
package skysocks

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// spreadDir is which direction's capacity the planner weighs. A tunnel's
// upload and download capacities are measured separately (tunnelMeter.sample
// decays a direction only when that direction moved bytes), so a striped
// upload must be planned on the upload estimate — planning it on the download
// one is the #5002 bug in a new place.
type spreadDir int

const (
	spreadDown spreadDir = iota // range-split chunks: the exit sends
	spreadUp                    // striped-upload chunks: we send
)

// spreadPolicy is the four knobs read as one, once per object.
type spreadPolicy struct {
	// maxShare is the largest fraction of the object one tunnel may carry;
	// 1 (the default) caps nothing.
	maxShare float64
	// minRoutes is how many ACTIVE tunnels the object must be spread over.
	// 0 (the default) asks for no floor. It is reached by promoting standby
	// tunnels, never by dialing: the discovered pool is the ceiling.
	minRoutes int
	// endgame duplicates the tail chunks on the fastest idle tunnel.
	endgame bool
	// even weighs every tunnel alike instead of by measured capacity — the
	// privacy end of the performance↔privacy spectrum.
	even bool
}

// spreadPolicyNow reads the knobs.
func spreadPolicyNow() spreadPolicy {
	return spreadPolicy{
		maxShare:  setSpreadMaxShare(),
		minRoutes: setSpreadMinRoutes(),
		endgame:   setSpreadEndgame(),
		even:      setSpreadWeight() == skysettings.SpreadWeightEven,
	}
}

// steers reports whether the policy changes any placement. When it does not,
// the planner keeps its ledger but defers to pickSessionFor, so "off" is the
// old code path and not a re-implementation of it.
func (p spreadPolicy) steers() bool {
	return p.capped() || p.minRoutes > 0 || p.endgame || p.even
}

// capped reports whether max_share bounds anything.
func (p spreadPolicy) capped() bool { return p.maxShare > 0 && p.maxShare < 1 }

// spreadChoose is the whole placement rule, as a pure function of what each
// tunnel has been MEASURED able to carry (capsBps, bytes/s, 0 = never
// measured), what its transports say it SHOULD manage (priorBps, bytes/s, 0 =
// no prior — see tunnel_prior.go), and what it has carried for THIS object
// (carried, bytes). It returns the index of the tunnel the next chunk belongs
// on, or -1 when there is nothing to place it on.
//
// Three lines:
//
//  1. Every tunnel gets a WEIGHT: its measured capacity (weight=rate) or 1
//     (weight=even). A tunnel with no measurement but a PRIOR is weighed by
//     its prior. A tunnel with neither is a PROBE: it weighs the smallest
//     weight present and may take exactly ONE chunk of the object, after which
//     it is skipped until a measurement re-weighs it. It is never credited the
//     best weight present — that was the old rule (the #4965 "probe rather
//     than starve" rule applied to shares), and it is what let a standby that
//     had never carried a byte be handed the fattest share of an object.
//  2. A tunnel whose share of the bytes so far is at or above max_share is
//     SKIPPED, but only while another tunnel is under its cap: a cap must
//     never be a reason to stall an object. Neither may the probe bound: with
//     nothing else eligible, every tunnel is.
//  3. Among what is left, the tunnel furthest BELOW its weight — smallest
//     carried/weight — takes the chunk; ties go to the tunnel with the most
//     MEASURED capacity, and only then to the lowest index. The first chunk of
//     an object is one big tie (nothing is carried yet), and the lowest index
//     is the tunnel the sessions happen to have been added in — on the rig,
//     the direct one — so an index-only tie-break aims every opening chunk at
//     the same route. An unmeasured tunnel loses every tie by construction,
//     which is what keeps a probe out of the opening chunks.
//
// Rule 3 alone is the fix for "the slow tunnel drags the pair below itself":
// at 8 and 3 MB/s the ratio settles at 8:3, so the slow tunnel gets fewer
// chunks rather than an equal number of them.
func spreadChoose(capsBps, priorBps []float64, carried []int64, p spreadPolicy) int {
	if len(capsBps) == 0 || len(capsBps) != len(carried) {
		return -1
	}
	// One row per tunnel, so the weight, the bytes and the eligibility are
	// never indexed out of four separate slices.
	type lane struct {
		weight   float64
		measured float64
		carried  int64
		probe    bool
		eligible bool
	}
	lanes := make([]lane, len(capsBps))
	// 1. weights. The smallest positive weight is what a probe lane borrows:
	// a route nothing is known about is worth no more than the least of the
	// routes something IS known about.
	least := 0.0
	for i, c := range capsBps {
		w := c
		if w <= 0 && i < len(priorBps) {
			w = priorBps[i]
		}
		lanes[i] = lane{weight: w, measured: c, carried: carried[i], probe: w <= 0, eligible: true}
		if p.even && w > 0 {
			lanes[i].weight = 1
		}
		if lanes[i].weight > 0 && (least == 0 || lanes[i].weight < least) {
			least = lanes[i].weight
		}
	}
	if least <= 0 {
		least = 1 // nothing is known about anything: one weight fits all
	}
	// 2. the cap and the probe bound, applied against the bytes placed so far.
	var total int64
	for _, l := range lanes {
		total += l.carried
	}
	eligible := 0
	for i := range lanes {
		if lanes[i].probe {
			lanes[i].weight = least
			// The one probe chunk: once it has carried anything for this
			// object it sits out until a measurement gives it a real weight.
			if lanes[i].carried > 0 {
				lanes[i].eligible = false
				continue
			}
		}
		if p.capped() && total > 0 && float64(lanes[i].carried) >= p.maxShare*float64(total) {
			lanes[i].eligible = false
			continue
		}
		eligible++
	}
	// 3. the deepest deficit wins. With nothing eligible — every tunnel at its
	// cap, or every tunnel a spent probe — the bounds are ignored: neither may
	// ever be a reason to stall an object.
	idx := -1
	bestScore, bestCap := 0.0, 0.0
	for i, l := range lanes {
		if !l.eligible && eligible > 0 {
			continue
		}
		score := float64(l.carried) / l.weight
		if idx == -1 || score < bestScore || (score == bestScore && l.measured > bestCap) {
			idx, bestScore, bestCap = i, score, l.measured
		}
	}
	return idx
}

// spreadDuplicates is the endgame test: the tail of an object is the part where
// one slow tunnel decides when the whole thing finishes, because there is no
// later chunk to fill the fast tunnels with. Once fewer chunks remain than
// there are tunnels to carry them, a duplicate of each costs bytes that would
// otherwise not be sent at all and removes the slow tunnel from the critical
// path. Bittorrent calls this the endgame; the bound here is the same one it
// uses — the tail only, and at most one duplicate per chunk.
func spreadDuplicates(remaining, active int, p spreadPolicy) bool {
	return p.endgame && remaining > 0 && active > 1 && remaining < active
}

// spreadShare is one tunnel's take of one object, for the completion log line
// and for the tests that assert the bound.
type spreadShare struct {
	sess  *yamux.Session
	port  uint16
	bytes int64
}

// spreadPlanner is one object's assignment ledger. Safe for concurrent use:
// every chunk goroutine picks and settles through it.
type spreadPlanner struct {
	c   *Client
	dir spreadDir
	pol spreadPolicy

	mu    sync.Mutex
	bytes map[*yamux.Session]int64
	order []*yamux.Session
}

// newSpreadPlanner snapshots the policy for one object. The knobs are read
// ONCE here, not per chunk: a max_share that moved mid-object would be applied
// to shares accumulated under the old one.
func (c *Client) newSpreadPlanner(dir spreadDir) *spreadPlanner {
	return &spreadPlanner{c: c, dir: dir, pol: spreadPolicyNow(), bytes: map[*yamux.Session]int64{}}
}

// ensureRoutes applies min_routes before the first chunk goes out, by promoting
// standby tunnels. It reports the active width it achieved.
func (p *spreadPlanner) ensureRoutes() int {
	if p == nil || p.c == nil || p.pol.minRoutes < 2 {
		return 0
	}
	return p.c.ensureMinRoutes(p.pol.minRoutes, p.dir, "spread.min_routes")
}

// pick returns the tunnel the next chunk of `size` bytes belongs on AND books
// those bytes against it, or nil to leave the choice to pickSessionFor (the
// policy steers nothing, or there is nothing to choose between).
//
// The choose and the charge are ONE critical section, and that is the whole
// point of the method: chunks are admitted in a burst — the download gate is
// chunk.tunnel_concurrency × the active width — so picks resolve concurrently.
// Reading the ledger under one lock and charging under another leaves every
// pick in the burst looking at the same all-zero ledger, which is one tie that
// the whole burst breaks the same way. A caller that gets a session back owes
// uncharge(sess, size) if the chunk never goes out.
func (p *spreadPlanner) pick(size int64) *yamux.Session {
	if p == nil || p.c == nil || !p.pol.steers() {
		return nil
	}
	sessions, caps, priors := p.c.spreadCandidates(p.dir)
	if len(sessions) < 2 {
		return nil
	}
	carried := make([]int64, len(sessions))
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range sessions {
		carried[i] = p.bytes[s]
	}
	i := spreadChoose(caps, priors, carried, p.pol)
	if i < 0 {
		return nil
	}
	p.chargeLocked(sessions[i], size)
	return sessions[i]
}

// pickIdle returns the fastest tunnel carrying NO stream right now, for an
// endgame duplicate: a duplicate is only free if it rides a tunnel that would
// otherwise sit out the rest of the object. nil when every tunnel is busy.
//
// "Fastest" is the measured capacity where there is one and the prior
// otherwise, so the duplicate of a tail chunk does not go to the one tunnel in
// the set nothing is known about.
func (p *spreadPlanner) pickIdle() *yamux.Session {
	if p == nil || p.c == nil {
		return nil
	}
	sessions, caps, priors := p.c.spreadCandidates(p.dir)
	var (
		best  *yamux.Session
		bestC = -1.0
	)
	for i, s := range sessions {
		if s.NumStreams() > 0 {
			continue
		}
		rate := caps[i]
		if rate <= 0 {
			rate = priors[i]
		}
		if rate > bestC {
			best, bestC = s, rate
		}
	}
	return best
}

// charge books `n` bytes against the tunnel at the moment the chunk is admitted
// — not when it completes. The reservation is what stops eight concurrent
// picks from all landing on the same tunnel because none of them has moved a
// byte yet.
func (p *spreadPlanner) charge(s *yamux.Session, n int64) {
	if p == nil || s == nil {
		return
	}
	p.mu.Lock()
	p.chargeLocked(s, n)
	p.mu.Unlock()
}

// chargeLocked is charge with p.mu already held, so a pick can book what it
// chose without letting go of the ledger in between.
func (p *spreadPlanner) chargeLocked(s *yamux.Session, n int64) {
	if n <= 0 {
		return
	}
	if _, seen := p.bytes[s]; !seen {
		p.order = append(p.order, s)
	}
	p.bytes[s] += n
}

// uncharge releases a reservation that never became bytes on the wire: the
// stream would not open, or the chosen tunnel closed between the pick and the
// open. A reservation left behind is phantom bytes — it steers the rest of the
// object away from a tunnel that carried nothing for it, and it inflates that
// tunnel's share in the completion line.
func (p *spreadPlanner) uncharge(s *yamux.Session, n int64) {
	if p == nil || s == nil || n <= 0 {
		return
	}
	p.mu.Lock()
	if p.bytes[s] -= n; p.bytes[s] < 0 {
		p.bytes[s] = 0
	}
	p.mu.Unlock()
}

// settle corrects the reservation to what the tunnel actually carried: the
// bytes delivered on a short read, and nothing at all on an attempt that
// failed (a chunk refetched elsewhere must not leave its first tunnel charged
// for bytes it never carried).
func (p *spreadPlanner) settle(s *yamux.Session, reserved, actual int64) {
	if p == nil || s == nil || reserved == actual {
		return
	}
	p.mu.Lock()
	p.bytes[s] += actual - reserved
	if p.bytes[s] < 0 {
		p.bytes[s] = 0
	}
	p.mu.Unlock()
}

// shares snapshots the ledger, biggest first.
func (p *spreadPlanner) shares() []spreadShare {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	out := make([]spreadShare, 0, len(p.order))
	for _, s := range p.order {
		out = append(out, spreadShare{sess: s, port: p.c.tunnelPort(s), bytes: p.bytes[s]})
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].bytes > out[j].bytes })
	return out
}

// sharesLine renders the ledger as `shares=<port>:<pct>,...` for the object's
// completion log line — the client's own account of what each tunnel carried,
// which is what the bench's carrier.tsv per-transport deltas are matched
// against (bench/direction.sh).
func (p *spreadPlanner) sharesLine() string {
	sh := p.shares()
	var total int64
	for _, s := range sh {
		total += s.bytes
	}
	if total <= 0 {
		return "shares=none"
	}
	parts := make([]string, 0, len(sh))
	for _, s := range sh {
		parts = append(parts, fmt.Sprintf("%d:%.0f%%", s.port, 100*float64(s.bytes)/float64(total)))
	}
	return "shares=" + strings.Join(parts, ",")
}

// topShare is the largest single tunnel's fraction of the object, 0 when
// nothing was booked. It is the number criterion 10 bounds.
func (p *spreadPlanner) topShare() float64 {
	sh := p.shares()
	var total, top int64
	for _, s := range sh {
		total += s.bytes
		if s.bytes > top {
			top = s.bytes
		}
	}
	if total <= 0 {
		return 0
	}
	return float64(top) / float64(total)
}

// recordSpread publishes the finished object's ledger on the client, so the
// status surface and the tests can read what the last object actually did
// rather than parsing it back out of a log line.
func (c *Client) recordSpread(pl *spreadPlanner) {
	if pl != nil {
		c.spreadLast.Store(pl)
	}
}

// lastSpread returns the ledger of the most recently completed split download
// or striped upload, or nil when none has completed.
func (c *Client) lastSpread() *spreadPlanner {
	pl, _ := c.spreadLast.Load().(*spreadPlanner)
	return pl
}

// spreadCandidates snapshots the tunnels a chunk may be placed on — live,
// active (a standby tunnel carries nothing by definition), not benched by an
// exit-open timeout, and not SNUBBED — with each one's proven capacity in the
// planner's direction AND its capacity prior. A snub is a standby by another
// name: the tunnel is no candidate until it is unsnubbed, and the chunks it
// held are re-issued onto the ones that are. The order is the session order, so
// spreadChoose's tie-break is today's pick order.
//
// The two capacity slices are returned SEPARATELY, and that is the point: a
// measurement and a prior are different kinds of claim, and the planner's
// whole new rule is that it must be able to tell them apart. capsBps[i] is 0
// for a tunnel no busy window has sampled, whatever priorBps[i] says.
func (c *Client) spreadCandidates(dir spreadDir) (sessions []*yamux.Session, capsBps, priorBps []float64) {
	now := time.Now()
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	for _, s := range c.sessions {
		if s == nil || s.IsClosed() || c.standby[s] {
			continue
		}
		m := c.recvStamp[s]
		if m != nil && (m.onBench(now) || m.snubSitOut()) {
			continue
		}
		var bps, prior float64
		if m != nil {
			bps, _ = m.capacityDir(now, dir == spreadUp)
			prior = m.prior()
		}
		sessions = append(sessions, s)
		capsBps = append(capsBps, bps)
		priorBps = append(priorBps, prior)
	}
	return sessions, capsBps, priorBps
}

// tunnelPort is the local route-group port a tunnel was dialed from — the one
// name the app, the visor and the bench's carrier.tsv share for it. 0 when the
// conn is not an app conn (every unit test).
func (c *Client) tunnelPort(s *yamux.Session) uint16 {
	c.sessionsMu.Lock()
	defer c.sessionsMu.Unlock()
	if m := c.recvStamp[s]; m != nil {
		return uint16(m.port)
	}
	return 0
}

// ensureMinRoutes promotes standby tunnels until the active set holds n of
// them, and reports the width it reached. It NEVER dials: the pool is filled
// by the keepalive loop against the routes that were discovered, and a spread
// policy asking for more routes than exist must run on the ones that do.
//
// It promotes on measured CAPACITY in the object's direction
// (promoteFastestStandby), not on the failover path's RTT rank: a route added to
// widen a transfer is judged on what it has been shown to carry, since that is
// what the planner will weigh its share by. With nothing measured that falls
// back to the RTT rank on its own.
func (c *Client) ensureMinRoutes(n int, dir spreadDir, reason string) int {
	held := c.liveSessionCount()
	for i := 0; i <= held; i++ {
		if c.activeLiveCount() >= n {
			break
		}
		if c.promoteFastestStandby(dir, reason) == nil {
			break
		}
	}
	return c.activeLiveCount()
}

// medianMeasured is the median of the capacities that have actually been
// measured (the positive entries), 0 when none has. It is what a striped
// upload credits a tunnel it knows nothing about.
//
// Crediting an unmeasured tunnel the BEST capacity present is right for a
// download — a tunnel that never gets a chunk is never measured, and the cost
// of probing one is a chunk that arrives late among many. It is wrong for a
// SMALL striped upload, where every chunk is admitted before any of them
// reports back and the object finishes when its slowest chunk does: measured
// live 2026-09-16 (bench/2026-09-16/e128db1ff-smoke/mux-tunnels-2, 10 MB
// uploads over two tunnels of 31.6 ms and 135.8 ms), the row that put one
// chunk on a third tunnel created mid-set and never measured ran x0.265 of the
// single-route bar, against x1.107 for the row that used the fast tunnel
// alone. The median is the honest prior: an unproven tunnel is no better than
// the middle of the ones that have proven something.
func medianMeasured(capsBps []float64) float64 {
	seen := make([]float64, 0, len(capsBps))
	for _, c := range capsBps {
		if c > 0 {
			seen = append(seen, c)
		}
	}
	if len(seen) == 0 {
		return 0
	}
	sort.Float64s(seen)
	if n := len(seen); n%2 == 1 {
		return seen[n/2]
	}
	return (seen[len(seen)/2-1] + seen[len(seen)/2]) / 2
}

// spreadBurst assigns n chunks of ONE object to the tunnels in capsBps and
// returns the tunnel index each chunk belongs on, chunk 0 first.
//
// It exists for the case the per-chunk picker cannot answer: an object small
// enough that every one of its chunks is admitted before any of them has moved
// a byte. The picker then resolves the whole burst against the same stale
// ledger and places the chunks round-robin, which for two tunnels of very
// different speeds is the worst assignment available — the object finishes at
// the slow tunnel's pace while the fast one idles. Live, at 2.5 MB chunks over
// a 31.6 ms and a 135.8 ms tunnel: 4/0 ran 1.61 s (x1.107 of the single-route
// bar), 3/1 ran x0.393 with the lone slow chunk taking ~3.6 s while the fast
// tunnel sat idle for 2.6 s of it, and 2/1/1 over a third, never-measured
// tunnel ran x0.265.
//
// Three rules:
//
//  1. WEIGHTS. A measured tunnel weighs its capacity; an unmeasured one weighs
//     medianMeasured. With nothing measured anywhere there is no slow tunnel to
//     avoid and the chunks go round-robin, which is equal shares.
//  2. PROPORTIONS. Seats are apportioned by largest remainder, then every
//     tunnel but the fastest is capped at floor(n × w_i / Σw) — so at 6 MB/s
//     beside 0.7 MB/s a four-chunk object puts nothing on the slow tunnel, and
//     the fastest absorbs whatever the caps leave over.
//  3. THE TAIL. The chunks are emitted slowest tunnel first, so the LAST chunk
//     of the object — the one that decides when it completes, because there is
//     no later chunk behind it to hide its latency — always lands on the
//     fastest tunnel.
func spreadBurst(capsBps []float64, n int) []int {
	k := len(capsBps)
	if n <= 0 || k == 0 {
		return nil
	}
	credit := medianMeasured(capsBps)
	if credit <= 0 || k == 1 {
		// Nothing proven anywhere (or nowhere else to put a chunk): equal
		// shares, which is what the picker's fewest-streams rule already does.
		out := make([]int, n)
		for i := range out {
			out[i] = i % k
		}
		return out
	}
	weights := make([]float64, k)
	total, fastest := 0.0, 0
	for i, c := range capsBps {
		weights[i] = c
		if weights[i] <= 0 {
			weights[i] = credit
		}
		total += weights[i]
		if weights[i] > weights[fastest] {
			fastest = i
		}
	}
	counts := seatsByRemainder(weights, total, n)
	// The cap, and the fastest tunnel absorbing what it leaves over.
	left := n
	for i := range counts {
		if i == fastest {
			continue
		}
		if capped := int(float64(n) * weights[i] / total); counts[i] > capped {
			counts[i] = capped
		}
		left -= counts[i]
	}
	counts[fastest] = left
	// Slowest first, so the object's last chunk is the fastest tunnel's.
	order := make([]int, k)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return weights[order[a]] < weights[order[b]] })
	out := make([]int, 0, n)
	for _, lane := range order {
		for j := 0; j < counts[lane]; j++ {
			out = append(out, lane)
		}
	}
	return out
}

// seatsByRemainder apportions n seats over the weights: each takes its whole
// quota, and the seats the quotas leave unassigned go to the largest fractional
// remainders (ties to the heavier weight, then the lower index). It is Hamilton
// apportionment, and it is only the first half of spreadBurst's rule 2 — the
// per-tunnel cap is applied to what it returns.
func seatsByRemainder(weights []float64, total float64, n int) []int {
	counts := make([]int, len(weights))
	if total <= 0 {
		return counts
	}
	type rem struct {
		i    int
		frac float64
		w    float64
	}
	rems := make([]rem, 0, len(weights))
	seated := 0
	for i, w := range weights {
		q := float64(n) * w / total
		counts[i] = int(q)
		seated += counts[i]
		rems = append(rems, rem{i: i, frac: q - float64(counts[i]), w: w})
	}
	sort.SliceStable(rems, func(a, b int) bool {
		if rems[a].frac != rems[b].frac {
			return rems[a].frac > rems[b].frac
		}
		return rems[a].w > rems[b].w
	})
	for j := 0; seated < n && j < len(rems); j++ {
		counts[rems[j].i]++
		seated++
	}
	return counts
}
