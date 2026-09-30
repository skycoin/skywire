// Package api pkg/deployment/ar/api/reach.go c4-net-discovery
//
// The reach book: per peer, what is known about whether it can actually be
// transported, published as the reach feed (see arfeed/reach.go for the
// record and why each type is judged the way it is).
//
// Three inputs, all local to the AR:
//
//   - probes — every stcpr / squicr / swtr binding is dialed back from here
//     when it appears or its address changes, and again every probeEvery.
//   - the sudph UDP control connection — up or down, as the UDP loop sees it.
//   - the visor's own declaration, the "reach" leaf of its AR-bind feed.
//
// A peer's record is dropped once it has neither a binding nor a fresh
// declaration. Everything is in memory: after a restart the probes re-run
// as bindings are refreshed and every visor's feed is re-filled, so the book
// rebuilds itself within one keepalive.
package api

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/deployment/ar/arfeed"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// probeEvery is how often a binding that answered is probed again.
	probeEvery = 30 * time.Minute
	// probeRetry is how soon a binding that did not answer is probed again.
	// A binding is published as Closed only after probeFailsToClose misses in
	// a row, so one lost packet does not demote it.
	probeRetry        = 5 * time.Minute
	probeFailsToClose = 2
	// probeTimeout bounds one probe.
	probeTimeout = 5 * time.Second
	// probeTargetGap is the least time between two probes of one address,
	// whichever peers bind it — the AR must not be usable to hammer a host.
	probeTargetGap = probeRetry
	// probeWorkers and probeQueue bound the probing. Probes are rare (one per
	// binding per probeEvery); a full queue drops, and the next refresh of the
	// binding asks again.
	probeWorkers = 8
	probeQueue   = 1024
	// declTTL is how long a declaration counts without being refreshed. The
	// visor's feed heartbeat refreshes it every 45 s.
	declTTL = 10 * time.Minute
	// bindTTL is how long a binding counts without a refresh. The AR's store
	// holds the authoritative TTL; this only ages out the book's copy.
	bindTTL = 30 * time.Minute
	// reachFlushWindow coalesces bucket re-encodes; reachBatchWindow is the
	// treestore publish window.
	reachFlushWindow = 3 * time.Second
	reachBatchWindow = 30 * time.Second
	reachSweepEvery  = time.Minute
)

// probedTypes are the types whose bound address the AR dials back.
var probedTypes = map[types.Type]string{
	types.STCPR: arfeed.TypeSTCPR,
	types.QUIC:  arfeed.TypeQUIC,
	types.WT:    arfeed.TypeWT,
}

// bookTypeName maps the bindable types to their reach names.
var bookTypeName = map[types.Type]string{
	types.STCPR: arfeed.TypeSTCPR,
	types.SUDPH: arfeed.TypeSUDPH,
	types.QUIC:  arfeed.TypeQUIC,
	types.WT:    arfeed.TypeWT,
}

type probeState struct {
	target  string
	next    time.Time // when it may be probed again
	queued  bool
	fails   int
	open    bool
	settled bool // a verdict has been published: open, or closed after misses
}

type peerReach struct {
	decl     *arfeed.ReachDecl
	declSeen time.Time
	bound    map[string]time.Time // reach type name -> last bind refresh
	probes   map[string]*probeState
	live     bool
}

type probeJob struct {
	pk     cipher.PubKey
	t      string
	target string
}

// reachBook is the AR's reachability state and the reach feed publisher.
type reachBook struct {
	log *logging.Logger
	pub *treestore.Publisher // nil until attached; the book still tracks state

	// probe is the function that dials a target; replaced in tests.
	probe func(ctx context.Context, t, target string) bool

	mu         sync.Mutex
	peers      map[cipher.PubKey]*peerReach
	lastTarget map[string]time.Time // target -> last probe start
	dirty      map[int]struct{}

	jobs chan probeJob
	done chan struct{}
	wg   sync.WaitGroup
	once sync.Once
}

func newReachBook(log *logging.Logger) *reachBook {
	b := &reachBook{
		log:        log,
		probe:      probeTarget,
		peers:      make(map[cipher.PubKey]*peerReach),
		lastTarget: make(map[string]time.Time),
		dirty:      make(map[int]struct{}),
		jobs:       make(chan probeJob, probeQueue),
		done:       make(chan struct{}),
	}
	for i := 0; i < probeWorkers; i++ {
		b.wg.Add(1)
		go b.prober()
	}
	b.wg.Add(1)
	go b.run()
	return b
}

func (b *reachBook) close() {
	if b == nil {
		return
	}
	b.once.Do(func() { close(b.done) })
	b.wg.Wait()
}

