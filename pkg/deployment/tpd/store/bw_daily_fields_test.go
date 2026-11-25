package store

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/telemetrywire"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// The scripts' label(hex) is cipher.DNSLabel.
func TestReporterLabelLuaMatchesGo(t *testing.T) {
	s := newTestRedisStore(t)
	ctx := context.Background()
	for range 200 {
		pk, _ := cipher.GenerateKeyPair()
		got, err := s.client.Eval(ctx, luaReporterLabel+"return label(ARGV[1])", nil, pk.Hex()).Text()
		require.NoError(t, err)
		require.Equal(t, pk.DNSLabel(), got)
	}
}

// bwFixture is a small network: some transports registered, some expired,
// reported by one or both edges over three days, with checkpoints and a
// same-network day.
type bwFixture struct {
	pks     []cipher.PubKey
	entries []*transport.Entry
}

func newBWFixture() bwFixture {
	var f bwFixture
	for range 6 {
		pk, _ := cipher.GenerateKeyPair()
		f.pks = append(f.pks, pk)
	}
	for i := range 12 {
		a, b := f.pks[i%6], f.pks[(i*5+1)%6]
		if a == b {
			b = f.pks[(i+2)%6]
		}
		f.entries = append(f.entries, &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: []tptypes.Type{"stcpr", "sudph", "dmsg"}[i%3]})
	}
	return f
}

// write applies round r of the fixture's reports through the store's writers.
func (f bwFixture) write(t *testing.T, s *redisStore, r uint64) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	if r == 0 {
		var live []*transport.SignedEntry
		for i, e := range f.entries {
			if i%2 == 0 {
				live = append(live, &transport.SignedEntry{Entry: e})
			}
		}
		require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, live))
	}
	for back := 2; back >= 0; back-- {
		at := now.AddDate(0, 0, -back)
		var ups []TelemetryUpdate
		for i, e := range f.entries {
			n := uint64(i+1)*1000 + r*7919 + uint64(2-back)*313
			if r == 0 {
				n = 0
			}
			ups = append(ups, TelemetryUpdate{ID: e.ID, Reporter: e.Edges[0], Sent: n, Recv: n / 3, Type: string(e.Type), At: at,
				SameNetwork: i == 3, LatMin: 1, LatMax: 3, LatAvg: 2})
			if i%3 != 0 {
				ups = append(ups, TelemetryUpdate{ID: e.ID, Reporter: e.Edges[1], Sent: n / 2, Recv: n, Type: string(e.Type), At: at})
			}
		}
		require.NoError(t, s.ApplyTelemetry(ctx, ups))
	}
	for i, e := range f.entries[:4] {
		ep := uint64(i + 1)
		var sent, recv []telemetrywire.Checkpoint
		for k := uint64(1); k <= r+1; k++ {
			sent = append(sent, cp(k, k))
			recv = append(recv, cp(k, k+uint64(i%2)*r))
		}
		require.NoError(t, s.ApplyCheckpoints(ctx, e.ID, e.Edges[0], dir(ep, sent...), dir(0)))
		require.NoError(t, s.ApplyCheckpoints(ctx, e.ID, e.Edges[1], dir(0), dir(ep, recv...)))
	}
}

// bwReadAll is every reader's answer over the bw:daily hashes, from a new
// store on the same database so no cache carries over.
func (f bwFixture) readAll(t *testing.T) map[string]any {
	t.Helper()
	ctx := context.Background()
	s := openTestRedisStore(t)
	defer s.Close()
	ids := make([]uuid.UUID, len(f.entries))
	for i, e := range f.entries {
		ids[i] = e.ID
	}
	byID := func(m []TransportMetric) []TransportMetric {
		slices.SortFunc(m, func(a, b TransportMetric) int { return strings.Compare(a.ID, b.ID) })
		return m
	}
	out := map[string]any{}
	for _, days := range []int{1, 3} {
		q := MetricsQuery{Days: days, Live: "all", Edges: true, Bandwidth: true, Latency: true}
		all, err := s.GetAllTransportMetrics(ctx, q)
		require.NoError(t, err)
		out["all"+strconv.Itoa(days)] = byID(all)
		some, err := s.GetTransportMetricsByIDs(ctx, ids, q)
		require.NoError(t, err)
		out["ids"+strconv.Itoa(days)] = byID(some)
		vis, err := s.GetTransportMetricsByVisors(ctx, f.pks, q)
		require.NoError(t, err)
		out["visors"+strconv.Itoa(days)] = byID(vis)
		netm, err := s.GetNetworkMetrics(ctx, q)
		require.NoError(t, err)
		out["net"+strconv.Itoa(days)] = netm
		agg, err := s.GetVisorAggregateMetrics(ctx, f.pks, q)
		require.NoError(t, err)
		out["agg"+strconv.Itoa(days)] = agg
	}
	today, err := s.GetTodayTransportMetrics(ctx)
	require.NoError(t, err)
	out["today"] = byID(today)
	for _, id := range ids {
		bw, err := s.GetTransportBandwidth(ctx, id, "daily", 3)
		require.NoError(t, err)
		zeroUpdatedAt(bw)
		out["tp:"+id.String()] = bw
	}
	for _, pk := range f.pks {
		bw, err := s.GetVisorBandwidth(ctx, pk, "daily", 3)
		require.NoError(t, err)
		slices.SortFunc(bw, func(a, b BandwidthAggregation) int { return strings.Compare(a.PeriodKey, b.PeriodKey) })
		zeroUpdatedAt(bw)
		out["visor:"+pk.Hex()] = bw
	}
	ix, err := s.scanBandwidthIndex(ctx)
	require.NoError(t, err)
	out["ix"] = [3]any{ix.byID, ix.edges, ix.types}
	now := time.Now().UTC()
	for back := range 3 {
		date := now.AddDate(0, 0, -back).Format(MetricsDateFormat)
		h, err := s.client.HGetAll(ctx, s.netDailyKey(date)).Result()
		require.NoError(t, err)
		out["netDaily:"+date] = h
	}
	return out
}

