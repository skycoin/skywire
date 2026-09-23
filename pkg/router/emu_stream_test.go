// Package router_test pkg/router/emu_stream_test.go c2-net-routing
//
// The mux campaign's bench cells, in process and deterministic.
//
// The live bench (bench/<date>/<commit>/run-*.sh) measures the real proxy over
// the real mesh: minutes per row, a frozen rig, and references that swing 2x in
// an hour. Most scheduler and planner questions do not need the mesh — they need
// the REAL client, the REAL exit and the REAL route groups over links whose
// delay, rate and loss are known constants. That is this file.
//
//	one tunnel = one emuRig (pkg/router/harness_emu_test.go)
//	             A side  -> a tunnel of a real skysocks.Client
//	             B side  -> served by a real skysocks.Server
//	             the sink -> the real `proxy loadtest serve` handler (pkg/loadtest)
//
// Every cell prints one row in the live bench's shape — name, dir, size,
// goodput B/s, ratio vs the reference, wire/goodput, hash_ok — plus the
// per-tunnel/per-leg byte share the emulated links actually carried. Hashes are
// hard asserts everywhere; ratios are REPORTED and only asserted where a live
// criterion demands it (the spread cap, the post-cut time to first byte), so a
// scheduler change shows up as a moved number rather than a flaky test.
//
//	go test ./pkg/router -run StreamBench -v
//
// Sizes and link shapes are env-overridable (see streamEnv below); the defaults
// are picked so the whole suite runs in well under two minutes. It is named
// StreamBench, not Emu, so `go test -run Emu` stays the fast scenario suite.
package router_test

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/loadtest"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/skysocks"
	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// ---------------------------------------------------------------------------
// knobs

const mib = 1 << 20

// streamEnv reads a float knob from the environment.
func streamEnv(name string, def float64) float64 {
	v, ok := os.LookupEnv(name)
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return def
	}
	return f
}

// benchCfg is the whole suite's shape in one place.
type benchCfg struct {
	down, up     int64         // object sizes
	chunk        int64         // range-split and upload chunk size
	timeout      time.Duration // per-transfer deadline
	directMs     float64       // one-way delay of the direct (reference) leg
	directBps    int64
	hopMsMin     float64 // multihop legs interpolate between these
	hopMsMax     float64
	hopBpsMin    int64
	hopBpsMax    int64
	lossPct      float64
	concurrency  int
	cutAtPercent float64
}

func loadBenchCfg() benchCfg {
	return benchCfg{
		down:         int64(streamEnv("EMUSTREAM_DOWN_MIB", 16) * mib),
		up:           int64(streamEnv("EMUSTREAM_UP_MIB", 4) * mib),
		chunk:        int64(streamEnv("EMUSTREAM_CHUNK_MIB", 1) * mib),
		timeout:      time.Duration(streamEnv("EMUSTREAM_TIMEOUT_S", 60) * float64(time.Second)),
		directMs:     streamEnv("EMUSTREAM_DIRECT_MS", 40),
		directBps:    int64(streamEnv("EMUSTREAM_DIRECT_MBPS", 8) * mib),
		hopMsMin:     streamEnv("EMUSTREAM_HOP_MS_MIN", 120),
		hopMsMax:     streamEnv("EMUSTREAM_HOP_MS_MAX", 150),
		hopBpsMin:    int64(streamEnv("EMUSTREAM_HOP_MBPS_MIN", 3) * mib),
		hopBpsMax:    int64(streamEnv("EMUSTREAM_HOP_MBPS_MAX", 6) * mib),
		lossPct:      streamEnv("EMUSTREAM_LOSS_PCT", 0.1),
		concurrency:  int(streamEnv("EMUSTREAM_CONCURRENCY", 8)),
		cutAtPercent: streamEnv("EMUSTREAM_CUT_AT_PCT", 25),
	}
}

// ---------------------------------------------------------------------------
// link shapes

