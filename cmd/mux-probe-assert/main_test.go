// Package main main_test.go — table-driven tests for the tally
// parser + assertion engine. The contract this pins:
//
//   - Tally parsing tolerates leading log lines from the bash runner.
//   - Numeric fields tolerate the runner's unit suffixes.
//   - Each scope-agreement assertion has both a pass and fail path.
//
// Pre-conditions are spelled out in each subtest to avoid the
// shared-fixture coupling that bit us on prior CLI parse tests.
package main

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

const sampleTally = `[16:42:07Z] probe start: target=03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c routes=2 duration=60s rate=5/s rpc=localhost:3435
[16:42:07Z] pre-flight: self_pk=02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36
[16:42:10Z] starting throughput leg (dmsg cat --routes=2)…
[16:42:13Z] rg delta: pre=0 post=2 new=2 (requested=2)
[16:42:13Z] fanout verified: 2 legs established for requested 2
[16:42:13Z] starting skychat low-rate stream (5 msg/s for 60s)…
=== mux-route-probe tally ===
target_pk:      03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c
self_pk:        02f9aa588dffa20b205e1c10bd0236130f080af157044d0eaa35753d2f2fcd6c36
routes_req:     2
routes_act:     2
duration:       60s
throughput:     312 KB/s (19169280 bytes)
skychat_sent:   300
skychat_acked:  298
rtt_p50:        45 ms
rtt_p99:        180 ms
`

func TestParseTally_Success(t *testing.T) {
	tally, err := parseTally(strings.NewReader(sampleTally))
	if err != nil {
		t.Fatalf("parseTally: %v", err)
	}
	if tally.TargetPK != "03d1d78e7323e1dc63a6cbbf79e52974791e3cd7b5aaab77f045d72a21b066ee8c" {
		t.Errorf("TargetPK: %q", tally.TargetPK)
	}
	if tally.RoutesReq != 2 {
		t.Errorf("RoutesReq: %d", tally.RoutesReq)
	}
	if tally.RoutesAct != 2 {
		t.Errorf("RoutesAct: %d", tally.RoutesAct)
	}
	if tally.DurationSec != 60 {
		t.Errorf("DurationSec: %d", tally.DurationSec)
	}
	if tally.ThroughputKBPS != 312 {
		t.Errorf("ThroughputKBPS: %d", tally.ThroughputKBPS)
	}
	if tally.ThroughputB != 19169280 {
		t.Errorf("ThroughputB: %d", tally.ThroughputB)
	}
	if tally.SkychatSent != 300 || tally.SkychatAcked != 298 {
		t.Errorf("skychat sent=%d acked=%d", tally.SkychatSent, tally.SkychatAcked)
	}
	if tally.RTTp50ms != 45 || tally.RTTp99ms != 180 {
		t.Errorf("rtt p50=%d p99=%d", tally.RTTp50ms, tally.RTTp99ms)
	}
}

func TestParseTally_NoTallyMarker(t *testing.T) {
	// Just log lines, no tally block — should error so a CI lane
	// doesn't silently zero out and report success on a runner that
	// crashed before emitting its tally.
	in := "[16:42:07Z] probe start: target=…\n[16:42:08Z] ABORT: pre-flight failed\n"
	if _, err := parseTally(strings.NewReader(in)); err == nil {
		t.Fatal("expected error on tally-missing input")
	}
}

func TestParseTally_NoAcksTolerated(t *testing.T) {
	// Bash runner emits "(no acks)" sentinel when the skychat
	// stream got zero ack-backs. Parser must not error on the
	// string — assignField returns nil and the int stays zero.
	in := `=== mux-route-probe tally ===
target_pk:      a
self_pk:        b
routes_req:     1
routes_act:     1
duration:       10s
throughput:     0 KB/s (0 bytes)
skychat_sent:   0
skychat_acked:  0
rtt_p50:        (no acks)
rtt_p99:        (no acks)
`
	tally, err := parseTally(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseTally: %v", err)
	}
	if tally.RTTp50ms != 0 || tally.RTTp99ms != 0 {
		t.Errorf("expected zero rtt on no-acks, got p50=%d p99=%d", tally.RTTp50ms, tally.RTTp99ms)
	}
}

