// Package main implements `tune` — a bandit tuner over the live bench knobs.
//
//	go run ./bench/tune -exit <pk> -out bench/<date>/<commit> -pins $S/pins \
//	    -runner run-mux.sh -args '2 http://127.0.0.1:18080 "<order>"' \
//	    -objective mux-tunnels-2/50up:ratio,mux-tunnels-2/10up:ratio \
//	    -knobs 'upload.chunk_bytes=1MiB,2MiB,4MiB;upload.concurrency=2,4,8' \
//	    -rounds 12 -trials 2 -env 'TUNNELS=2 LEGS='
//
// WHY A BANDIT AND NOT A SWEEP. bench/run-sweep.sh spends one complete runner
// invocation on every value of one knob, and every value pays the same price
// whether it looked hopeless after its first run or not. A rig minute is the
// scarce thing here — a 2-trial 10/50 MB down+up set is about four minutes —
// and the measurement is NOISY: the paired ratios of the 2026-09-16/17 campaign
// swing +-30 % between trials of the same shape, and the unpaired bar swings 2x
// inside an hour. A full sweep of three knobs at three values each is 9 runs
// that say almost nothing about the INTERACTION between them, and a Bayesian
// optimizer over a continuous space would spend its budget learning a surface
// nobody asked for.
//
// So: one arm per (knob, value) — a discrete grid, the same grid a sweep would
// have used — and Thompson sampling with a Gaussian posterior per arm. Every
// round spends exactly ONE runner invocation, on the value its own posterior
// says is most likely to be the best, so a value that lost twice stops being
// paid for while a value that is merely uncertain keeps being probed. The knob
// under test rotates round-robin, and the OTHER knobs are held at the
// incumbent — each knob's posterior-mean-best value so far — so a round is a
// real measurement of one knob against the current best-known configuration
// rather than against the compiled defaults.
//
// Nothing here decides anything permanent: it writes tune.tsv (every round, its
// settings and its objective) and incumbent.txt (a SETTINGS line to paste into
// a campaign). The verdict on the incumbent is still a full run of the campaign
// under bench/verdict.sh — this only says where to point it.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- the grid --

// knob is one tunable and the discrete values the tuner may put it at. A route
// knob is spelled as a whole `route settings` flag and goes to ROUTE_SETTINGS;
// an app knob is a `proxy settings` key=value and goes to SETTINGS.
type knob struct {
	name   string
	values []string
	route  bool
}

// parseKnobs reads "a=1,2;b=3,4" into knobs. Values keep their spelling exactly
// ("4MiB", "250ms"): the CLI parses them, not this.
func parseKnobs(spec string, route bool) ([]knob, error) {
	var out []knob
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, vals, ok := strings.Cut(part, "=")
		if !ok {
			return nil, fmt.Errorf("knob %q has no =<values>", part)
		}
		k := knob{name: strings.TrimSpace(name), route: route}
		for _, v := range strings.Split(vals, ",") {
			if v = strings.TrimSpace(v); v != "" {
				k.values = append(k.values, v)
			}
		}
		if k.name == "" || len(k.values) == 0 {
			return nil, fmt.Errorf("knob %q has no name or no values", part)
		}
		out = append(out, k)
	}
	return out, nil
}

// ------------------------------------------------------------ the posterior --

// arm is one (knob, value) pair and the running moments of the objective
// measured at it. Welford, so a long run cannot lose precision to catastrophic
// cancellation in a sum of squares.
type arm struct {
	knob  string
	value string
	n     int
	mean  float64
	m2    float64
}

func (a *arm) update(x float64) {
	a.n++
	d := x - a.mean
	a.mean += d / float64(a.n)
	a.m2 += d * (x - a.mean)
}

// variance is the sample variance of this arm's own observations, or 0 when it
// has fewer than two.
func (a *arm) variance() float64 {
	if a.n < 2 {
		return 0
	}
	return a.m2 / float64(a.n-1)
}

// stderr of the arm's mean, or 0 when it has fewer than two observations.
func (a *arm) stderr() float64 {
	if a.n < 2 {
		return 0
	}
	return math.Sqrt(a.variance() / float64(a.n))
}

