// Command mux-probe-assert consumes the tally emitted by
// ci_scripts/mux-route-probe.sh and enforces pass/fail thresholds
// against a recorded baseline. Designed to be piped:
//
//	mux-route-probe.sh <target_pk> | mux-probe-assert -baseline=ci_scripts/mux-baseline.json
//
// Or with the tally captured to a file:
//
//	mux-route-probe.sh <target_pk> > tally.txt
//	mux-probe-assert -baseline=... < tally.txt
//
// Exit codes intentionally match the runner's (per the 2026-05-19
// mux-test scope agreement) so a CI lane can use either tool's exit
// code without ambiguity:
//
//	0  success — all checks pass
//	1  usage / parse error
//	2  topology-degrade abort (routes_act < routes_req)
//	3  integrity failure (reserved; runner doesn't emit a checksum yet)
//	4  throughput regression (< 70% of baseline)
//	5  head-of-line correlation breach (reserved; needs per-msg data
//	   the bash runner doesn't emit yet)
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// Tally holds the parsed fields from mux-route-probe.sh's stdout
// tally block. Field order matches the runner's emit order so the
// json shape is stable across versions of the tool.
type Tally struct {
	TargetPK       string `json:"target_pk"`
	SelfPK         string `json:"self_pk"`
	RoutesReq      int    `json:"routes_req"`
	RoutesAct      int    `json:"routes_act"`
	DurationSec    int    `json:"duration_sec"`
	ThroughputKBPS int    `json:"throughput_kbps"`
	ThroughputB    int64  `json:"throughput_bytes"`
	SkychatSent    int    `json:"skychat_sent"`
	SkychatAcked   int    `json:"skychat_acked"`
	RTTp50ms       int    `json:"rtt_p50_ms"`
	RTTp99ms       int    `json:"rtt_p99_ms"`
}

// Baseline holds the recorded reference numbers for the assertions.
// Throughput is the only baseline-relative threshold today; RTT is
// an absolute sanity bound the operator picks. Routes is enforced
// against the tally's own RoutesReq, not the baseline.
type Baseline struct {
	// ThroughputKBPS is the single-leg / single-route reference for
	// the throughput ratio assertion. The mux run is expected to
	// hit ≥ ThroughputThresholdPct of this number.
	ThroughputKBPS int `json:"throughput_kbps"`

	// ThroughputThresholdPct is the floor (out of 100) the mux run
	// must clear vs ThroughputKBPS. Defaults to 70 per the
	// 2026-05-19 scope agreement.
	ThroughputThresholdPct int `json:"throughput_threshold_pct"`

	// RTTp99CeilingMs is the absolute upper bound on the p99 ack
	// latency the operator considers acceptable. Zero disables the
	// check (useful when only throughput is being measured).
	RTTp99CeilingMs int `json:"rtt_p99_ceiling_ms"`

	// SkychatAckRateMinPct is the minimum percentage (out of 100)
	// of skychat sends that must ack within the runner's wait
	// window. Zero disables.
	SkychatAckRateMinPct int `json:"skychat_ack_rate_min_pct"`
}

const (
	exitOK                = 0
	exitUsage             = 1
	exitTopologyDegrade   = 2
	exitIntegrity         = 3 //nolint:unused,deadcode // reserved
	exitThroughputRegress = 4
	exitHOLBreach         = 5 //nolint:unused,deadcode // reserved
)

func main() {
	var baselinePath string
	flag.StringVar(&baselinePath, "baseline", "", "path to baseline JSON (required)")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: %s -baseline=<path> < tally.txt\n\n", os.Args[0])
		flag.PrintDefaults()
	}
	flag.Parse()

	if baselinePath == "" {
		flag.Usage()
		os.Exit(exitUsage)
	}

	baseline, err := loadBaseline(baselinePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "load baseline %q: %v\n", baselinePath, err)
		os.Exit(exitUsage)
	}

	tally, err := parseTally(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse tally: %v\n", err)
		os.Exit(exitUsage)
	}

	code := assertTally(tally, baseline, os.Stderr)
	os.Exit(code)
}