// peer returns the peer's record, creating it. Caller holds mu.
func (b *reachBook) peer(pk cipher.PubKey) *peerReach {
	p := b.peers[pk]
	if p == nil {
		p = &peerReach{bound: make(map[string]time.Time), probes: make(map[string]*probeState)}
		b.peers[pk] = p
	}
	return p
}

func (b *reachBook) markDirty(pk cipher.PubKey) {
	b.dirty[arfeed.ReachBucket(pk)] = struct{}{}
}

// probeAddr is the address a dialer would use for a binding: the stored
// remote IP (v4 first) and the declared listening port.
func probeAddr(data addrresolver.VisorData) string {
	if data.Port == "" {
		return ""
	}
	for _, a := range []string{data.RemoteAddr, data.RemoteAddrV6} {
		if a == "" {
			continue
		}
		host := a
		if h, _, err := net.SplitHostPort(a); err == nil {
			host = h
		}
		if net.ParseIP(host) == nil {
			continue
		}
		return net.JoinHostPort(host, data.Port)
	}
	return ""
}

// noteBind records a binding write. Bindings are refreshed on every keepalive,
// so this is cheap unless the address changed or a probe is due.
func (b *reachBook) noteBind(t types.Type, pk cipher.PubKey, data addrresolver.VisorData) {
	if b == nil {
		return
	}
	name, ok := bookTypeName[types.NormalizeType(t)]
	if !ok {
		return
	}
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.peer(pk)
	if _, had := p.bound[name]; !had {
		b.markDirty(pk)
	}
	p.bound[name] = now

	if _, probed := probedTypes[types.NormalizeType(t)]; !probed {
		return
	}
	target := probeAddr(data)
	ps := p.probes[name]
	if ps == nil || ps.target != target {
		// New or moved: whatever was known about the old address is void.
		if ps != nil && ps.settled {
			b.markDirty(pk)
		}
		ps = &probeState{target: target}
		p.probes[name] = ps
	}
	if target == "" || ps.queued || now.Before(ps.next) {
		return
	}
	if last, ok := b.lastTarget[target]; ok && now.Sub(last) < probeTargetGap {
		return
	}
	select {
	case b.jobs <- probeJob{pk: pk, t: name, target: target}:
		ps.queued = true
		b.lastTarget[target] = now
	default:
	}
}