// posterior is the Normal-mean conjugate update: a Gaussian prior N(m0, v0) on
// the arm's true mean, n observations of known noise variance sigma2, and the
// posterior is Gaussian again with precision tau0 + n/sigma2. An unobserved arm
// is simply the prior, which is what makes Thompson sampling explore it.
func posterior(a *arm, m0, v0, sigma2 float64) (mean, variance float64) {
	if v0 <= 0 {
		v0 = 1
	}
	if sigma2 <= 0 {
		sigma2 = 1
	}
	if a == nil || a.n == 0 {
		return m0, v0
	}
	tau0, tauN := 1/v0, float64(a.n)/sigma2
	variance = 1 / (tau0 + tauN)
	mean = variance * (tau0*m0 + tauN*a.mean)
	return mean, variance
}

// ------------------------------------------------------------- the objective --

// cellRef names one summarized cell of one set: "mux-tunnels-2/50up:ratio".
type cellRef struct {
	set    string
	cell   string
	metric string // "ratio" (paired) or "median" (MB/s)
}

func parseObjective(spec string) ([]cellRef, error) {
	var out []cellRef
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		body, metric, ok := strings.Cut(part, ":")
		if !ok {
			metric = "ratio"
		}
		set, cell, ok := strings.Cut(body, "/")
		if !ok || set == "" || cell == "" {
			return nil, fmt.Errorf("objective %q is not <set>/<cell>[:metric]", part)
		}
		switch metric {
		case "ratio", "median":
		default:
			return nil, fmt.Errorf("objective %q: metric must be ratio or median", part)
		}
		out = append(out, cellRef{set: set, cell: cell, metric: metric})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty objective")
	}
	return out, nil
}

func (c cellRef) String() string { return c.set + "/" + c.cell + ":" + c.metric }

var cellRE = regexp.MustCompile(`^([0-9]+)(down|up)$`)

func median(v []float64) (float64, bool) {
	if len(v) == 0 {
		return 0, false
	}
	s := append([]float64(nil), v...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2], true
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2, true
}

// pairedRatio is the MEDIAN paired ratio of one cell of <dir>/<set>.paired.tsv:
// columns row, cell, ref_MBps, mux_MBps, ratio, ref_ok, mux_ok, ref_legs. A row
// whose ratio is "-" never hash-verified on one side and is not a number.
func pairedRatio(dir, set, cell string) (float64, bool) {
	rows, err := readTSV(filepath.Join(dir, set+".paired.tsv"))
	if err != nil {
		return 0, false
	}
	var v []float64
	for _, f := range rows {
		if len(f) < 5 || f[1] != cell {
			continue
		}
		if x, err := strconv.ParseFloat(f[4], 64); err == nil {
			v = append(v, x)
		}
	}
	return median(v)
}

// setMedian is bench/summarize.sh's own formula on <dir>/<set>.tsv: the median
// of $4/1e6 over hash-verified rows ($8 == 1) of that size and direction.
func setMedian(dir, set, cell string) (float64, bool) {
	m := cellRE.FindStringSubmatch(cell)
	if m == nil {
		return 0, false
	}
	mb, err := strconv.Atoi(m[1])
	if err != nil {
		return 0, false
	}
	want, dirn := strconv.Itoa(mb*1000000), m[2]
	rows, err := readTSV(filepath.Join(dir, set+".tsv"))
	if err != nil {
		return 0, false
	}
	var v []float64
	for _, f := range rows {
		if len(f) < 8 || f[1] != dirn || f[2] != want || f[7] != "1" {
			continue
		}
		if x, err := strconv.ParseFloat(f[3], 64); err == nil {
			v = append(v, x/1e6)
		}
	}
	return median(v)
}

// readTSV returns the non-comment rows of a TSV, split on tabs.
func readTSV(path string) ([][]string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // a bench artifact this tool just wrote
	if err != nil {
		return nil, err
	}
	var out [][]string
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, strings.Split(line, "\t"))
	}
	return out, nil
}