func TestAssertTally_TopologyDegrade(t *testing.T) {
	tally := Tally{RoutesReq: 2, RoutesAct: 1, ThroughputKBPS: 1000}
	baseline := Baseline{ThroughputKBPS: 1000, ThroughputThresholdPct: 70}
	var buf bytes.Buffer
	if got := assertTally(tally, baseline, &buf); got != exitTopologyDegrade {
		t.Fatalf("expected exitTopologyDegrade (%d), got %d. log=%s", exitTopologyDegrade, got, buf.String())
	}
	if !strings.Contains(buf.String(), "topology degrade") {
		t.Errorf("missing 'topology degrade' in log: %s", buf.String())
	}
}

func TestAssertTally_ThroughputRegression(t *testing.T) {
	// 70% of 1000 KB/s = 700 KB/s floor. 600 < 700 → fail.
	tally := Tally{RoutesReq: 2, RoutesAct: 2, ThroughputKBPS: 600}
	baseline := Baseline{ThroughputKBPS: 1000, ThroughputThresholdPct: 70}
	var buf bytes.Buffer
	if got := assertTally(tally, baseline, &buf); got != exitThroughputRegress {
		t.Fatalf("expected exitThroughputRegress (%d), got %d. log=%s", exitThroughputRegress, got, buf.String())
	}
}

func TestAssertTally_RTTp99CeilingBreach(t *testing.T) {
	tally := Tally{RoutesReq: 1, RoutesAct: 1, ThroughputKBPS: 1000, RTTp99ms: 5000}
	baseline := Baseline{
		ThroughputKBPS:         1000,
		ThroughputThresholdPct: 70,
		RTTp99CeilingMs:        1000,
	}
	var buf bytes.Buffer
	got := assertTally(tally, baseline, &buf)
	if got != exitThroughputRegress {
		t.Fatalf("expected exitThroughputRegress (%d) on p99 breach, got %d. log=%s", exitThroughputRegress, got, buf.String())
	}
	if !strings.Contains(buf.String(), "p99 RTT") {
		t.Errorf("missing 'p99 RTT' in log: %s", buf.String())
	}
}

func TestAssertTally_SkychatAckRateBreach(t *testing.T) {
	// 50/100 = 50% acked, min is 90% → fail.
	tally := Tally{
		RoutesReq:      1,
		RoutesAct:      1,
		ThroughputKBPS: 1000,
		SkychatSent:    100,
		SkychatAcked:   50,
	}
	baseline := Baseline{
		ThroughputKBPS:         1000,
		ThroughputThresholdPct: 70,
		SkychatAckRateMinPct:   90,
	}
	var buf bytes.Buffer
	got := assertTally(tally, baseline, &buf)
	if got != exitThroughputRegress {
		t.Fatalf("expected exitThroughputRegress (%d) on ack-rate breach, got %d. log=%s", exitThroughputRegress, got, buf.String())
	}
}

func TestAssertTally_AllPass(t *testing.T) {
	tally := Tally{
		RoutesReq:      2,
		RoutesAct:      2,
		ThroughputKBPS: 850,
		RTTp99ms:       180,
		SkychatSent:    100,
		SkychatAcked:   95,
	}
	baseline := Baseline{
		ThroughputKBPS:         1000,
		ThroughputThresholdPct: 70,
		RTTp99CeilingMs:        500,
		SkychatAckRateMinPct:   90,
	}
	var buf bytes.Buffer
	if got := assertTally(tally, baseline, &buf); got != exitOK {
		t.Fatalf("expected exitOK, got %d. log=%s", got, buf.String())
	}
	// All four PASS lines should be present.
	for _, want := range []string{"PASS: fanout took", "PASS: throughput", "PASS: p99 RTT", "PASS: skychat ack rate"} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("missing %q in log: %s", want, buf.String())
		}
	}
}

func TestAssertTally_DisabledChecksSkip(t *testing.T) {
	// Zero-valued baseline thresholds → SKIP rather than FAIL.
	// Useful when only one assertion is being measured (e.g. early
	// runs without a recorded throughput baseline yet).
	tally := Tally{RoutesReq: 1, RoutesAct: 1, ThroughputKBPS: 100}
	baseline := Baseline{} // all-zero
	var buf bytes.Buffer
	if got := assertTally(tally, baseline, &buf); got != exitOK {
		t.Fatalf("expected exitOK with all-zero baseline, got %d. log=%s", got, buf.String())
	}
	if !strings.Contains(buf.String(), "SKIP: throughput check disabled") {
		t.Errorf("missing throughput SKIP: %s", buf.String())
	}
}

// Sanity: parseTally accepts io.Reader, not just strings.Reader.
var _ io.Reader = (*bytes.Reader)(nil)
