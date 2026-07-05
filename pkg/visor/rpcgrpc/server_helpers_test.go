// Package rpcgrpc — pkg/visor/rpcgrpc/server_helpers_test.go: unit tests
// for the pure server-side helpers that don't need a gRPC stream or a
// VisorAPI mock: the calcMemStore route-finder adapter, the logrus →
// AppLogEntry converter, and the ping-tree candidate/level helpers.
package rpcgrpc

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tpdstore "github.com/skycoin/skywire/pkg/transport-discovery/store"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// TestCalcMemStore pins the in-process route-finder store used by
// StreamCalcRoutes. Only GetTransportsByEdge[NoLatency] carry logic;
// the remaining store.Store methods are stubs that must stay no-op so
// graph construction never accidentally mutates anything.
func TestCalcMemStore(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	pkC, _ := cipher.GenerateKeyPair()

	entries := []*transport.Entry{
		{ID: uuid.New(), Edges: [2]cipher.PubKey{pkA, pkB}, Type: tptypes.STCPR},
		{ID: uuid.New(), Edges: [2]cipher.PubKey{pkB, pkC}, Type: tptypes.SUDPH},
		nil, // nil entries must be skipped without panicking
		// self-loop: only one edge should be recorded
		{ID: uuid.New(), Edges: [2]cipher.PubKey{pkA, pkA}, Type: tptypes.STCP},
	}
	store := newCalcMemStore(entries)
	ctx := context.Background()

	t.Run("GetTransportsByEdge resolves known edges", func(t *testing.T) {
		// pkA is on the A-B edge and the A-A self-loop.
		tps, err := store.GetTransportsByEdge(ctx, pkA)
		if err != nil {
			t.Fatalf("GetTransportsByEdge(pkA): %v", err)
		}
		if len(tps) != 2 {
			t.Errorf("pkA edges = %d, want 2 (A-B + A-A self loop)", len(tps))
		}
		// pkB sits on two distinct transports.
		tps, err = store.GetTransportsByEdge(ctx, pkB)
		if err != nil {
			t.Fatalf("GetTransportsByEdge(pkB): %v", err)
		}
		if len(tps) != 2 {
			t.Errorf("pkB edges = %d, want 2", len(tps))
		}
	})

	t.Run("unknown edge returns ErrTransportNotFound", func(t *testing.T) {
		unknown, _ := cipher.GenerateKeyPair()
		_, err := store.GetTransportsByEdge(ctx, unknown)
		if err != tpdstore.ErrTransportNotFound {
			t.Errorf("unknown edge err = %v, want ErrTransportNotFound", err)
		}
	})

	t.Run("NoLatency variant mirrors GetTransportsByEdge", func(t *testing.T) {
		tps, err := store.GetTransportsByEdgeNoLatency(ctx, pkC)
		if err != nil || len(tps) != 1 {
			t.Errorf("GetTransportsByEdgeNoLatency(pkC) = (%d, %v), want (1, nil)", len(tps), err)
		}
	})

	t.Run("stub methods are inert", func(t *testing.T) {
		// Exercise every stub once. They must not panic and must return
		// the documented zero/no-op values. This keeps the store honest
		// if the store.Store interface grows a method with real intent.
		id := uuid.New()
		if err := store.RegisterTransport(ctx, pkA, nil); err != nil {
			t.Errorf("RegisterTransport: %v", err)
		}
		if err := store.RegisterTransportsBatch(ctx, pkA, nil); err != nil {
			t.Errorf("RegisterTransportsBatch: %v", err)
		}
		if err := store.DeregisterTransport(ctx, id); err != nil {
			t.Errorf("DeregisterTransport: %v", err)
		}
		if _, err := store.GetTransportByID(ctx, id); err != tpdstore.ErrTransportNotFound {
			t.Errorf("GetTransportByID err = %v, want ErrTransportNotFound", err)
		}
		if m, err := store.GetNumberOfTransports(ctx); err != nil || m != nil {
			t.Errorf("GetNumberOfTransports = (%v, %v), want (nil, nil)", m, err)
		}
		if all, err := store.GetAllTransports(ctx, true); err != nil || all != nil {
			t.Errorf("GetAllTransports = (%v, %v), want (nil, nil)", all, err)
		}
		if err := store.UpdateBandwidth(ctx, "tp", pkA, 1, 2); err != nil {
			t.Errorf("UpdateBandwidth: %v", err)
		}
		if err := store.UpdateLatency(ctx, "tp", 1, 2, 3); err != nil {
			t.Errorf("UpdateLatency: %v", err)
		}
		if _, err := store.GetTransportBandwidth(ctx, id, "h", 1); err != nil {
			t.Errorf("GetTransportBandwidth: %v", err)
		}
		if _, err := store.GetVisorBandwidth(ctx, pkA, "h", 1); err != nil {
			t.Errorf("GetVisorBandwidth: %v", err)
		}
		if _, err := store.GetAllVisorSummaries(ctx, true, true); err != nil {
			t.Errorf("GetAllVisorSummaries: %v", err)
		}
		if err := store.RecordHeartbeat(ctx, pkA, "h"); err != nil {
			t.Errorf("RecordHeartbeat: %v", err)
		}
		if m := store.GetDailyTimeline(ctx, "h", time.Unix(0, 0)); m != nil {
			t.Errorf("GetDailyTimeline = %v, want nil", m)
		}
		if err := store.RecordTransportHeartbeat(ctx, id, "h", time.Unix(0, 0)); err != nil {
			t.Errorf("RecordTransportHeartbeat: %v", err)
		}
		if err := store.IngestTransportTimeline(ctx, id, "h", nil); err != nil {
			t.Errorf("IngestTransportTimeline: %v", err)
		}
		if _, err := store.GetTransportUptimeSummaries(ctx, nil, true, true); err != nil {
			t.Errorf("GetTransportUptimeSummaries: %v", err)
		}
		if _, err := store.GetTransportUptimeByVisor(ctx, pkA, true, true); err != nil {
			t.Errorf("GetTransportUptimeByVisor: %v", err)
		}
		if m := store.GetTransportDailyTimeline(ctx, "h", time.Unix(0, 0)); m != nil {
			t.Errorf("GetTransportDailyTimeline = %v, want nil", m)
		}
		if err := store.BackupAndCleanOldBandwidth(ctx, "h"); err != nil {
			t.Errorf("BackupAndCleanOldBandwidth: %v", err)
		}
		if _, err := store.GetNetworkMetrics(ctx, tpdstore.MetricsQuery{}); err != nil {
			t.Errorf("GetNetworkMetrics: %v", err)
		}
		if _, err := store.GetVisorAggregateMetrics(ctx, nil, tpdstore.MetricsQuery{}); err != nil {
			t.Errorf("GetVisorAggregateMetrics: %v", err)
		}
		if _, err := store.GetAllTransportMetrics(ctx, tpdstore.MetricsQuery{}); err != nil {
			t.Errorf("GetAllTransportMetrics: %v", err)
		}
		if _, err := store.GetTransportMetricsByIDs(ctx, nil, tpdstore.MetricsQuery{}); err != nil {
			t.Errorf("GetTransportMetricsByIDs: %v", err)
		}
		if _, err := store.GetTransportMetricsByVisors(ctx, nil, tpdstore.MetricsQuery{}); err != nil {
			t.Errorf("GetTransportMetricsByVisors: %v", err)
		}
		store.Close() // must not panic
	})
}