// directLeg is the reference route: one hop, 40 ms each way, 8 MB/s.
func (c benchCfg) directLeg(seed int64) router.EmuLegSpec {
	lc := router.EmuLinkConfig{
		Delay:      time.Duration(c.directMs * float64(time.Millisecond)),
		LossPct:    c.lossPct,
		RateBps:    c.directBps,
		QueueBytes: 2 * mib,
		Seed:       seed,
	}
	down := lc
	down.Seed = seed + 1
	return router.EmuLegSpec{Name: "direct", Up: lc, Down: down, LatencyMs: 2 * c.directMs, Direct: true}
}

// hopLeg is a multihop route: k selects a point on the 120-150 ms /
// 6-3 MB/s spread, so a group's legs are heterogeneous the way real ones are.
func (c benchCfg) hopLeg(name string, k int, seed int64) router.EmuLegSpec {
	const shapes = 3
	f := float64(k%shapes) / float64(shapes-1)
	ms := c.hopMsMin + (c.hopMsMax-c.hopMsMin)*f
	bps := c.hopBpsMax - int64(float64(c.hopBpsMax-c.hopBpsMin)*f)
	lc := router.EmuLinkConfig{
		Delay:      time.Duration(ms * float64(time.Millisecond)),
		LossPct:    c.lossPct,
		RateBps:    bps,
		QueueBytes: 2 * mib,
		Seed:       seed,
	}
	down := lc
	down.Seed = seed + 1
	return router.EmuLegSpec{
		Name:      fmt.Sprintf("%s-%.0fms-%.1fMBs", name, ms, float64(bps)/mib),
		Up:        lc,
		Down:      down,
		LatencyMs: 2 * ms,
	}
}

// ---------------------------------------------------------------------------
// one tunnel: an emulated route group with a real exit on the far end

// onceListener hands the exit's end of a route group to a skysocks.Server as
// the one connection it will ever accept.
type onceListener struct {
	conn    net.Conn
	taken   atomic.Bool
	done    chan struct{}
	closing sync.Once
}

func newOnceListener(c net.Conn) *onceListener {
	return &onceListener{conn: c, done: make(chan struct{})}
}

