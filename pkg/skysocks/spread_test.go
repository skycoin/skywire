// Package skysocks pkg/skysocks/spread_test.go — the spread policy's tables
// and its two end-to-end share bounds.
package skysocks

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/0magnet/yamux"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// planChunk is the size of every chunk the tables below place.
const planChunk = int64(1 << 20)

// runPlan plays `chunks` equal chunks through spreadChoose and returns the
// bytes each tunnel ended up with — the planner's whole decision, without a
// tunnel, a stream or a byte of network.
func runPlan(capsBps []float64, chunks int, p spreadPolicy) []int64 {
	carried := make([]int64, len(capsBps))
	for i := 0; i < chunks; i++ {
		idx := spreadChoose(capsBps, carried, p)
		if idx < 0 {
			break
		}
		carried[idx] += planChunk
	}
	return carried
}

// mustRatio parses a knob value the way the CLI does, so a test sets 0.4 by
// typing 0.4 rather than by pasting its IEEE-754 bit pattern.
func mustRatio(t *testing.T, name, raw string) int64 {
	t.Helper()
	v, err := skysettings.Parse(name, raw)
	require.NoError(t, err)
	return v
}

func shareOf(carried []int64, i int) float64 {
	var total int64
	for _, n := range carried {
		total += n
	}
	if total == 0 {
		return 0
	}
	return float64(carried[i]) / float64(total)
}

// The default weighting is capacity-proportional: an 8 MB/s tunnel beside a
// 3 MB/s one takes 8/11 of the object, not half of it. This is the whole fix
// for "the slow tunnel drags the pair below itself" — the slow tunnel gets
// fewer chunks rather than an equal number of them.
func TestSpreadChooseIsCapacityProportional(t *testing.T) {
	caps := []float64{8 << 20, 3 << 20}
	carried := runPlan(caps, 110, spreadPolicy{maxShare: 1})

	require.InDelta(t, 0.727, shareOf(carried, 0), 0.02, "the 8 MB/s tunnel takes 8/11")
	require.InDelta(t, 0.273, shareOf(carried, 1), 0.02, "the 3 MB/s tunnel takes 3/11")
}

// weight=even is the privacy end of the spectrum: the shares are equal whatever
// the tunnels have been measured at, so the bytes say nothing about which route
// is fastest.
func TestSpreadChooseEvenIgnoresCapacity(t *testing.T) {
	caps := []float64{8 << 20, 3 << 20, 1 << 20}
	carried := runPlan(caps, 99, spreadPolicy{maxShare: 1, even: true})

	for i := range caps {
		require.InDelta(t, 1.0/3, shareOf(carried, i), 0.02, "tunnel %d", i)
	}
}

// An unmeasured tunnel is credited the best capacity present rather than
// starved — the #4965 rule, applied to shares. A tunnel that never gets a chunk
// is never measured, and a tunnel that is never measured never gets a chunk.
func TestSpreadChooseProbesAnUnmeasuredTunnel(t *testing.T) {
	carried := runPlan([]float64{8 << 20, 0}, 20, spreadPolicy{maxShare: 1})
	require.Positive(t, carried[1], "the unproven tunnel must be given something to prove")
	require.InDelta(t, 0.5, shareOf(carried, 1), 0.05)
}

// max_share 0.4 over three tunnels: nobody finishes above 40 % of the object,
// give or take the one chunk a tunnel may be handed while it is still under the
// cap. The tunnels are deliberately UNEQUAL — a cap that only holds when the
// capacities already agree holds nothing.
func TestSpreadChooseHoldsTheCap(t *testing.T) {
	caps := []float64{9 << 20, 3 << 20, 1 << 20}
	const chunks = 100
	carried := runPlan(caps, chunks, spreadPolicy{maxShare: 0.4})

	slack := 1.0 / float64(chunks) // one chunk
	for i := range caps {
		require.LessOrEqualf(t, shareOf(carried, i), 0.4+slack, "tunnel %d is over its cap", i)
	}
	require.Greater(t, shareOf(carried, 0), 0.3, "the fastest tunnel still takes the most it may")
}

// A cap must never be a reason to stall an object: with every tunnel at or over
// its share the cap is ignored and the chunk is still placed.
func TestSpreadChooseNeverStallsOnTheCap(t *testing.T) {
	// One tunnel cannot be under a 0.4 cap of itself, ever.
	require.Equal(t, 0, spreadChoose([]float64{1 << 20}, []int64{1 << 30}, spreadPolicy{maxShare: 0.4}))
	require.Equal(t, -1, spreadChoose(nil, nil, spreadPolicy{maxShare: 0.4}), "nothing to place it on")
}

