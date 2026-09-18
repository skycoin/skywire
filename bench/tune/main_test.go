package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPosteriorUpdate(t *testing.T) {
	a := &arm{knob: "k", value: "v"}
	for _, x := range []float64{1.0, 1.2, 0.8} {
		a.update(x)
	}
	if a.n != 3 {
		t.Fatalf("n = %d, want 3", a.n)
	}
	if math.Abs(a.mean-1.0) > 1e-9 {
		t.Fatalf("mean = %v, want 1.0", a.mean)
	}
	if want := 0.04; math.Abs(a.variance()-want) > 1e-9 {
		t.Fatalf("variance = %v, want %v", a.variance(), want)
	}
	if want := math.Sqrt(0.04 / 3); math.Abs(a.stderr()-want) > 1e-9 {
		t.Fatalf("stderr = %v, want %v", a.stderr(), want)
	}

	// The posterior mean sits BETWEEN the prior and the sample mean, and moves
	// toward the data as observations accumulate; the variance only shrinks.
	m0, v0, s2 := 0.5, 0.16, 0.04
	m1, var1 := posterior(&arm{n: 1, mean: 1.0}, m0, v0, s2)
	m3, var3 := posterior(a, m0, v0, s2)
	if !(m0 < m1 && m1 < m3 && m3 < 1.0) {
		t.Fatalf("posterior means not ordered prior < n=1 < n=3 < data: %v %v %v", m0, m1, m3)
	}
	if !(var3 < var1 && var1 < v0) {
		t.Fatalf("posterior variance did not shrink: v0=%v var1=%v var3=%v", v0, var1, var3)
	}
	// An unobserved arm IS the prior — that is what makes Thompson explore it.
	if m, v := posterior(&arm{}, m0, v0, s2); m != m0 || v != v0 {
		t.Fatalf("unobserved arm = (%v, %v), want the prior (%v, %v)", m, v, m0, v0)
	}
	// The conjugate identity, spelled out: precision adds.
	if want := 1 / (1/v0 + 3/s2); math.Abs(var3-want) > 1e-12 {
		t.Fatalf("posterior variance = %v, want %v", var3, want)
	}
}

func TestIncumbentPrefersMeasuredValuesOnly(t *testing.T) {
	cfg := config{
		knobs: []knob{{name: "a", values: []string{"1", "2"}}, {name: "b", values: []string{"x", "y"}}},
		sigma: 0.2,
	}
	tu := newTuner(cfg, &bytes.Buffer{})
	// knob b is never measured: it must stay OUT of the settings line so the
	// app keeps its compiled default.
	tu.arm("a", "1").update(0.9)
	tu.arm("a", "2").update(1.4)
	inc := tu.incumbent()
	if inc["a"] != "2" {
		t.Fatalf("incumbent a = %q, want 2", inc["a"])
	}
	if v, ok := inc["b"]; ok {
		t.Fatalf("unmeasured knob b is in the incumbent as %q", v)
	}
	s, rs := tu.settingsFor("b", "y")
	if s != "a=2 b=y" {
		t.Fatalf("settings = %q, want \"a=2 b=y\"", s)
	}
	if rs != "" {
		t.Fatalf("route settings = %q, want empty", rs)
	}
}

func TestSettingsForRouteKnobs(t *testing.T) {
	cfg := config{knobs: []knob{
		{name: "upload.concurrency", values: []string{"2", "4"}},
		{name: "--ecf-max-window", values: []string{"4MiB", "8MiB"}, route: true},
	}, sigma: 0.2}
	tu := newTuner(cfg, &bytes.Buffer{})
	tu.arm("upload.concurrency", "4").update(1.1)
	s, rs := tu.settingsFor("--ecf-max-window", "8MiB")
	if s != "upload.concurrency=4" {
		t.Fatalf("settings = %q", s)
	}
	if rs != "--ecf-max-window 8MiB" {
		t.Fatalf("route settings = %q", rs)
	}
}