// evaluate reads the named cells out of a finished run directory and combines
// them by GEOMETRIC mean — the cells are ratios (and the fallback is MB/s), so
// a proportional loss in one cell has to cost as much as a proportional gain in
// another, which an arithmetic mean of a 10 MB cell and a 50 MB cell does not
// give. A cell that never appeared (an INVALID set, a shape the run did not
// reach) is left out rather than counted as zero; a run that produced NO cell
// at all is a MISSING observation and updates nothing.
func evaluate(dir string, cells []cellRef) (float64, []string, bool) {
	var logs []string
	sum, n := 0.0, 0
	for _, c := range cells {
		var (
			v   float64
			ok  bool
			src string
		)
		if c.metric == "ratio" {
			if v, ok = pairedRatio(dir, c.set, c.cell); ok {
				src = "ratio"
			}
		}
		if !ok {
			// no paired file (PAIRED=0, or the reference failed its probes):
			// fall back to the cell's own median goodput.
			if v, ok = setMedian(dir, c.set, c.cell); ok {
				src = "median"
			}
		}
		if !ok || v <= 0 {
			logs = append(logs, c.set+"/"+c.cell+"=-")
			continue
		}
		logs = append(logs, fmt.Sprintf("%s/%s=%.3f(%s)", c.set, c.cell, v, src))
		sum += math.Log(v)
		n++
	}
	if n == 0 {
		return 0, logs, false
	}
	return math.Exp(sum / float64(n)), logs, true
}

// ---------------------------------------------------------------- the tuner --

type config struct {
	exitPK   string
	out      string
	pins     string
	benchDir string
	runner   string
	args     string
	env      string
	trials   int
	rounds   int
	seed     int64
	sigma    float64 // assumed observation noise when the arms cannot yet say
	knobs    []knob
	cells    []cellRef
}

type tuner struct {
	cfg  config
	arms map[string]*arm // "knob=value"
	rng  *rand.Rand
	out  io.Writer
	rec  []record
}

type record struct {
	round         int
	knob, value   string
	settings      string
	routeSettings string
	objective     float64
	ok            bool
	cells         []string
}

func newTuner(cfg config, out io.Writer) *tuner {
	t := &tuner{cfg: cfg, arms: map[string]*arm{}, rng: rand.New(rand.NewSource(cfg.seed)), out: out} //nolint:gosec // reproducibility, not secrecy
	for _, k := range cfg.knobs {
		for _, v := range k.values {
			t.arms[k.name+"="+v] = &arm{knob: k.name, value: v}
		}
	}
	return t
}

func (t *tuner) arm(knob, value string) *arm { return t.arms[knob+"="+value] }

// logf is the run log. A write to it failing is not a reason to abandon a rig
// run, so the error is dropped deliberately and in one place.
func (t *tuner) logf(format string, a ...any) { fmt.Fprintf(t.out, format, a...) } //nolint:errcheck,gosec

// sigma2 is the pooled within-arm variance — every arm's observations are of
// the same rig on the same day, so they estimate one noise level between them.
// Floored, because two runs that happened to land on the same number must not
// collapse the posterior and with it the exploration.
func (t *tuner) sigma2() float64 {
	ss, df := 0.0, 0
	for _, a := range t.arms {
		if a.n >= 2 {
			ss += a.m2
			df += a.n - 1
		}
	}
	floor := (t.cfg.sigma / 4) * (t.cfg.sigma / 4)
	if df == 0 {
		return t.cfg.sigma * t.cfg.sigma
	}
	return math.Max(ss/float64(df), floor)
}

// prior is empirical: the grand mean of everything measured so far, with a
// variance four noise-variances wide, so an arm nobody has run yet is sampled
// around the going rate with twice the spread of a single measurement and is
// therefore tried early. Before the very first observation every arm is the
// same prior and the draw decides — which is the right amount of knowledge.
func (t *tuner) prior() (mean, variance float64) {
	sum, n := 0.0, 0
	for _, a := range t.arms {
		sum += a.mean * float64(a.n)
		n += a.n
	}
	s2 := t.sigma2()
	if n == 0 {
		return 0, 4 * s2
	}
	return sum / float64(n), 4 * s2
}