func (l *onceListener) Accept() (net.Conn, error) {
	if l.taken.CompareAndSwap(false, true) {
		return l.conn, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *onceListener) Close() error {
	l.closing.Do(func() { close(l.done) })
	return nil
}

func (l *onceListener) Addr() net.Addr { return l.conn.LocalAddr() }

// tunnel is one emuRig with a real skysocks exit answering on its acceptor end.
type tunnel struct {
	name string
	rig  *router.EmuRig
}

// newTunnel builds the rig, starts the exit, and returns the initiator end for
// the client to wrap.
func newTunnel(t *testing.T, name string, legs []router.EmuLegSpec) *tunnel {
	t.Helper()
	rig := router.NewEmuRig(t, router.EmuOpts{Legs: legs})
	srv, err := skysocks.NewServer(nil, nil)
	if err != nil {
		t.Fatalf("%s: new exit: %v", name, err)
	}
	ln := newOnceListener(rig.ExitConn())
	go func() { _ = srv.Serve(ln) }()    //nolint:errcheck // the rig's Close ends it
	t.Cleanup(func() { _ = ln.Close() }) //nolint:errcheck
	return &tunnel{name: name, rig: rig}
}

// shares renders the per-tunnel / per-leg wire bytes of one direction and
// returns each tunnel's share of the total.
func shares(tunnels []*tunnel, down bool) (string, []float64) {
	totals := make([]uint64, len(tunnels))
	var sum uint64
	for i, tn := range tunnels {
		totals[i] = tn.rig.WireBytesDir(down)
		sum += totals[i]
	}
	out := make([]float64, len(tunnels))
	var b strings.Builder
	for i, tn := range tunnels {
		if sum > 0 {
			out[i] = float64(totals[i]) / float64(sum)
		}
		if i > 0 {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "%s=%.1f%%[", tn.name, 100*out[i])
		for j := 0; j < tn.rig.Legs(); j++ {
			n := tn.rig.UpWireBytes(j)
			if down {
				n = tn.rig.DownWireBytes(j)
			}
			if j > 0 {
				b.WriteString(" ")
			}
			fmt.Fprintf(&b, "%s:%d", tn.rig.LegName(j), n)
		}
		b.WriteString("]")
	}
	return b.String(), out
}

// ---------------------------------------------------------------------------
// the proxy under test

// proxy is a running skysocks client with its tunnels.
type proxy struct {
	addr    string
	client  *skysocks.Client
	active  []*tunnel
	standby []*tunnel
}

// newProxy wires `active` tunnels into a client, holds `standby` in the pool,
// and returns the SOCKS address it listens on.
func newProxy(t *testing.T, cfg benchCfg, active, standby []*tunnel, sinkPort int) *proxy {
	t.Helper()
	if len(active) == 0 {
		t.Fatal("a proxy needs at least one active tunnel")
	}
	client, err := skysocks.NewClient(active[0].rig.ClientConn(), nil)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	for _, tn := range active[1:] {
		if err := client.AddTunnel(tn.rig.ClientConn()); err != nil {
			t.Fatalf("add tunnel %s: %v", tn.name, err)
		}
	}
	for _, tn := range standby {
		if err := client.AddStandbyTunnel(tn.rig.ClientConn()); err != nil {
			t.Fatalf("add standby tunnel %s: %v", tn.name, err)
		}
	}
	client.SetTunnelTarget(len(active))
	if len(standby) > 0 {
		client.SetStandbyPool(len(standby))
	}
	client.SetRangeSplit(true, cfg.concurrency, cfg.chunk)
	client.SetRangeSplitPort(sinkPort)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()                               //nolint:errcheck
	go func() { _ = client.ListenAndServe(addr) }() //nolint:errcheck
	for i := 0; i < 400; i++ {
		if cc, err := net.Dial("tcp", addr); err == nil {
			_ = cc.Close() //nolint:errcheck
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return &proxy{addr: addr, client: client, active: active, standby: standby}
}

// ---------------------------------------------------------------------------
// the browser

// socksConnect opens a SOCKS5 CONNECT through the proxy to host:port.
func socksConnect(proxyAddr, target string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, err
	}
	c, err := net.Dial("tcp", proxyAddr)
	if err != nil {
		return nil, err
	}
	fail := func(e error) (net.Conn, error) {
		_ = c.Close() //nolint:errcheck
		return nil, e
	}
	if _, err := c.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(c, make([]byte, 2)); err != nil {
		return fail(err)
	}
	req := []byte{0x05, 0x01, 0x00, 0x03, byte(len(host))} //nolint:gosec // 127.0.0.1 is short
	req = append(req, host...)
	req = append(req, byte(port>>8), byte(port&0xff)) //nolint:gosec
	if _, err := c.Write(req); err != nil {
		return fail(err)
	}
	if _, err := io.ReadFull(c, make([]byte, 10)); err != nil {
		return fail(err)
	}
	return c, nil
}

// readClock measures the LONGEST silence the reader saw after a mark — the
// post-cut time to first byte the degrade row reports. The first byte after a
// cut is usually already in flight, so timing only that one measures nothing;
// what the cut costs is the gap between the last byte the dead tunnel had
// buffered and the first byte the rescue delivers, and that is the widest gap
// after the mark.
type readClock struct {
	mu      sync.Mutex
	marked  bool
	last    time.Time
	widest  time.Duration
	anyByte bool
	n       atomic.Int64
}

func (p *readClock) note(n int) {
	if n <= 0 {
		return
	}
	p.n.Add(int64(n))
	now := time.Now()
	p.mu.Lock()
	if p.marked {
		p.anyByte = true
		if d := now.Sub(p.last); d > p.widest {
			p.widest = d
		}
	}
	p.last = now
	p.mu.Unlock()
}

func (p *readClock) mark() {
	p.mu.Lock()
	p.marked, p.last, p.widest, p.anyByte = true, time.Now(), 0, false
	p.mu.Unlock()
}

// gap is the widest byte-free interval after the mark; -1 if nothing followed.
func (p *readClock) gap() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.marked || !p.anyByte {
		return -1
	}
	return p.widest
}

func (p *readClock) got() int64 { return p.n.Load() }

// xfer is one transfer's result.
type xfer struct {
	bytes   int64
	got     int64
	elapsed time.Duration
	hashOK  bool
	err     error
}

// download pulls `n` bytes of the sink's certified object through the proxy and
// checks the body against the X-Sha256 the sink named.
func download(t *testing.T, p *proxy, sinkAddr string, n int64, cfg benchCfg, clk *readClock) xfer {
	t.Helper()
	if clk == nil {
		clk = new(readClock)
	}
	start := time.Now()
	res := xfer{bytes: n}
	c, err := socksConnect(p.addr, sinkAddr)
	if err != nil {
		res.err = err
		return res
	}
	defer c.Close()                                //nolint:errcheck
	_ = c.SetDeadline(time.Now().Add(cfg.timeout)) //nolint:errcheck

	if _, err := fmt.Fprintf(c, "GET /?bytes=%d HTTP/1.1\r\nHost: %s\r\nUser-Agent: emubench\r\n\r\n", n, sinkAddr); err != nil {
		res.err = err
		return res
	}
	resp, err := http.ReadResponse(bufio.NewReaderSize(c, 64<<10), nil)
	if err != nil {
		res.err = err
		return res
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		res.err = fmt.Errorf("status %d", resp.StatusCode)
		return res
	}
	want := resp.Header.Get("X-Sha256")

	h := sha256.New()
	buf := make([]byte, 64<<10)
	for {
		rn, rerr := resp.Body.Read(buf)
		if rn > 0 {
			h.Write(buf[:rn]) //nolint:errcheck // hash.Hash never errors
			clk.note(rn)
		}
		if rerr != nil {
			if rerr != io.EOF {
				res.err = rerr
			}
			break
		}
	}
	res.elapsed = time.Since(start)
	res.got = clk.got()
	res.hashOK = res.got == n && want != "" && hex.EncodeToString(h.Sum(nil)) == want
	return res
}

// upload POSTs a deterministic blob to the sink's /upload and checks the hash
// the sink computed over what it received.
func upload(t *testing.T, p *proxy, sinkAddr string, n int64, cfg benchCfg) xfer {
	t.Helper()
	blob := make([]byte, n)
	for i := range blob {
		blob[i] = byte(i*17 + 5)
	}
	sum := sha256.Sum256(blob)
	want := hex.EncodeToString(sum[:])

	start := time.Now()
	res := xfer{bytes: n}
	c, err := socksConnect(p.addr, sinkAddr)
	if err != nil {
		res.err = err
		return res
	}
	defer c.Close()                                //nolint:errcheck
	_ = c.SetDeadline(time.Now().Add(cfg.timeout)) //nolint:errcheck

	if _, err := fmt.Fprintf(c, "POST /upload HTTP/1.1\r\nHost: %s\r\nContent-Type: application/octet-stream\r\nContent-Length: %d\r\nExpect: 100-continue\r\n\r\n",
		sinkAddr, len(blob)); err != nil {
		res.err = err
		return res
	}
	br := bufio.NewReaderSize(c, 64<<10)
	line, err := br.ReadString('\n')
	if err != nil {
		res.err = fmt.Errorf("interim: %w", err)
		return res
	}
	if strings.Contains(line, " 100 ") {
		for {
			l, err := br.ReadString('\n')
			if err != nil {
				res.err = fmt.Errorf("interim headers: %w", err)
				return res
			}
			if strings.TrimRight(l, "\r\n") == "" {
				break
			}
		}
		if _, err := c.Write(blob); err != nil {
			res.err = err
			return res
		}
	} else {
		res.err = fmt.Errorf("first response line = %q, want 100 Continue", strings.TrimSpace(line))
		return res
	}
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		res.err = err
		return res
	}
	defer resp.Body.Close() //nolint:errcheck
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		res.err = err
		return res
	}
	res.elapsed = time.Since(start)
	res.got = got.Bytes
	res.hashOK = resp.StatusCode == http.StatusOK && got.Bytes == n && got.Sha256 == want
	return res
}