func TestParseKnobsAndObjective(t *testing.T) {
	ks, err := parseKnobs("upload.chunk_bytes=1MiB,2MiB; upload.concurrency=2,4,8", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(ks) != 2 || ks[0].name != "upload.chunk_bytes" || len(ks[1].values) != 3 {
		t.Fatalf("parsed %+v", ks)
	}
	if _, err := parseKnobs("bogus", false); err == nil {
		t.Fatal("a knob with no values was accepted")
	}
	cs, err := parseObjective("mux-tunnels-2/50up:ratio,mux-tunnels-2/10up")
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 2 || cs[1].metric != "ratio" || cs[1].cell != "10up" {
		t.Fatalf("parsed %+v", cs)
	}
	if _, err := parseObjective("mux-tunnels-2/50up:bogus"); err == nil {
		t.Fatal("an unknown metric was accepted")
	}
}

func TestEvaluateReadsPairedThenMedian(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "s.paired.tsv"),
		"# header\n# row\tcell\tref_MBps\tmux_MBps\tratio\tref_ok\tmux_ok\tref_legs\n"+
			"1\t50up\t4.00\t8.00\t2.000\t1\t1\t1\n"+
			"2\t50up\t4.00\t2.00\t0.500\t1\t1\t1\n"+
			"3\t50up\t4.00\t-\t-\t1\t0\t1\n")
	// one cell has a paired file, the other must fall back to the set medians
	write(t, filepath.Join(dir, "s.tsv"),
		"# rows\ns-r1\tdown\t10000000\t6000000\t0\t0\t0\t1\n"+
			"s-r2\tdown\t10000000\t2000000\t0\t0\t0\t1\n"+
			"s-r3\tdown\t10000000\t99000000\t0\t0\t0\t0\n") // not hash-verified: excluded
	cells, err := parseObjective("s/50up:ratio,s/10down:ratio")
	if err != nil {
		t.Fatal(err)
	}
	got, logs, ok := evaluate(dir, cells)
	if !ok {
		t.Fatalf("no objective from %v", logs)
	}
	// geometric mean of the median ratio (1.25) and the median median (4 MB/s)
	if want := math.Sqrt(1.25 * 4.0); math.Abs(got-want) > 1e-9 {
		t.Fatalf("objective = %v, want %v (%v)", got, want, logs)
	}
	if !strings.Contains(strings.Join(logs, " "), "(median)") {
		t.Fatalf("the fallback source was not recorded: %v", logs)
	}
	// a directory with nothing in it is a MISSING observation, never a zero
	if _, _, ok := evaluate(t.TempDir(), cells); ok {
		t.Fatal("an empty run directory produced an objective")
	}
}

// fakeRunner writes a paired.tsv whose ratios are a known mean for the value of
// the knob under test plus a deterministic, row-dependent wobble, so the tuner
// is driven by a noisy but knowable signal.
const fakeRunner = `#!/bin/sh
# $1 exit pk, $2 out dir, $3 pins, $4 trials, rest ignored; SETTINGS is the grid
out=$2
mean=1.0
case " $SETTINGS " in
*" k=good "*) mean=2.0 ;;
*" k=mid "*)  mean=1.2 ;;
*" k=bad "*)  mean=0.5 ;;
esac
{
  echo "# fake runner, SETTINGS=$SETTINGS"
  printf '# row\tcell\tref_MBps\tmux_MBps\tratio\tref_ok\tmux_ok\tref_legs\n'
  i=1
  while [ $i -le 4 ]; do
    r=$(awk -v m="$mean" -v i="$i" 'BEGIN{printf "%.3f", m * (1 + 0.1 * ((i % 3) - 1))}')
    printf '%d\t50up\t4.00\t4.00\t%s\t1\t1\t1\n' "$i" "$r"
    i=$((i + 1))
  done
} > "$out/s.paired.tsv"
echo "fake: wrote $out/s.paired.tsv"
`