func loadBaseline(path string) (Baseline, error) {
	var b Baseline
	f, err := os.Open(path) //nolint:gosec // operator-supplied path
	if err != nil {
		return b, err
	}
	defer func() { _ = f.Close() }() //nolint:errcheck
	if err := json.NewDecoder(f).Decode(&b); err != nil {
		return b, fmt.Errorf("decode: %w", err)
	}
	if b.ThroughputThresholdPct == 0 {
		b.ThroughputThresholdPct = 70 // scope-agreement default
	}
	return b, nil
}

// parseTally reads the runner's stdout block. Tolerant of leading
// log lines (the bash runner emits "[HH:MM:SSZ] …" log lines before
// the tally) — only lines after "=== mux-route-probe tally ===" are
// consumed.
func parseTally(r io.Reader) (Tally, error) {
	var t Tally
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	inTally := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "=== mux-route-probe tally ===") {
			inTally = true
			continue
		}
		if !inTally || line == "" {
			continue
		}
		key, val, ok := splitKV(line)
		if !ok {
			continue
		}
		if err := assignField(&t, key, val); err != nil {
			return t, fmt.Errorf("field %q: %w", key, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return t, err
	}
	if !inTally {
		return t, errors.New("no tally block found (expected '=== mux-route-probe tally ===' marker)")
	}
	if t.RoutesReq == 0 {
		return t, errors.New("tally missing required field routes_req")
	}
	return t, nil
}

// splitKV pulls "key: value" out of one tally line. Returns ok=false
// for lines that don't match (e.g. blank, marker, stray log noise
// mixed in).
func splitKV(line string) (key, val string, ok bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	return strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:]), true
}

// assignField populates t from one key/value pair. Unknown keys are
// silently ignored so future runner additions don't break older
// harness builds. Numeric fields tolerate the runner's trailing unit
// suffix ("60s", "1234 KB/s (5678 bytes)", "12 ms").
func assignField(t *Tally, key, val string) error {
	switch key {
	case "target_pk":
		t.TargetPK = val
	case "self_pk":
		t.SelfPK = val
	case "routes_req":
		return scanInt(&t.RoutesReq, val)
	case "routes_act":
		return scanInt(&t.RoutesAct, val)
	case "duration":
		return scanInt(&t.DurationSec, strings.TrimSuffix(val, "s"))
	case "throughput":
		// "1234 KB/s (5678 bytes)"
		fields := strings.Fields(val)
		if len(fields) >= 1 {
			if err := scanInt(&t.ThroughputKBPS, fields[0]); err != nil {
				return err
			}
		}
		// Pull "(5678 bytes)" if present — purely informational.
		if i := strings.Index(val, "("); i >= 0 {
			rest := strings.TrimSuffix(strings.TrimPrefix(val[i+1:], ""), ")")
			restFields := strings.Fields(rest)
			if len(restFields) >= 1 {
				_ = scanInt64(&t.ThroughputB, restFields[0]) //nolint:errcheck
			}
		}
	case "skychat_sent":
		return scanInt(&t.SkychatSent, val)
	case "skychat_acked":
		return scanInt(&t.SkychatAcked, val)
	case "rtt_p50":
		return scanInt(&t.RTTp50ms, strings.TrimSuffix(val, " ms"))
	case "rtt_p99":
		return scanInt(&t.RTTp99ms, strings.TrimSuffix(val, " ms"))
	}
	return nil
}

func scanInt(out *int, s string) error {
	s = strings.TrimSpace(s)
	if s == "" || s == "(no acks)" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*out = n
	return nil
}

func scanInt64(out *int64, s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*out = n
	return nil
}

// emitf/emitln wrap fmt.Fprintf/Fprintln on an io.Writer and discard
// the return so callers don't have to litter _, _ = on every trace
// line. The Writer here is stderr (or a test buffer); a write error
// is not actionable from the harness's perspective.
func emitf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...) //nolint:errcheck
}