// bwRewriteFields moves part of every label field's value to its hex field:
// all of it for share 1, a third for share 3. vck flags move whole.
func bwRewriteFields(t *testing.T, s *redisStore, share uint64) {
	t.Helper()
	ctx := context.Background()
	keys, err := s.client.Keys(ctx, serviceName+":bw:daily:*").Result()
	require.NoError(t, err)
	require.NotEmpty(t, keys)
	for _, k := range keys {
		h, err := s.client.HGetAll(ctx, k).Result()
		require.NoError(t, err)
		for field, v := range h {
			pk, suffix, ok := reporterFieldSplit(field)
			if !ok || len(field) == 2*len(pk)+1+len(suffix) {
				continue
			}
			hexField := pk.Hex() + ":" + suffix
			n, err := strconv.ParseUint(v, 10, 64)
			require.NoError(t, err)
			moved := n / share
			if suffix == verifiedCheckField || share == 1 {
				moved = n
			}
			require.NoError(t, s.client.HIncrBy(ctx, k, hexField, int64(moved)).Err()) //nolint:gosec // test counters are small
			if moved == n {
				require.NoError(t, s.client.HDel(ctx, k, field).Err())
			} else {
				require.NoError(t, s.client.HIncrBy(ctx, k, field, -int64(moved)).Err()) //nolint:gosec // test counters are small
			}
		}
	}
}

// Every bw:daily reader answers the same whether a hash's reporter fields
// are in the label form, the hex form of hashes written before it, or split
// across both, and writes on top of a hex hash end where label ones do.
func TestBandwidthDailyFieldFormsReadTheSame(t *testing.T) {
	if now := time.Now().UTC(); now.Hour() == 23 && now.Minute() >= 58 {
		t.Skip("too close to UTC midnight for a same-day fixture")
	}
	s := newTestRedisStore(t)
	ctx := context.Background()
	f := newBWFixture()
	f.write(t, s, 0)
	f.write(t, s, 1)

	keys, err := s.client.Keys(ctx, serviceName+":bw:daily:*").Result()
	require.NoError(t, err)
	for _, k := range keys {
		enc, err := s.client.ObjectEncoding(ctx, k).Result()
		require.NoError(t, err)
		require.Equal(t, "listpack", enc, k)
	}

	want := f.readAll(t)
	bwRewriteFields(t, s, 1)
	requireSameReads(t, want, f.readAll(t), "hex fields")
	f.write(t, s, 2)
	afterHex := f.readAll(t)

	require.NoError(t, s.client.FlushDB(ctx).Err())
	f.write(t, s, 0)
	f.write(t, s, 1)
	bwRewriteFields(t, s, 3)
	requireSameReads(t, want, f.readAll(t), "split fields")
	f.write(t, s, 2)
	afterSplit := f.readAll(t)

	require.NoError(t, s.client.FlushDB(ctx).Err())
	f.write(t, s, 0)
	f.write(t, s, 1)
	f.write(t, s, 2)
	afterLabel := f.readAll(t)
	requireSameReads(t, afterLabel, afterHex, "writes on hex fields")
	requireSameReads(t, afterLabel, afterSplit, "writes on split fields")
}

// requireSameReads compares two bwFixture.readAll results reader by reader.
func requireSameReads(t *testing.T, want, got map[string]any, msg string) {
	t.Helper()
	require.ElementsMatch(t, slices.Collect(maps.Keys(want)), slices.Collect(maps.Keys(got)), msg)
	for k, w := range want {
		wj, err := json.Marshal(w)
		require.NoError(t, err)
		gj, err := json.Marshal(got[k])
		require.NoError(t, err)
		require.JSONEq(t, string(wj), string(gj), "%s: %s", msg, k)
	}
}

// zeroUpdatedAt clears the write time, which differs between two runs of the
// same writes.
func zeroUpdatedAt(bw []BandwidthAggregation) {
	for i := range bw {
		bw[i].UpdatedAt = 0
	}
}