// pick draws one sample from every value's posterior and returns the argmax —
// Thompson sampling: a value is run with exactly the probability that it is the
// best one given what has been measured.
func (t *tuner) pick(k knob) string {
	m0, v0 := t.prior()
	s2 := t.sigma2()
	best, bestDraw := k.values[0], math.Inf(-1)
	for _, v := range k.values {
		m, va := posterior(t.arm(k.name, v), m0, v0, s2)
		draw := m + t.rng.NormFloat64()*math.Sqrt(va)
		if draw > bestDraw {
			best, bestDraw = v, draw
		}
	}
	return best
}

// incumbent is the posterior-mean-best value of each knob that has been
// measured at all. A knob with no observation yet is ABSENT from the map: the
// runner is then not told about it and the app keeps its compiled default,
// which is the honest baseline — inventing a value for it would measure a
// configuration nobody chose.
func (t *tuner) incumbent() map[string]string {
	m0, v0 := t.prior()
	s2 := t.sigma2()
	best := map[string]string{}
	for _, k := range t.cfg.knobs {
		bv, bm := "", math.Inf(-1)
		for _, v := range k.values {
			a := t.arm(k.name, v)
			if a.n == 0 {
				continue
			}
			m, _ := posterior(a, m0, v0, s2)
			if m > bm {
				bv, bm = v, m
			}
		}
		if bv != "" {
			best[k.name] = bv
		}
	}
	return best
}

// settingsFor builds the two environment strings for a round: the knob under
// test at the drawn value, every other knob at its incumbent.
func (t *tuner) settingsFor(under string, value string) (settings, routeSettings string) {
	inc := t.incumbent()
	var app, route []string
	for _, k := range t.cfg.knobs {
		v, ok := inc[k.name]
		if k.name == under {
			v, ok = value, true
		}
		if !ok {
			continue
		}
		if k.route {
			route = append(route, k.name, v)
		} else {
			app = append(app, k.name+"="+v)
		}
	}
	return strings.Join(app, " "), strings.Join(route, " ")
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// runDir is <out>/tune/r<round>-<knob>=<value>/, with the campaign's paired
// reference copied in the way bench/run-sweep.sh copies it into a sweep value:
// the reference is the control and must be the SAME route on every round, or
// the ratios of different rounds are not comparable.
func (t *tuner) runDir(round int, knob, value string) (string, error) {
	safe := strings.NewReplacer("/", "_", " ", "_").Replace(knob + "=" + value)
	dir := filepath.Join(t.cfg.out, "tune", fmt.Sprintf("r%d-%s", round, safe))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	for _, f := range []string{"paired-ref.txt", "paired-ref.tsv"} {
		b, err := os.ReadFile(filepath.Join(t.cfg.out, f)) //nolint:gosec // the campaign's own artifact
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil { //nolint:gosec // dir is this tool's own <out>/tune/r<n> path
			return "", err
		}
	}
	return dir, nil
}

// invoke runs the bench runner once, streaming its stdout into the log with the
// round as a prefix so an unattended tail can tell rounds apart.
func (t *tuner) invoke(prefix, settings, routeSettings, dir string) error {
	line := fmt.Sprintf("SETTINGS=%s ROUTE_SETTINGS=%s %s %s %s %s %s %d %s",
		shQuote(settings), shQuote(routeSettings), t.cfg.env,
		shQuote(filepath.Join(t.cfg.benchDir, t.cfg.runner)),
		shQuote(t.cfg.exitPK), shQuote(dir), shQuote(t.cfg.pins), t.cfg.trials, t.cfg.args) + " 2>&1"
	t.logf("%s %s\n", prefix, line)
	cmd := exec.Command("sh", "-c", line) //nolint:gosec // the whole point: the operator's own runner
	pipe, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(pipe)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		t.logf("%s %s\n", prefix, sc.Text())
	}
	return cmd.Wait()
}