// TestToAppLogEntry pins the logrus.Entry → wire AppLogEntry mapping:
// the _module field is hoisted to Module, every other field is
// stringified into Fields, and the level/message/timestamp carry over.
func TestToAppLogEntry(t *testing.T) {
	now := time.Now()
	e := &logrus.Entry{
		Time:    now,
		Level:   logrus.WarnLevel,
		Message: "disk almost full",
		Data: logrus.Fields{
			"_module": "skychat",
			"pct":     97,
			"path":    "/var",
		},
	}
	out := toAppLogEntry(e)

	if out.Module != "skychat" {
		t.Errorf("Module = %q, want %q (hoisted from _module)", out.Module, "skychat")
	}
	if out.Level != "warning" {
		t.Errorf("Level = %q, want %q", out.Level, "warning")
	}
	if out.Message != "disk almost full" {
		t.Errorf("Message = %q", out.Message)
	}
	if out.TimestampNs != now.UnixNano() {
		t.Errorf("TimestampNs = %d, want %d", out.TimestampNs, now.UnixNano())
	}
	if _, ok := out.Fields["_module"]; ok {
		t.Error("_module must not leak into Fields")
	}
	if out.Fields["pct"] != "97" {
		t.Errorf("Fields[pct] = %q, want %q (best-effort Sprint)", out.Fields["pct"], "97")
	}
	if out.Fields["path"] != "/var" {
		t.Errorf("Fields[path] = %q", out.Fields["path"])
	}
}