// noteDelBind records a binding removal.
func (b *reachBook) noteDelBind(t types.Type, pk cipher.PubKey) {
	if b == nil {
		return
	}
	name, ok := bookTypeName[types.NormalizeType(t)]
	if !ok {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.peers[pk]
	if p == nil {
		return
	}
	if _, had := p.bound[name]; had {
		delete(p.bound, name)
		delete(p.probes, name)
		b.markDirty(pk)
	}
}

// setLive records whether the peer holds a sudph UDP control connection.
func (b *reachBook) setLive(pk cipher.PubKey, live bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !live && b.peers[pk] == nil {
		return
	}
	p := b.peer(pk)
	if p.live != live {
		p.live = live
		b.markDirty(pk)
	}
}

// noteDecl records a visor's own declaration. Called on every filled Root of
// its feed, so an unchanged declaration only refreshes the timestamp.
func (b *reachBook) noteDecl(pk cipher.PubKey, d arfeed.ReachDecl) {
	if b == nil {
		return
	}
	d = normalizeDecl(d)
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.peer(pk)
	if p.decl == nil || !sameDecl(*p.decl, d) {
		p.decl = &d
		b.markDirty(pk)
	}
	p.declSeen = time.Now()
}

func normalizeDecl(d arfeed.ReachDecl) arfeed.ReachDecl {
	acc := append([]string(nil), d.Accepts...)
	sort.Strings(acc)
	d.Accepts = acc
	if len(d.Inbound) > 0 {
		g := int64(arfeed.InboundGranularity / time.Second)
		in := make(map[string]int64, len(d.Inbound))
		for t, at := range d.Inbound {
			if at > 0 {
				in[t] = at - at%g
			}
		}
		d.Inbound = in
	}
	return d
}

func sameDecl(a, b arfeed.ReachDecl) bool {
	if a.NAT != b.NAT || len(a.Accepts) != len(b.Accepts) || len(a.Inbound) != len(b.Inbound) {
		return false
	}
	for i := range a.Accepts {
		if a.Accepts[i] != b.Accepts[i] {
			return false
		}
	}
	for t, at := range a.Inbound {
		if b.Inbound[t] != at {
			return false
		}
	}
	return true
}

// prober runs probe jobs.
func (b *reachBook) prober() {
	defer b.wg.Done()
	for {
		select {
		case <-b.done:
			return
		case j := <-b.jobs:
			ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
			ok := b.probe(ctx, j.t, j.target)
			cancel()
			b.probeDone(j, ok)
		}
	}
}

func (b *reachBook) probeDone(j probeJob, ok bool) {
	now := time.Now()
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.peers[j.pk]
	if p == nil {
		return
	}
	ps := p.probes[j.t]
	if ps == nil || ps.target != j.target {
		return // the binding moved while the probe ran
	}
	ps.queued = false
	wasOpen, wasSettled := ps.open, ps.settled
	if ok {
		ps.fails = 0
		ps.open, ps.settled = true, true
		ps.next = now.Add(probeEvery)
	} else {
		ps.fails++
		ps.next = now.Add(probeRetry)
		if ps.fails >= probeFailsToClose {
			ps.open, ps.settled = false, true
		}
	}
	if ps.open != wasOpen || ps.settled != wasSettled {
		b.markDirty(j.pk)
		b.log.WithField("pk", j.pk).WithField("type", j.t).WithField("target", j.target).
			WithField("open", ps.open).Debug("reach: probe verdict changed")
	}
}

// record builds a peer's published record. Caller holds mu.
func (p *peerReach) record(now time.Time) *arfeed.Reach {
	r := &arfeed.Reach{Live: p.live}
	if p.decl != nil && now.Sub(p.declSeen) <= declTTL {
		r.ReachDecl = *p.decl
	}
	for t := range p.bound {
		r.Bound = append(r.Bound, t)
	}
	for t, ps := range p.probes {
		if !ps.settled {
			continue
		}
		if ps.open {
			r.Open = append(r.Open, t)
		} else {
			r.Closed = append(r.Closed, t)
		}
	}
	sort.Strings(r.Bound)
	sort.Strings(r.Open)
	sort.Strings(r.Closed)
	return r
}

// sweep ages out stale bindings and declarations and drops empty peers.
// Caller holds mu.
func (b *reachBook) sweep(now time.Time) {
	for pk, p := range b.peers {
		for t, at := range p.bound {
			if now.Sub(at) > bindTTL {
				delete(p.bound, t)
				delete(p.probes, t)
				b.markDirty(pk)
			}
		}
		if p.decl != nil && now.Sub(p.declSeen) > declTTL {
			p.decl = nil
			b.markDirty(pk)
		}
		if len(p.bound) == 0 && p.decl == nil && !p.live {
			delete(b.peers, pk)
			b.markDirty(pk)
		}
	}
	for target, at := range b.lastTarget {
		if now.Sub(at) > probeTargetGap {
			delete(b.lastTarget, target)
		}
	}
}

// buckets encodes the named buckets from the current state.
func (b *reachBook) buckets(which map[int]struct{}, now time.Time) []treestore.PutOp {
	b.mu.Lock()
	by := make(map[int]map[cipher.PubKey]*arfeed.Reach, len(which))
	for i := range which {
		by[i] = make(map[cipher.PubKey]*arfeed.Reach)
	}
	for pk, p := range b.peers {
		m, want := by[arfeed.ReachBucket(pk)]
		if !want {
			continue
		}
		m[pk] = p.record(now)
	}
	b.mu.Unlock()

	ops := make([]treestore.PutOp, 0, len(by))
	for i, m := range by {
		blob, err := arfeed.EncodeReachBucket(m)
		if err != nil {
			b.log.WithError(err).Debug("reach: bucket encode failed")
			continue
		}
		ops = append(ops, treestore.PutOp{Path: arfeed.ReachPath(i), Value: blob})
	}
	return ops
}

// run flushes dirty buckets to the feed and sweeps.
func (b *reachBook) run() {
	defer b.wg.Done()
	flush := time.NewTicker(reachFlushWindow)
	defer flush.Stop()
	sweep := time.NewTicker(reachSweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-sweep.C:
			b.mu.Lock()
			b.sweep(time.Now())
			b.mu.Unlock()
		case <-flush.C:
			b.flush()
		}
	}
}

func (b *reachBook) flush() {
	b.mu.Lock()
	pub := b.pub
	if pub == nil || len(b.dirty) == 0 {
		b.mu.Unlock()
		return
	}
	dirty := b.dirty
	b.dirty = make(map[int]struct{})
	b.mu.Unlock()
	if err := pub.PutBatch(b.buckets(dirty, time.Now())); err != nil {
		b.log.WithError(err).Debug("reach: PutBatch failed")
	}
}

// attach installs the feed publisher and materializes every bucket, so the
// tree is dense from the first Root.
func (b *reachBook) attach(pub *treestore.Publisher) {
	all := make(map[int]struct{}, arfeed.ReachBuckets)
	for i := 0; i < arfeed.ReachBuckets; i++ {
		all[i] = struct{}{}
	}
	b.mu.Lock()
	b.pub = pub
	b.mu.Unlock()
	if err := pub.PutBatch(b.buckets(all, time.Now())); err != nil {
		b.log.WithError(err).Debug("reach: initial PutBatch failed")
	}
}