// Off is off: with no knob set the policy steers nothing and the planner hands
// the choice back to pickSessionFor, so an unset client places chunks exactly
// as it does today.
func TestSpreadOffLeavesThePickOrderAlone(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	require.False(t, spreadPolicyNow().steers())

	c := &Client{}
	pl := c.newSpreadPlanner(spreadDown)
	require.Nil(t, pl.pick(), "off, the planner defers to the picker")

	require.True(t, skysettings.Apply(map[string]int64{skysettings.SpreadMinRoutes: 3}))
	require.True(t, spreadPolicyNow().steers())
}

// The endgame duplicates the TAIL and nothing else: while more chunks remain
// than there are tunnels, a duplicate would take a tunnel away from a chunk
// that has not been fetched at all.
func TestSpreadDuplicatesOnlyTheTail(t *testing.T) {
	on := spreadPolicy{maxShare: 1, endgame: true}
	off := spreadPolicy{maxShare: 1}

	require.False(t, spreadDuplicates(10, 3, on), "10 chunks left over 3 tunnels: no tail yet")
	require.False(t, spreadDuplicates(3, 3, on), "exactly one chunk each is not a tail")
	require.True(t, spreadDuplicates(2, 3, on))
	require.True(t, spreadDuplicates(1, 3, on))
	require.False(t, spreadDuplicates(0, 3, on))
	require.False(t, spreadDuplicates(1, 1, on), "one tunnel cannot duplicate onto itself")
	require.False(t, spreadDuplicates(1, 3, off), "off by default")
}

// The endgame on the real admission path: a duplicate is armed for the tail
// chunks only, at most one per chunk, and only onto a tunnel that is idle. The
// first answer wins and the loser's bytes are dropped — the reassembled object
// is unchanged.
func TestSpreadEndgameDuplicatesTheTailChunksOnly(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	var sessions []*yamux.Session
	stamps := map[*yamux.Session]*tunnelMeter{}
	for i := 0; i < 3; i++ {
		s, done := newTestSession(t)
		defer done() //nolint:gocritic // one deferred close per session is the point
		sessions = append(sessions, s)
		stamps[s] = new(tunnelMeter)
	}
	c := &Client{sessions: sessions, recvStamp: stamps, closeC: make(chan struct{})}
	c.rs = rangeSplitConfig{enabled: true, concurrency: 2, chunkSize: 1 << 10}
	require.True(t, skysettings.Apply(map[string]int64{skysettings.SpreadEndgame: 1}))
	pl := c.newSpreadPlanner(spreadDown)
	require.True(t, pl.pol.endgame)

	const chunkSize, total = int64(1 << 10), int64(8 << 10) // 8 chunks
	var mu sync.Mutex
	attempts := map[int64]int{}
	pinned := map[int64]int{}
	f := c.startChunkFetchesPlanned(0, total, chunkSize, pl, func(start, end int64, _ rsProgress, p chunkPlacement) ([]byte, error) {
		mu.Lock()
		attempts[start]++
		if p.pin != nil {
			pinned[start]++
		}
		mu.Unlock()
		return bytes.Repeat([]byte{'x'}, int(end-start+1)), nil
	})

	cli, srv := net.Pipe()
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(srv) //nolint:errcheck
		got <- b
	}()
	f.writeInOrder(cli, nil)
	cli.Close() //nolint:errcheck,gosec
	body := <-got
	require.Len(t, body, int(total), "the duplicate must not add a byte to the object")

	mu.Lock()
	defer mu.Unlock()
	for start := int64(0); start < total; start += chunkSize {
		tail := total-start < int64(len(sessions))*chunkSize
		require.LessOrEqual(t, attempts[start], 2, "at most one duplicate per chunk")
		if tail {
			require.LessOrEqual(t, pinned[start], 1, "chunk %d: at most one duplicate", start)
		} else {
			require.Zero(t, pinned[start], "chunk %d is not in the tail and must not be duplicated", start)
		}
	}
	require.Positive(t, pinned[total-chunkSize]+pinned[total-2*chunkSize], "the tail was never duplicated at all")
}

// The ledger is kept whatever the knobs say — that is where the `shares=` line
// of a completion log comes from — and settle corrects a reservation to what
// the tunnel actually carried.
func TestSpreadPlannerLedgerAndShares(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	s0, c0 := newTestSession(t)
	defer c0()
	s1, c1 := newTestSession(t)
	defer c1()

	c := &Client{}
	pl := c.newSpreadPlanner(spreadDown)
	pl.charge(s0, 6<<20)
	pl.charge(s1, 4<<20)
	require.InDelta(t, 0.6, pl.topShare(), 1e-9)

	// The attempt on s0 delivered nothing: the reservation goes back.
	pl.settle(s0, 6<<20, 0)
	require.InDelta(t, 1.0, pl.topShare(), 1e-9, "s1 is now the whole object")
	require.Equal(t, "shares=0:100%,0:0%", pl.sharesLine(), "ports are 0 on a test conn")

	pl.charge(s0, 4<<20)
	require.InDelta(t, 0.5, pl.topShare(), 1e-9)
}