// ---------------------------------------------------------------------------
// the report

// row is one line of the summary table, in the live bench's column order.
type row struct {
	name   string
	dir    string
	size   int64
	bps    float64
	ratio  float64
	wire   float64
	hashOK bool
	share  string
	note   string
}

func (r row) tsv() string {
	return fmt.Sprintf("%s\t%s\t%d\t%.0f\t%.2f\t%.3f\t%v", r.name, r.dir, r.size, r.bps, r.ratio, r.wire, r.hashOK)
}

// record folds a transfer and the tunnels' wire counters into a row, logs it,
// and hard-asserts the hash.
func record(t *testing.T, rows *[]row, name, dir string, x xfer, tunnels []*tunnel, note string) row {
	t.Helper()
	r := row{name: name, dir: dir, size: x.bytes, hashOK: x.hashOK, note: note}
	if x.elapsed > 0 {
		r.bps = float64(x.got) / x.elapsed.Seconds()
	}
	line, _ := shares(tunnels, dir == "down")
	r.share = line
	var wire uint64
	for _, tn := range tunnels {
		wire += tn.rig.WireBytesDir(dir == "down")
	}
	if x.got > 0 {
		r.wire = float64(wire) / float64(x.got)
	}
	*rows = append(*rows, r)
	t.Logf("row  %s", r.tsv())
	t.Logf("share %s", r.share)
	if note != "" {
		t.Logf("note  %s", note)
	}
	if x.err != nil {
		t.Errorf("%s/%s: %v", name, dir, x.err)
	}
	if !x.hashOK {
		t.Errorf("%s/%s: hash check FAILED (%d/%d bytes)", name, dir, x.got, x.bytes)
	}
	return r
}