func TestRunWithFakeRunnerFindsTheBestValue(t *testing.T) {
	bench := t.TempDir()
	write(t, filepath.Join(bench, "fake.sh"), fakeRunner)
	if err := os.Chmod(filepath.Join(bench, "fake.sh"), 0o750); err != nil { //nolint:gosec // a runner has to be executable
		t.Fatal(err)
	}
	out := t.TempDir()
	write(t, filepath.Join(out, "paired-ref.txt"), "0371ab4b\n")
	cells, err := parseObjective("s/50up:ratio")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		exitPK: "02aa", out: out, pins: filepath.Join(out, "pins"), benchDir: bench,
		runner: "fake.sh", trials: 1, rounds: 9, seed: 7, sigma: 0.2,
		knobs: []knob{{name: "k", values: []string{"bad", "mid", "good"}}},
		cells: cells,
	}
	var log bytes.Buffer
	tu := newTuner(cfg, &log)
	tu.run()

	if got := tu.incumbent()["k"]; got != "good" {
		t.Fatalf("incumbent k = %q, want good\n%s", got, log.String())
	}
	// the bandit must SPEND on the winner, not sweep evenly
	good, bad := tu.arm("k", "good").n, tu.arm("k", "bad").n
	if good <= bad {
		t.Fatalf("good was run %d times and bad %d: the sampling is not concentrating\n%s", good, bad, log.String())
	}
	if n := tu.arm("k", "bad").n; n == 0 {
		t.Fatal("no value was ever explored more than once")
	}
	if math.Abs(tu.arm("k", "good").mean-2.0) > 0.05 {
		t.Fatalf("good arm mean = %v, want ~2.0", tu.arm("k", "good").mean)
	}

	// artifacts: one directory per round, the reference copied in, a full tsv
	for r := 1; r <= cfg.rounds; r++ {
		hits, err := filepath.Glob(filepath.Join(out, "tune", fmt.Sprintf("r%d-k=*", r)))
		if err != nil {
			t.Fatal(err)
		}
		if len(hits) != 1 {
			t.Fatalf("round %d produced %d directories", r, len(hits))
		}
		if _, err := os.Stat(filepath.Join(hits[0], "paired-ref.txt")); err != nil {
			t.Fatalf("round %d has no paired reference copied in: %v", r, err)
		}
	}
	tsv, err := os.ReadFile(filepath.Join(out, "tune", "tune.tsv")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, l := range strings.Split(strings.TrimRight(string(tsv), "\n"), "\n") {
		if !strings.HasPrefix(l, "#") {
			rows++
			if n := len(strings.Split(l, "\t")); n != 7 {
				t.Fatalf("tune.tsv row has %d columns: %q", n, l)
			}
		}
	}
	if rows != cfg.rounds {
		t.Fatalf("tune.tsv has %d rows, want %d", rows, cfg.rounds)
	}
	inc, err := os.ReadFile(filepath.Join(out, "tune", "incumbent.txt")) //nolint:gosec // the test's own temp dir
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(inc)) != `SETTINGS="k=good"` {
		t.Fatalf("incumbent.txt = %q", string(inc))
	}

	// determinism: the same seed against the same (deterministic) runner
	// replays the same rounds.
	out2 := t.TempDir()
	cfg2 := cfg
	cfg2.out = out2
	cfg2.pins = filepath.Join(out2, "pins")
	tu2 := newTuner(cfg2, &bytes.Buffer{})
	tu2.run()
	for i := range tu.rec {
		if tu.rec[i].value != tu2.rec[i].value {
			t.Fatalf("round %d differed between seeds-equal runs: %q vs %q", i+1, tu.rec[i].value, tu2.rec[i].value)
		}
	}
}

// A runner that writes nothing must leave every arm untouched: a failed run is
// a missing observation, not a zero that would poison the value forever.
func TestFailedRunIsAMissingObservation(t *testing.T) {
	bench := t.TempDir()
	write(t, filepath.Join(bench, "fail.sh"), "#!/bin/sh\necho 'fake: the rig is down'\nexit 1\n")
	if err := os.Chmod(filepath.Join(bench, "fail.sh"), 0o750); err != nil { //nolint:gosec // a runner has to be executable
		t.Fatal(err)
	}
	out := t.TempDir()
	cells, err := parseObjective("s/50up:ratio")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config{
		exitPK: "02aa", out: out, pins: "pins", benchDir: bench, runner: "fail.sh",
		trials: 1, rounds: 3, seed: 3, sigma: 0.2,
		knobs: []knob{{name: "k", values: []string{"a", "b"}}}, cells: cells,
	}
	var log bytes.Buffer
	tu := newTuner(cfg, &log)
	tu.run()
	for _, v := range []string{"a", "b"} {
		if a := tu.arm("k", v); a.n != 0 || a.mean != 0 {
			t.Fatalf("arm %q was updated by a failed run: n=%d mean=%v", v, a.n, a.mean)
		}
	}
	if len(tu.incumbent()) != 0 {
		t.Fatalf("a failed run produced an incumbent: %v", tu.incumbent())
	}
	if !strings.Contains(log.String(), "missing observation") {
		t.Fatalf("the miss was not reported:\n%s", log.String())
	}
}

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