// round runs exactly one round: pick the knob (round-robin), draw its value,
// run the runner once on top of the incumbent, update that one arm.
func (t *tuner) round(n int) record {
	k := t.cfg.knobs[(n-1)%len(t.cfg.knobs)]
	value := t.pick(k)
	settings, routeSettings := t.settingsFor(k.name, value)
	prefix := fmt.Sprintf("[r%d %s=%s]", n, k.name, value)
	r := record{round: n, knob: k.name, value: value, settings: settings, routeSettings: routeSettings}
	dir, err := t.runDir(n, k.name, value)
	if err != nil {
		t.logf("%s run directory: %v\n", prefix, err)
		return r
	}
	if err := t.invoke(prefix, settings, routeSettings, dir); err != nil {
		// A runner that exits non-zero has still usually written its rows (the
		// EXIT_RES gate alone makes run-mux.sh exit 1 at the very end), so the
		// artifacts are read either way and only their ABSENCE is a miss.
		t.logf("%s runner exited: %v — reading whatever it wrote\n", prefix, err)
	}
	obj, cells, ok := evaluate(dir, t.cfg.cells)
	r.objective, r.cells, r.ok = obj, cells, ok
	if !ok {
		t.logf("%s NO objective cell was produced — recorded as a missing observation, not a zero\n", prefix)
		return r
	}
	t.arm(k.name, value).update(obj)
	a := t.arm(k.name, value)
	t.logf("%s objective %.4f [%s] — arm now n=%d mean=%.4f\n", prefix, obj, strings.Join(cells, " "), a.n, a.mean)
	return r
}

func (t *tuner) run() {
	for n := 1; n <= t.cfg.rounds; n++ {
		r := t.round(n)
		t.rec = append(t.rec, r)
		if err := t.writeTSV(); err != nil {
			t.logf("tune.tsv: %v\n", err)
		}
		if err := t.writeIncumbent(); err != nil {
			t.logf("incumbent.txt: %v\n", err)
		}
	}
	t.table()
}

// ------------------------------------------------------------- the artifacts --

func (t *tuner) tuneDir() string { return filepath.Join(t.cfg.out, "tune") }