func emitln(w io.Writer, s string) {
	_, _ = fmt.Fprintln(w, s) //nolint:errcheck
}

// assertTally applies the scope-agreement thresholds and returns the
// exit code. Writes a one-line summary per assertion to w so a CI
// lane log shows the full pass/fail trace, not just the exit code.
func assertTally(t Tally, b Baseline, w io.Writer) int {
	emitf(w, "mux-probe-assert: target=%s routes_req=%d routes_act=%d throughput=%dKB/s p50=%dms p99=%dms\n",
		t.TargetPK, t.RoutesReq, t.RoutesAct, t.ThroughputKBPS, t.RTTp50ms, t.RTTp99ms)

	// (a) routes_act ≥ routes_req — the methodology gap.
	if t.RoutesAct < t.RoutesReq {
		emitf(w, "FAIL: topology degrade — routes_act %d < routes_req %d\n", t.RoutesAct, t.RoutesReq)
		return exitTopologyDegrade
	}
	emitf(w, "PASS: fanout took (routes_act %d ≥ routes_req %d)\n", t.RoutesAct, t.RoutesReq)

	// (c) throughput ≥ threshold% of baseline.
	if b.ThroughputKBPS > 0 {
		floor := b.ThroughputKBPS * b.ThroughputThresholdPct / 100
		if t.ThroughputKBPS < floor {
			emitf(w, "FAIL: throughput regression — %d KB/s < %d KB/s (%d%% of baseline %d KB/s)\n",
				t.ThroughputKBPS, floor, b.ThroughputThresholdPct, b.ThroughputKBPS)
			return exitThroughputRegress
		}
		emitf(w, "PASS: throughput %d KB/s ≥ floor %d KB/s (baseline %d KB/s @ %d%%)\n",
			t.ThroughputKBPS, floor, b.ThroughputKBPS, b.ThroughputThresholdPct)
	} else {
		emitln(w, "SKIP: throughput check disabled (baseline.throughput_kbps == 0)")
	}

	// (d) p99 ≤ ceiling. Operator-picked absolute bound, not
	// baseline-relative — a mux run shouldn't bloat the tail.
	if b.RTTp99CeilingMs > 0 {
		if t.RTTp99ms > b.RTTp99CeilingMs {
			emitf(w, "FAIL: p99 RTT %d ms > ceiling %d ms\n", t.RTTp99ms, b.RTTp99CeilingMs)
			return exitThroughputRegress // grouped with throughput-class regressions
		}
		emitf(w, "PASS: p99 RTT %d ms ≤ ceiling %d ms\n", t.RTTp99ms, b.RTTp99CeilingMs)
	} else {
		emitln(w, "SKIP: p99 ceiling check disabled (baseline.rtt_p99_ceiling_ms == 0)")
	}

	// Skychat ack-rate floor. Independent of routes/throughput —
	// catches the case where the route exists but the chat-app
	// is dropping messages on the receive side.
	if b.SkychatAckRateMinPct > 0 && t.SkychatSent > 0 {
		ackPct := t.SkychatAcked * 100 / t.SkychatSent
		if ackPct < b.SkychatAckRateMinPct {
			emitf(w, "FAIL: skychat ack rate %d%% < min %d%% (%d/%d)\n",
				ackPct, b.SkychatAckRateMinPct, t.SkychatAcked, t.SkychatSent)
			return exitThroughputRegress
		}
		emitf(w, "PASS: skychat ack rate %d%% ≥ min %d%% (%d/%d)\n",
			ackPct, b.SkychatAckRateMinPct, t.SkychatAcked, t.SkychatSent)
	}

	// (e) head-of-line correlation: reserved. The bash runner only
	// emits aggregate p50/p99, not per-message timestamps, so the
	// correlation between skychat-RTT and skysocks-throughput can't
	// be computed here. Pairs with a future runner enhancement that
	// emits a CSV of (send_ts, ack_ts, concurrent_throughput).
	emitln(w, "SKIP: head-of-line correlation check (runner doesn't emit per-message data yet)")

	return exitOK
}