// TestToAppLogEntryNonStringModule pins the guard: a _module whose value
// is not a string must NOT be hoisted — it falls through to Fields like
// any other key (the type assertion fails, no Module set).
func TestToAppLogEntryNonStringModule(t *testing.T) {
	e := &logrus.Entry{
		Time:    time.Now(),
		Level:   logrus.InfoLevel,
		Message: "x",
		Data:    logrus.Fields{"_module": 42},
	}
	out := toAppLogEntry(e)
	if out.Module != "" {
		t.Errorf("Module = %q, want empty (non-string _module not hoisted)", out.Module)
	}
	if out.Fields["_module"] != "42" {
		t.Errorf("non-string _module should land in Fields, got %q", out.Fields["_module"])
	}
}

// TestCandidateToDiscovered pins the candidate → wire-event field copy.
func TestCandidateToDiscovered(t *testing.T) {
	c := pingTreeCandidate{
		tpID:     "tp-1",
		tpType:   "stcpr",
		remotePK: "remote",
		parentPK: "parent",
		level:    3,
	}
	d := candidateToDiscovered(c)
	if d.TpId != "tp-1" || d.TpType != "stcpr" || d.RemotePk != "remote" ||
		d.ParentPk != "parent" || d.Level != 3 {
		t.Errorf("candidateToDiscovered field mismatch: %+v", d)
	}
}

// TestPksFromCandidates pins the remote-PK set extraction (dedups by
// definition — it's a set).
func TestPksFromCandidates(t *testing.T) {
	cs := []pingTreeCandidate{
		{remotePK: "a"},
		{remotePK: "b"},
		{remotePK: "a"}, // duplicate collapses
	}
	set := pksFromCandidates(cs)
	if len(set) != 2 || !set["a"] || !set["b"] {
		t.Errorf("pksFromCandidates = %v, want {a,b}", set)
	}
	if len(pksFromCandidates(nil)) != 0 {
		t.Error("nil candidates should yield empty set")
	}
}

// TestLevelDoneFor pins the per-level summary event: counts come from
// the supplied stats, and a nil stats yields a bare attempted count.
func TestLevelDoneFor(t *testing.T) {
	cands := []pingTreeCandidate{{remotePK: "a"}, {remotePK: "b"}, {remotePK: "c"}}

	t.Run("with stats", func(t *testing.T) {
		stats := &levelStats{succeeded: 2, failed: 1, skippedCached: 0}
		ev := levelDoneFor(cands, 2, stats)
		if ev.Level != 2 || ev.Attempted != 3 || ev.Succeeded != 2 || ev.Failed != 1 {
			t.Errorf("levelDoneFor = %+v", ev)
		}
	})

	t.Run("nil stats yields zero counts", func(t *testing.T) {
		ev := levelDoneFor(cands, 1, nil)
		if ev.Attempted != 3 || ev.Succeeded != 0 || ev.Failed != 0 || ev.SkippedCached != 0 {
			t.Errorf("nil-stats levelDoneFor = %+v", ev)
		}
	})
}

// TestItoa pins the strconv-free int formatter used in StatusUpdate
// phase strings, including the negative and zero edge cases.
func TestItoa(t *testing.T) {
	cases := map[int]string{
		0:       "0",
		7:       "7",
		42:      "42",
		-1:      "-1",
		-12345:  "-12345",
		1000000: "1000000",
	}
	for in, want := range cases {
		if got := itoa(in); got != want {
			t.Errorf("itoa(%d) = %q, want %q", in, got, want)
		}
	}
}