// writeTSV rewrites the whole record every round, so a run killed at round 7
// leaves seven readable rounds behind it.
func (t *tuner) writeTSV() error {
	var b strings.Builder
	b.WriteString("# bench/tune — one runner invocation per round, Thompson sampling over the knob grid\n")
	b.WriteString("# objective: " + objString(t.cfg.cells) + " (geometric mean of the cells)\n")
	b.WriteString("# round\tknob\tvalue\tsettings\troute_settings\tobjective\tcells\n")
	for _, r := range t.rec {
		obj := "-"
		if r.ok {
			obj = fmt.Sprintf("%.4f", r.objective)
		}
		fmt.Fprintf(&b, "%d\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.round, r.knob, r.value, r.settings, r.routeSettings, obj, strings.Join(r.cells, ","))
	}
	if err := os.MkdirAll(t.tuneDir(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(t.tuneDir(), "tune.tsv"), []byte(b.String()), 0o600)
}

func objString(cells []cellRef) string {
	s := make([]string, len(cells))
	for i, c := range cells {
		s[i] = c.String()
	}
	return strings.Join(s, ",")
}

// incumbentLines is the paste-ready shape of the current best configuration.
func (t *tuner) incumbentLines() string {
	settings, routeSettings := t.settingsFor("", "")
	var b strings.Builder
	fmt.Fprintf(&b, "SETTINGS=\"%s\"\n", settings)
	if routeSettings != "" {
		fmt.Fprintf(&b, "ROUTE_SETTINGS=\"%s\"\n", routeSettings)
	}
	return b.String()
}

func (t *tuner) writeIncumbent() error {
	if err := os.MkdirAll(t.tuneDir(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(t.tuneDir(), "incumbent.txt"), []byte(t.incumbentLines()), 0o600)
}

// table prints one block per knob: every value, how many rounds it was given,
// its measured mean and the standard error of that mean, and which value the
// posterior currently calls best. A value with n=1 has no stderr and its mean
// is one noisy sample — read it as "not yet ruled out", not as a result.
func (t *tuner) table() {
	inc := t.incumbent()
	t.logf("\n=== tune: %d rounds, objective %s\n", t.cfg.rounds, objString(t.cfg.cells))
	for _, k := range t.cfg.knobs {
		t.logf("\n%-28s %4s %10s %10s %s\n", k.name, "n", "mean", "+-stderr", "best?")
		for _, v := range k.values {
			a := t.arm(k.name, v)
			mean, se := "-", "-"
			if a.n > 0 {
				mean = fmt.Sprintf("%.4f", a.mean)
			}
			if a.n > 1 {
				se = fmt.Sprintf("%.4f", a.stderr())
			}
			best := ""
			if inc[k.name] == v {
				best = "<- best"
			}
			t.logf("  %-26s %4d %10s %10s %s\n", v, a.n, mean, se, best)
		}
	}
	t.logf("\nincumbent:\n%s\n%s\n", t.incumbentLines(), filepath.Join(t.tuneDir(), "tune.tsv"))
}

// ------------------------------------------------------------------- main ----

func main() {
	var (
		cfg       config
		knobsSpec = flag.String("knobs", "", "app knobs and their values: \"upload.chunk_bytes=1MiB,2MiB,4MiB;upload.concurrency=2,4,8\"")
		routeSpec = flag.String("route-knobs", "", "router knobs as whole flags: \"--ecf-max-window=4MiB,8MiB,16MiB\"")
		objSpec   = flag.String("objective", "", "cells to maximize: \"mux-tunnels-2/50up:ratio,mux-tunnels-2/10up:ratio\"")
	)
	flag.StringVar(&cfg.exitPK, "exit", "", "exit visor public key (first runner argument)")
	flag.StringVar(&cfg.out, "out", "", "campaign directory; runs go under <out>/tune/")
	flag.StringVar(&cfg.pins, "pins", "", "pins directory (third runner argument)")
	flag.StringVar(&cfg.benchDir, "bench", "bench", "directory holding the runners")
	flag.StringVar(&cfg.runner, "runner", "run-mux.sh", "runner to invoke once per round")
	flag.StringVar(&cfg.args, "args", "", "runner arguments AFTER out/pins/trials, verbatim: \"http://127.0.0.1:18080 \\\"<order>\\\"\"")
	flag.StringVar(&cfg.env, "env", "", "extra environment for every invocation, verbatim: \"TUNNELS=2 LEGS=\"")
	flag.IntVar(&cfg.trials, "trials", 2, "trials per cell (fourth runner argument)")
	flag.IntVar(&cfg.rounds, "rounds", 12, "runner invocations to spend")
	flag.Int64Var(&cfg.seed, "seed", 1, "RNG seed; the same seed and the same measurements replay the same rounds")
	flag.Float64Var(&cfg.sigma, "sigma", 0.2, "assumed observation noise before the arms can estimate it")
	flag.Parse()

	fail := func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, "tune: "+f+"\n", a...)
		os.Exit(2)
	}
	if cfg.exitPK == "" || cfg.out == "" || cfg.pins == "" {
		fail("-exit, -out and -pins are all required")
	}
	knobs, err := parseKnobs(*knobsSpec, false)
	if err != nil {
		fail("-knobs: %v", err)
	}
	rknobs, err := parseKnobs(*routeSpec, true)
	if err != nil {
		fail("-route-knobs: %v", err)
	}
	cfg.knobs = append(knobs, rknobs...)
	if len(cfg.knobs) == 0 {
		fail("no knobs: give -knobs and/or -route-knobs")
	}
	if cfg.cells, err = parseObjective(*objSpec); err != nil {
		fail("-objective: %v", err)
	}
	if cfg.rounds < 1 {
		fail("-rounds must be at least 1")
	}
	if _, err := os.Stat(filepath.Join(cfg.benchDir, cfg.runner)); err != nil {
		fail("no runner %s: %v", filepath.Join(cfg.benchDir, cfg.runner), err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.out, "tune"), 0o750); err != nil {
		fail("%v", err)
	}
	newTuner(cfg, os.Stdout).run()
}