// ---------------------------------------------------------------------------
// the suite

// baseKnobs is the profile every cell runs under: chunks small enough that the
// bench's objects are many chunks, so a scheduler has something to schedule.
func baseKnobs(cfg benchCfg) map[string]int64 {
	return map[string]int64{
		skysettings.ChunkMaxBytes:        cfg.chunk,
		skysettings.ChunkMinBytes:        cfg.chunk,
		skysettings.ChunkProbeBytes:      cfg.chunk,
		skysettings.UploadChunkBytes:     cfg.chunk,
		skysettings.UploadStripeMinBytes: cfg.chunk,
	}
}

func withKnobs(base map[string]int64, extra map[string]int64) map[string]int64 {
	out := make(map[string]int64, len(base)+len(extra))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	return out
}

// TestStreamBench runs the campaign's cells over emulated tunnels. Each subtest
// is one cell; the parent prints the summary table.
func TestStreamBench(t *testing.T) {
	cfg := loadBenchCfg()
	t.Cleanup(func() { skysettings.Reset() })

	sink := httptest.NewServer(loadtest.Handler())
	t.Cleanup(sink.Close)
	sinkAddr := sink.Listener.Addr().String()
	_, portStr, err := net.SplitHostPort(sinkAddr)
	if err != nil {
		t.Fatalf("sink addr: %v", err)
	}
	sinkPort, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatalf("sink port: %v", err)
	}

	base := baseKnobs(cfg)
	var rows []row
	var refDown, refUp float64

	// seed keeps every emulated link's loss pattern distinct but reproducible.
	var seed int64
	nextSeed := func() int64 { seed += 2; return seed }

	// build is the per-cell rig: n active tunnels of `legsPer` legs each, plus
	// `standby` pooled tunnels of one hop leg.
	build := func(t *testing.T, n, legsPer, standby int, direct bool) ([]*tunnel, []*tunnel, *proxy) {
		t.Helper()
		var active, pool []*tunnel
		for i := 0; i < n; i++ {
			legs := make([]router.EmuLegSpec, 0, legsPer)
			for j := 0; j < legsPer; j++ {
				if direct {
					legs = append(legs, cfg.directLeg(nextSeed()))
				} else {
					legs = append(legs, cfg.hopLeg(fmt.Sprintf("t%d-l%d", i, j), i+j, nextSeed()))
				}
			}
			active = append(active, newTunnel(t, fmt.Sprintf("tun%d", i), legs))
			active[len(active)-1].rig.SetRole("active")
		}
		for i := 0; i < standby; i++ {
			pool = append(pool, newTunnel(t, fmt.Sprintf("pool%d", i),
				[]router.EmuLegSpec{cfg.hopLeg(fmt.Sprintf("p%d", i), i, nextSeed())}))
			pool[len(pool)-1].rig.SetRole("standby")
		}
		return active, pool, newProxy(t, cfg, active, pool, sinkPort)
	}

	// assertLegDiscipline is the emu bench's own gate for #5094/#5096: after
	// everything a cell did, a STANDBY tunnel must still hold exactly one
	// leg, and an ACTIVE tunnel must hold no more than the cell's configured
	// width — whatever establishMuxRoutes, self-heal or the arbiter did in
	// between must never have widened a pool tunnel.
	// assertIntegrity is the bench's INTEGRITY gate, checked cell by cell against
	// the counters as they stood after the previous cell (they are process-wide
	// and cumulative). delivery_crc_failures moving means the mux handed the app
	// bytes that are not what the sender wrote, in that order — the #4005 class of
	// defect the external object hash used to be the only witness to.
	// aead_failures moving means a frame failed its per-frame AEAD. Both must stay
	// flat on every emulated cell, however lossy the legs are: loss and reordering
	// are recovered by SACK/FEC/reorder, never by delivering wrong bytes.
	integrityBase := router.MuxCountersSnapshot()
	assertIntegrity := func(t *testing.T) {
		t.Helper()
		got := router.MuxCountersSnapshot()
		if d := got.DeliveryCRCFailures - integrityBase.DeliveryCRCFailures; d != 0 {
			t.Errorf("delivery_crc_failures advanced by %d: the mux delivered corrupted or misordered bytes", d)
		}
		if d := got.AEADFailures - integrityBase.AEADFailures; d != 0 {
			t.Errorf("aead_failures advanced by %d: a mux frame failed its per-frame AEAD", d)
		}
		integrityBase = got
	}

	assertLegDiscipline := func(t *testing.T, legsPer int, active, pool []*tunnel) {
		t.Helper()
		// Every cell ends in assertLegDiscipline, so the integrity gate rides along
		// here rather than being repeated at each call site.
		assertIntegrity(t)
		for _, tn := range active {
			if n := tn.rig.AliveLegs(); n > legsPer {
				t.Errorf("%s: role=%q legs=%d exceeds the cell's configured width %d", tn.name, tn.rig.Role(), n, legsPer)
			}
		}
		for _, tn := range pool {
			if n := tn.rig.AliveLegs(); n != 1 {
				t.Errorf("%s: role=%q legs=%d, want exactly 1 for a standby tunnel", tn.name, tn.rig.Role(), n)
			}
		}
	}

	// 1. ref — one tunnel, one direct leg. Every ratio below is against this.
	t.Run("ref", func(t *testing.T) {
		if !skysettings.Apply(base) && skysettings.Version() == 0 {
			t.Log("knobs unchanged")
		}
		active, _, p := build(t, 1, 1, 0, true)
		r := record(t, &rows, "ref", "down", download(t, p, sinkAddr, cfg.down, cfg, nil), active, "")
		refDown = r.bps
		rows[len(rows)-1].ratio = 1
		assertLegDiscipline(t, 1, active, nil)

		activeUp, _, pUp := build(t, 1, 1, 0, true)
		ru := record(t, &rows, "ref", "up", upload(t, pUp, sinkAddr, cfg.up, cfg), activeUp, "")
		refUp = ru.bps
		rows[len(rows)-1].ratio = 1
		assertLegDiscipline(t, 1, activeUp, nil)
	})

	// ratio fills in the reference column once the reference exists.
	ratio := func(r *row) {
		ref := refDown
		if r.dir == "up" {
			ref = refUp
		}
		if ref > 0 {
			r.ratio = r.bps / ref
		}
	}
	cell := func(t *testing.T, name string, n, legsPer int, dirs ...string) {
		t.Helper()
		skysettings.Apply(base) //nolint:errcheck // wholesale install; the return says only whether it moved
		for _, dir := range dirs {
			active, pool, p := build(t, n, legsPer, 0, false)
			var x xfer
			if dir == "down" {
				x = download(t, p, sinkAddr, cfg.down, cfg, nil)
			} else {
				x = upload(t, p, sinkAddr, cfg.up, cfg)
			}
			record(t, &rows, name, dir, x, active, "")
			ratio(&rows[len(rows)-1])
			assertLegDiscipline(t, legsPer, active, pool)
		}
	}

	// 2. tunnels-2 — two tunnels of one leg each.
	t.Run("tunnels-2", func(t *testing.T) { cell(t, "tunnels-2", 2, 1, "down", "up") })

	// 3. legs-2 — one tunnel of two legs (the mux's own aggregation).
	t.Run("legs-2", func(t *testing.T) { cell(t, "legs-2", 1, 2, "down", "up") })

	// 4. compose-2x2 — two tunnels of two legs: the planner over the scheduler.
	t.Run("compose-2x2", func(t *testing.T) { cell(t, "compose-2x2", 2, 2, "down", "up") })

	// 5. spread-3 — criterion 10: no route over 40 % of the object, at least
	// three routes carrying. This one is a HARD assert, both directions.
	t.Run("spread-3", func(t *testing.T) {
		skysettings.Apply(withKnobs(base, map[string]int64{ //nolint:errcheck
			skysettings.SpreadMaxShare:  mustRatio(t, "0.4"),
			skysettings.SpreadMinRoutes: 3,
		}))
		for _, dir := range []string{"down", "up"} {
			active, _, p := build(t, 3, 1, 0, false)
			var x xfer
			size := cfg.down
			if dir == "down" {
				x = download(t, p, sinkAddr, cfg.down, cfg, nil)
			} else {
				size = cfg.up
				x = upload(t, p, sinkAddr, cfg.up, cfg)
			}
			_, sh := shares(active, dir == "down")
			carried, top := 0, 0.0
			for _, s := range sh {
				if s > 0.01 {
					carried++
				}
				if s > top {
					top = s
				}
			}
			slack := float64(cfg.chunk) / float64(size)
			record(t, &rows, "spread-3", dir, x, active,
				fmt.Sprintf("spread.max_share=0.4 min_routes=3: %d routes carried, top=%.1f%% (cap %.1f%%)",
					carried, 100*top, 100*(0.4+slack)))
			ratio(&rows[len(rows)-1])
			if carried < 3 {
				t.Errorf("%s: only %d route(s) carried bytes, want at least min_routes=3", dir, carried)
			}
			if top > 0.4+slack {
				t.Errorf("%s: top route carried %.1f%% of the object, over the %.1f%% cap",
					dir, 100*top, 100*(0.4+slack))
			}
			assertLegDiscipline(t, 1, active, nil)
		}
	})

	// 6. standby-cut — 2 active + 4 standby; one active tunnel's legs are
	// black-holed a quarter of the way into the download. The object must still
	// arrive intact and the next byte must land inside two seconds.
	t.Run("standby-cut", func(t *testing.T) {
		// The cut is `skywire cli tp rm` on every leg of one active tunnel:
		// the legs are black-holed AND their transports are taken away, so the
		// route group closes and the tunnel dies the way a visor-side teardown
		// kills it. That is the shape the live degrade row measures, and the
		// one the client has a prompt answer for — the session's death re-issues
		// its outstanding chunks on a live tunnel free of budget.
		//
		// A leg that is ONLY black-holed (rig.Leg(i).Cut alone, no transport
		// removal) is a different and much slower case: the socket is still
		// open, so recovery waits on tunnel.snub_after (20 s, sized for the
		// live mesh's reorder wedges) or on the liveness path's hard-dead
		// window. That case belongs in a cell of its own with the snub knobs
		// swept, not in a bar that claims two seconds.
		skysettings.Apply(base) //nolint:errcheck
		active, pool, p := build(t, 2, 1, 4, false)
		all := append(append([]*tunnel{}, active...), pool...)

		clk := new(readClock)
		cutAt := int64(float64(cfg.down) * cfg.cutAtPercent / 100)
		cutDone := make(chan struct{})
		go func() {
			defer close(cutDone)
			deadline := time.Now().Add(cfg.timeout)
			for time.Now().Before(deadline) {
				if clk.got() >= cutAt {
					clk.mark()
					for i := 0; i < active[0].rig.Legs(); i++ {
						active[0].rig.Leg(i).RemoveTransport()
					}
					return
				}
				time.Sleep(5 * time.Millisecond)
			}
		}()
		x := download(t, p, sinkAddr, cfg.down, cfg, clk)
		<-cutDone

		gap := clk.gap()
		held, standby, _, _, _ := p.client.StandbyPoolState()
		record(t, &rows, "standby-cut", "down", x, all,
			fmt.Sprintf("cut tun0's legs at %d bytes; widest byte-free gap after the cut %v; pool held=%d standby=%d",
				cutAt, gap, held, standby))
		ratio(&rows[len(rows)-1])
		if gap < 0 {
			t.Error("no byte landed after the cut — the transfer never resumed")
		} else if gap >= 2*time.Second {
			t.Errorf("time to first byte after the cut = %v, want under 2s", gap)
		}
		assertLegDiscipline(t, 1, active, pool)

		// #5094/#5096: after exactly this kind of cut, the live router's
		// background establishMuxRoutes looks for a tunnel to widen. The
		// harness has no Router.DialRoutes to launch that routine for real,
		// so drive its own gate (dialMuxTarget) directly on a still-standby
		// pool tunnel — the shape of the regression the rig run of
		// 2026-09-22 hit (all 30 pooled tunnels grown to 2 legs before their
		// role could clamp the dial).
		pool[0].rig.SimulateEstablishMuxRoutesWiden(2) //nolint:errcheck // return checked via assertLegDiscipline below
		assertLegDiscipline(t, 1, active, pool)
	})

	// 7. up2 — two concurrent uploads on tunnels-2; the row is their sum.
	t.Run("up2", func(t *testing.T) {
		skysettings.Apply(base) //nolint:errcheck
		active, _, p := build(t, 2, 1, 0, false)
		var wg sync.WaitGroup
		out := make([]xfer, 2)
		start := time.Now()
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				out[i] = upload(t, p, sinkAddr, cfg.up, cfg)
			}(i)
		}
		wg.Wait()
		sum := xfer{bytes: 2 * cfg.up, elapsed: time.Since(start), hashOK: true}
		for _, x := range out {
			sum.got += x.got
			sum.hashOK = sum.hashOK && x.hashOK
			if x.err != nil && sum.err == nil {
				sum.err = x.err
			}
		}
		record(t, &rows, "up2", "up", sum, active,
			fmt.Sprintf("two concurrent %d-byte uploads; the row is their sum", cfg.up))
		ratio(&rows[len(rows)-1])
		assertLegDiscipline(t, 1, active, nil)
	})

	// The summary table, in the live bench's column order.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].dir > rows[j].dir })
	var b strings.Builder
	b.WriteString("\nname\tdir\tsize\tgoodput_Bps\tratio\twire/goodput\thash_ok\n")
	for _, r := range rows {
		b.WriteString(r.tsv())
		b.WriteString("\n")
	}
	b.WriteString("\nper-tunnel / per-leg wire bytes\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%-12s %-4s %s\n", r.name, r.dir, r.share)
	}
	t.Log(b.String())
}

// mustRatio parses a ratio knob the way the CLI does, so a test writes 0.4
// rather than its IEEE-754 bit pattern.
func mustRatio(t *testing.T, v string) int64 {
	t.Helper()
	n, err := skysettings.Parse(skysettings.SpreadMaxShare, v)
	if err != nil {
		t.Fatalf("parse %s=%s: %v", skysettings.SpreadMaxShare, v, err)
	}
	return n
}