// min_routes is reached by PROMOTING standby tunnels — the pool the keepalive
// loop already filled — and never by dialing: a spread policy asking for more
// routes than were discovered runs on the ones that were.
func TestEnsureMinRoutesPromotesStandbysAndNeverDials(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	active, closeA := newTestSession(t)
	defer closeA()
	sb1, close1 := newTestSession(t)
	defer close1()
	sb2, close2 := newTestSession(t)
	defer close2()

	mA, m1, m2 := new(tunnelMeter), new(tunnelMeter), new(tunnelMeter)
	mA.rttMs, m1.rttMs, m2.rttMs = 40, 20, 60
	c := &Client{
		sessions:  []*yamux.Session{active, sb1, sb2},
		recvStamp: map[*yamux.Session]*tunnelMeter{active: mA, sb1: m1, sb2: m2},
		standby:   map[*yamux.Session]bool{sb1: true, sb2: true},
		closeC:    make(chan struct{}),
	}
	require.Equal(t, 1, c.activeLiveCount())

	require.Equal(t, 3, c.ensureMinRoutes(3, "test"), "both standbys promoted")
	require.False(t, c.IsStandby(sb1))
	require.False(t, c.IsStandby(sb2))

	// Asking for more than the pool holds is not an error and dials nothing:
	// the object runs on the width that exists.
	require.Equal(t, 3, c.ensureMinRoutes(8, "test"))
	require.Len(t, c.sessions, 3, "ensureMinRoutes must never add a tunnel")
}

// The planner applies min_routes before the first chunk goes out, through the
// same promote path.
func TestSpreadPlannerEnsureRoutesUsesThePool(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	active, closeA := newTestSession(t)
	defer closeA()
	sb, closeS := newTestSession(t)
	defer closeS()

	c := &Client{
		sessions:  []*yamux.Session{active, sb},
		recvStamp: map[*yamux.Session]*tunnelMeter{active: new(tunnelMeter), sb: new(tunnelMeter)},
		standby:   map[*yamux.Session]bool{sb: true},
		closeC:    make(chan struct{}),
	}
	require.Zero(t, c.newSpreadPlanner(spreadDown).ensureRoutes(), "off, nothing is promoted")
	require.True(t, c.IsStandby(sb))

	require.True(t, skysettings.Apply(map[string]int64{skysettings.SpreadMinRoutes: 2}))
	require.Equal(t, 2, c.newSpreadPlanner(spreadDown).ensureRoutes())
	require.False(t, c.IsStandby(sb))
}