// ReachCXOPublisher is the reach feed's publisher, as the host tracks it.
type ReachCXOPublisher struct {
	book *reachBook
	pub  *treestore.Publisher
}

// Publisher returns the underlying feed publisher.
func (p *ReachCXOPublisher) Publisher() *treestore.Publisher { return p.pub }

// Close detaches the feed from the book and closes it.
func (p *ReachCXOPublisher) Close() error {
	p.book.mu.Lock()
	if p.book.pub == p.pub {
		p.book.pub = nil
	}
	p.book.mu.Unlock()
	return p.pub.Close()
}

// StartReachCXOPublisher publishes the reach book as the reach feed on
// DmsgARReachCXOPort.
func (a *API) StartReachCXOPublisher(dmsgC *dmsg.Client, sk cipher.SecKey) (*ReachCXOPublisher, error) {
	pub, err := treestore.NewWithDMSG(dmsgC, sk, treestore.PubConfig{
		Logger:      a.reach.log,
		InMemoryDB:  true, // rebuilt from bindings and visor feeds after a restart
		DmsgPort:    skyenv.DmsgARReachCXOPort,
		BatchWindow: reachBatchWindow,
	})
	if err != nil {
		return nil, err
	}
	pub.SetAllowlist(nil)
	a.reach.attach(pub)
	return &ReachCXOPublisher{book: a.reach, pub: pub}, nil
}

// IngestReachFromCXO applies a visor's reach declaration from its AR-bind feed.
func (a *API) IngestReachFromCXO(reporter cipher.PubKey, d arfeed.ReachDecl) {
	if reporter == (cipher.PubKey{}) {
		return
	}
	a.reach.noteDecl(reporter, d)
}

// probeTarget dials target the way a peer would and reports whether anything
// answered.
func probeTarget(ctx context.Context, t, target string) bool {
	switch t {
	case arfeed.TypeSTCPR:
		var d net.Dialer
		c, err := d.DialContext(ctx, "tcp", target)
		if err != nil {
			return false
		}
		_ = c.Close() //nolint:errcheck
		return true
	case arfeed.TypeQUIC, arfeed.TypeWT:
		return probeQUIC(ctx, target)
	}
	return false
}

// quicProbeSize is the least datagram size a QUIC server answers for an
// unknown version (RFC 9000 §14.1: an Initial is padded to 1200 bytes).
const quicProbeSize = 1200

// quicProbeVersion is a reserved version (RFC 9000 §15, 0x?a?a?a?a), which no
// server supports, so a listener answers it with a version negotiation.
const quicProbeVersion = 0x1a2a3a4a

// probeQUIC sends one long-header packet with an unsupported version and waits
// for the version negotiation packet a QUIC listener sends back (quic-go does
// unless DisableVersionNegotiationPackets, which skywire never sets). Two
// tries, as UDP may lose either datagram.
func probeQUIC(ctx context.Context, target string) bool {
	raddr, err := net.ResolveUDPAddr("udp", target)
	if err != nil {
		return false
	}
	c, err := net.ListenUDP("udp", nil)
	if err != nil {
		return false
	}
	defer c.Close() //nolint:errcheck

	pkt := make([]byte, quicProbeSize)
	if _, err := rand.Read(pkt); err != nil {
		return false
	}
	pkt[0] = 0xc0 | (pkt[0] & 0x3f) // long header, fixed bit
	binary.BigEndian.PutUint32(pkt[1:5], quicProbeVersion)
	pkt[5] = 8  // destination connection ID length (followed by 8 random bytes)
	pkt[14] = 8 // source connection ID length (followed by 8 random bytes)

	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(probeTimeout)
	}
	per := time.Until(deadline) / 2
	buf := make([]byte, 1500)
	for try := 0; try < 2; try++ {
		if _, err := c.WriteToUDP(pkt, raddr); err != nil {
			return false
		}
		_ = c.SetReadDeadline(time.Now().Add(per)) //nolint:errcheck
		for {
			n, from, err := c.ReadFromUDP(buf)
			if err != nil {
				break // this try timed out
			}
			if !from.IP.Equal(raddr.IP) || from.Port != raddr.Port {
				continue
			}
			if isVersionNegotiation(buf[:n]) {
				return true
			}
		}
	}
	return false
}

// isVersionNegotiation reports whether b is a QUIC version negotiation packet:
// a long header whose version field is zero.
func isVersionNegotiation(b []byte) bool {
	return len(b) >= 7 && b[0]&0x80 != 0 && binary.BigEndian.Uint32(b[1:5]) == 0
}