// newSpreadTestClient builds a client with `tunnels` ACTIVE tunnels and
// `standby` pooled ones, every one of them a real yamux session to its own fake
// exit spliced to backendAddr. It returns the proxy address and the client, so
// a test can read the ledger the planner kept for the last object.
func newSpreadTestClient(t *testing.T, backendAddr string, tunnels, standby int, conc int, chunk int64) (string, *Client) {
	t.Helper()
	pair := func() net.Conn {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		require.NoError(t, err)
		defer ln.Close() //nolint:errcheck
		dialed := make(chan net.Conn, 1)
		go func() {
			c, _ := ln.Accept() //nolint:errcheck
			dialed <- c
		}()
		cli, err := net.Dial("tcp", ln.Addr().String())
		require.NoError(t, err)
		go rsFakeExit(t, <-dialed, backendAddr)
		return cli
	}

	client, err := NewClient(pair(), nil)
	require.NoError(t, err)
	for i := 1; i < tunnels; i++ {
		require.NoError(t, client.AddTunnel(pair()))
	}
	for i := 0; i < standby; i++ {
		require.NoError(t, client.AddStandbyTunnel(pair()))
	}
	client.SetRangeSplit(true, conc, chunk)
	t.Cleanup(func() { _ = client.Close() }) //nolint:errcheck

	probe, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	proxyAddr := probe.Addr().String()
	probe.Close()                                        //nolint:errcheck,gosec
	go func() { _ = client.ListenAndServe(proxyAddr) }() //nolint:errcheck
	for i := 0; i < 200; i++ {
		if cc, err := net.Dial("tcp", proxyAddr); err == nil {
			cc.Close() //nolint:errcheck,gosec
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	return proxyAddr, client
}

// spreadShares reads the ledger of the most recently completed object off the
// client, the way the completion log line does.
func spreadTopShare(t *testing.T, pl *spreadPlanner, want float64, slack float64, what string) {
	t.Helper()
	require.NotNil(t, pl, "%s: no ledger was kept", what)
	sh := pl.shares()
	var total int64
	seen := map[*yamux.Session]bool{}
	for _, s := range sh {
		require.NotNil(t, s.sess)
		require.Falsef(t, seen[s.sess], "%s: a tunnel appears twice in the ledger", what)
		seen[s.sess] = true
		total += s.bytes
	}
	t.Logf("%s: %s over %d tunnels (%d bytes booked), top=%.1f%%", what, pl.sharesLine(), len(sh), total, 100*pl.topShare())
	require.GreaterOrEqual(t, len(sh), 3, "%s: the object must be spread over at least min_routes tunnels", what)
	require.LessOrEqual(t, pl.topShare(), want+slack, "%s: a tunnel carried more than its cap", what)
}

// The criterion-10 bound, end to end, on the machinery a live run uses: a 50 MB
// download under max_share 0.4 / min_routes 3 puts no more than 40 % of the
// object — give or take the one chunk a tunnel may be handed while it is still
// under the cap — on any single tunnel, and spreads it over three.
//
// The client starts with TWO active tunnels and two in the pool, so the test
// also proves min_routes reaching into the standby pool for the third.
func TestSpreadBoundsTheShareOfA50MBDownload(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	const blobSize = 50 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*31 + 7)
	}
	want := sha256.Sum256(blob)

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", "\"spreadv1\"")
		http.ServeContent(w, r, "blob.bin", time.Unix(0, 0), bytes.NewReader(blob))
	}))
	defer backend.Close()

	const chunk = int64(1 << 20)
	proxy, client := newSpreadTestClient(t, backend.Listener.Addr().String(), 2, 2, 6, chunk)

	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.SpreadMaxShare:  mustRatio(t, skysettings.SpreadMaxShare, "0.4"),
		skysettings.SpreadMinRoutes: 3,
		skysettings.ChunkMaxBytes:   chunk,
		skysettings.ChunkProbeBytes: chunk,
	}))
	require.InDelta(t, 0.4, setSpreadMaxShare(), 1e-9)

	resp := socks5Get(t, proxy, "/blob.bin")
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, 200, resp.StatusCode)
	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Len(t, got, blobSize)
	require.Equal(t, want, sha256.Sum256(got), "the spread object must still be byte-identical")

	// One chunk of a 50 MB object is 2 %.
	spreadTopShare(t, client.lastSpread(), 0.4, float64(chunk)/blobSize, "50 MB download")
}

// The same bound on the upload half: a 50 MB striped POST under max_share 0.4 /
// min_routes 3. The shares are planned on the UPLOAD capacity estimate, which
// is the direction these chunks actually use.
func TestSpreadBoundsTheShareOfA50MBUpload(t *testing.T) {
	t.Cleanup(func() { skysettings.Reset() })
	defer restoreUploadTunables(uploadChunkBytes, uploadMemBytes, uploadStripeMinBytes, uploadConcurrency)()
	uploadChunkBytes = 1 << 20
	uploadMemBytes = 16 << 20
	uploadStripeMinBytes = 1 << 20
	uploadConcurrency = 2

	const blobSize = 50 << 20
	blob := make([]byte, blobSize)
	for i := range blob {
		blob[i] = byte(i*29 + 3)
	}
	want := sha256.Sum256(blob)

	sink := &stubSink{}
	backend := httptest.NewServer(sink.handler())
	defer backend.Close()

	proxy, client := newSpreadTestClient(t, backend.Listener.Addr().String(), 2, 2, 6, 1<<20)
	require.True(t, skysettings.Apply(map[string]int64{
		skysettings.SpreadMaxShare:  mustRatio(t, skysettings.SpreadMaxShare, "0.4"),
		skysettings.SpreadMinRoutes: 3,
	}))

	resp := socks5Upload(t, proxy, blob)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, 200, resp.StatusCode)
	var got struct {
		Bytes  int64  `json:"bytes"`
		Sha256 string `json:"sha256"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	require.EqualValues(t, blobSize, got.Bytes)
	require.Equal(t, hex.EncodeToString(want[:]), got.Sha256)
	require.Zero(t, sink.whole, "the body must be striped, not sent whole")

	spreadTopShare(t, client.lastSpread(), 0.4, float64(uploadChunkBytes)/blobSize, "50 MB upload")
}
