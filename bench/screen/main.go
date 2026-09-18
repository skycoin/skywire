// Package main implements `screen` — a two-level screening design over the live
// bench knobs, and the effect estimator that reads its result back.
//
//	go run ./bench/screen gen -factors bench/screen/factors-mux.tsv -runs 16 > variants.txt
//	bench/run-variants.sh <exit> bench/<date>/<commit>-screen $S/pins 3 <sink> "" variants.txt
//	go run ./bench/screen analyze variants.txt bench/<date>/<commit>-screen
//
// WHY A SCREEN COMES BEFORE A TUNER. bench/tune spends a rig invocation per
// round on ONE knob against the incumbent; that is the right instrument once the
// handful of knobs that matter is known, and the wrong one for finding them.
// There are 115 router knobs and 71 proxy knobs, and a one-knob-at-a-time walk
// over even twelve of them at two values each is 24 rig rows before anything is
// known about any of them — while a Plackett-Burman design moves EVERY factor in
// EVERY run, so each of the twelve main effects is estimated from all sixteen
// runs at once. That is the whole trick of a screening design: the N-1 columns
// are orthogonal, so the average of the runs where a factor sat high, minus the
// average of the runs where it sat low, is an unbiased estimate of that factor's
// main effect with every other factor averaged out of it.
//
// WHAT IT CANNOT DO, said plainly. A resolution-III design aliases each main
// effect with two-factor interactions: if ecf.max_window_bytes only helps while
// leg.starve_ratio is low, the screen will attribute some of that to whichever
// other factor happens to share the alias. A screen is a FILTER, not a verdict —
// it says which factors are worth a tuner and which are noise. The verdict is
// still a paired campaign under bench/verdict.sh.
//
// THE RESPONSE IS THE A/B RATIO, not the rate: bench/run-variants.sh interleaves
// the baseline with every design point, so each run has its own control measured
// seconds away, and the ratio is what survives a link whose bar swings 2x inside
// an hour. The standard error is taken from the replicate scatter — the several
// pair ratios each run contributes — which is the only honest noise estimate
// available when every run is measured once per trial.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ------------------------------------------------------------- the factors --

// factor is one knob and the two values the screen moves it between. The values
// keep their spelling exactly ("4MiB", "250ms", "false"): the CLI parses them.
type factor struct {
	name string
	low  string
	high string
}

// parseFactors reads "<knob> <low> <high>" lines. Blank lines and # comments are
// skipped, which is what lets the shipped factors file carry a line of prose per
// factor saying why it is in.
func parseFactors(r io.Reader) ([]factor, error) {
	var out []factor
	seen := map[string]bool{}
	sc := bufio.NewScanner(r)
	for ln := 1; sc.Scan(); ln++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want '<knob> <low> <high>', got %q", ln, line)
		}
		if seen[f[0]] {
			return nil, fmt.Errorf("line %d: %s is named twice", ln, f[0])
		}
		if f[1] == f[2] {
			return nil, fmt.Errorf("line %d: %s has the same low and high (%s) — it cannot be screened", ln, f[0], f[1])
		}
		seen[f[0]] = true
		out = append(out, factor{name: f[0], low: f[1], high: f[2]})
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("no factors")
	}
	return out, nil
}

// --------------------------------------------------------------- the design --

// Plackett-Burman generating rows. Row i of the design is the generator shifted
// left by i, for i in [0, n-1), and the last row is all low — the construction
// of Plackett & Burman (1946). Every column then carries n/2 highs and n/2 lows,
// and every PAIR of columns carries each of the four combinations n/4 times,
// which is what makes the main effects orthogonal. TestDesignIsBalanced asserts
// both properties rather than trusting these strings.
var pbGenerators = map[int]string{
	12: "++-+++---+-",
	24: "+++++-+-++--++--+-+----",
}

// plackettBurman builds the n-run design, n runs by n-1 columns of +1/-1.
func plackettBurman(n int) ([][]int, error) {
	gen, ok := pbGenerators[n]
	if !ok {
		return nil, fmt.Errorf("no Plackett-Burman generator for %d runs", n)
	}
	if len(gen) != n-1 {
		return nil, fmt.Errorf("the %d-run generator is %d long, want %d", n, len(gen), n-1)
	}
	d := make([][]int, n)
	for r := 0; r < n-1; r++ {
		row := make([]int, n-1)
		for c := 0; c < n-1; c++ {
			if gen[(c+r)%(n-1)] == '+' {
				row[c] = 1
			} else {
				row[c] = -1
			}
		}
		d[r] = row
	}
	last := make([]int, n-1)
	for c := range last {
		last[c] = -1
	}
	d[n-1] = last
	return d, nil
}

// hadamard16 is the 16-run two-level fractional factorial: the Sylvester
// Hadamard matrix of order 16 with its constant column dropped, which is the
// 2^(15-11) design. Column j of row i is the parity of the bits i and j share,
// so column 1, 2, 4 and 8 are the four base factors and the rest are their
// interaction columns — every column balanced, every pair orthogonal, and, like
// a Plackett-Burman design, resolution III.
func hadamard16() [][]int {
	const n = 16
	d := make([][]int, n)
	for i := 0; i < n; i++ {
		row := make([]int, n-1)
		for j := 1; j < n; j++ {
			bits := i & j
			parity := 0
			for b := bits; b != 0; b >>= 1 {
				parity ^= b & 1
			}
			if parity == 0 {
				row[j-1] = 1
			} else {
				row[j-1] = -1
			}
		}
		d[i] = row
	}
	return d
}

// designName says which construction a run count asks for.
func designName(runs int) string {
	switch runs {
	case 12, 24:
		return "Plackett-Burman"
	case 16:
		return "fractional factorial (Hadamard/Sylvester, 2^(15-11))"
	}
	return "unknown"
}

// design returns the first k columns of the runs-run design.
func design(runs, k int) ([][]int, error) {
	var (
		d   [][]int
		err error
	)
	switch runs {
	case 12, 24:
		d, err = plackettBurman(runs)
	case 16:
		d = hadamard16()
	default:
		return nil, fmt.Errorf("runs must be 12, 16 or 24, not %d", runs)
	}
	if err != nil {
		return nil, err
	}
	if k > runs-1 {
		return nil, fmt.Errorf("a %d-run design holds at most %d factors, not %d — use -runs %d",
			runs, runs-1, k, nextSize(k))
	}
	if k < 1 {
		return nil, errors.New("no factors")
	}
	out := make([][]int, len(d))
	for i, row := range d {
		out[i] = append([]int(nil), row[:k]...)
	}
	return out, nil
}

// nextSize: the smallest design that holds k factors.
func nextSize(k int) int {
	switch {
	case k <= 11:
		return 12
	case k <= 15:
		return 16
	default:
		return 24
	}
}

// ------------------------------------------------------------------- gen ----

// writeVariants emits the run-variants.sh variants file: the baseline first —
// naming no knob, so every factor sits at the value the visor already holds —
// then one line per design point. The `# factor` header lines are what analyze
// reads back to know which value was the high level.
func writeVariants(w io.Writer, fs []factor, d [][]int, runs int, src string) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# a %d-run %s screening design over %d factors\n", runs, designName(runs), len(fs))
	fmt.Fprintf(&b, "# generated by: bench/screen gen -factors %s -runs %d\n", src, runs)
	fmt.Fprintf(&b, "# run it with:  bench/run-variants.sh <exit pk> <out dir> <pins> 3 <sink> \"\" <this file>\n")
	fmt.Fprintf(&b, "# read it with: go run ./bench/screen analyze <this file> <out dir>\n")
	fmt.Fprintf(&b, "# baseline names no knob: every factor stays where the visor already holds it, and it is\n")
	fmt.Fprintf(&b, "# re-measured immediately before every design point, so each run has its own control.\n")
	for _, f := range fs {
		fmt.Fprintf(&b, "# factor %s low=%s high=%s\n", f.name, f.low, f.high)
	}
	b.WriteString("baseline\n")
	for i, row := range d {
		kv := make([]string, 0, len(fs))
		for j, f := range fs {
			v := f.low
			if row[j] > 0 {
				v = f.high
			}
			kv = append(kv, f.name+"="+v)
		}
		fmt.Fprintf(&b, "r%02d\t%s\n", i+1, strings.Join(kv, " "))
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func runGen(args []string) error {
	fs := flag.NewFlagSet("gen", flag.ContinueOnError)
	factors := fs.String("factors", "bench/screen/factors-mux.tsv", "factors file: '<knob> <low> <high>' per line")
	runs := fs.Int("runs", 16, "design size: 12 or 24 (Plackett-Burman) or 16 (fractional factorial)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	f, err := os.Open(*factors) //nolint:gosec // a path the operator typed
	if err != nil {
		return err
	}
	defer f.Close() //nolint:errcheck,gosec
	list, err := parseFactors(f)
	if err != nil {
		return fmt.Errorf("%s: %w", *factors, err)
	}
	d, err := design(*runs, len(list))
	if err != nil {
		return err
	}
	return writeVariants(os.Stdout, list, d, *runs, *factors)
}

// ---------------------------------------------------------------- analyze ----

// variantsFile is what gen wrote and run-variants.sh ran: the factor levels, and
// which knob assignment each run carried.
type variantsFile struct {
	baseline string
	factors  []factor
	runs     []string            // variant names, in file order
	level    map[string][]int    // variant -> +1/-1 per factor
	values   map[string][]string // variant -> the value each factor sat at
}

// parseVariants reads the file back. The `# factor` headers carry the levels;
// without them (a hand-written variants file) the levels are inferred from the
// two distinct values each knob takes, low being the numerically or
// lexicographically smaller — stated here because an inferred sign flips the
// sign of the effect, nothing else.
func parseVariants(r io.Reader) (*variantsFile, error) {
	v := &variantsFile{level: map[string][]int{}, values: map[string][]string{}}
	var (
		lines  [][]string // variant name, then key=value pairs
		names  []string
		hdr    = map[string]factor{}
		hdrOrd []string
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "#") {
			f := strings.Fields(strings.TrimPrefix(line, "#"))
			if len(f) == 4 && f[0] == "factor" && strings.HasPrefix(f[2], "low=") && strings.HasPrefix(f[3], "high=") {
				name := f[1]
				hdr[name] = factor{name: name, low: strings.TrimPrefix(f[2], "low="), high: strings.TrimPrefix(f[3], "high=")}
				hdrOrd = append(hdrOrd, name)
			}
			continue
		}
		if line == "" {
			continue
		}
		f := strings.Fields(line)
		lines = append(lines, f)
		names = append(names, f[0])
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(lines) < 2 {
		return nil, errors.New("the variants file holds fewer than two variants")
	}
	v.baseline = names[0]
	for _, n := range names {
		if n == "baseline" {
			v.baseline = n
		}
	}
	// the factor order: the headers when they are there, else first sight
	seen := map[string]bool{}
	var order []string
	if len(hdrOrd) > 0 {
		order = hdrOrd
		for _, n := range order {
			seen[n] = true
		}
	}
	vals := map[string]map[string]bool{}
	for _, f := range lines {
		if f[0] == v.baseline {
			continue
		}
		for _, kv := range f[1:] {
			k, val, ok := strings.Cut(kv, "=")
			if !ok {
				return nil, fmt.Errorf("%q in variant %s is not key=value", kv, f[0])
			}
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
			if vals[k] == nil {
				vals[k] = map[string]bool{}
			}
			vals[k][val] = true
		}
	}
	for _, k := range order {
		if f, ok := hdr[k]; ok {
			v.factors = append(v.factors, f)
			continue
		}
		var got []string
		for val := range vals[k] {
			got = append(got, val)
		}
		if len(got) != 2 {
			return nil, fmt.Errorf("%s takes %d distinct values; a two-level screen needs exactly 2", k, len(got))
		}
		sort.Slice(got, func(i, j int) bool {
			a, ea := strconv.ParseFloat(got[i], 64)
			b, eb := strconv.ParseFloat(got[j], 64)
			if ea == nil && eb == nil {
				return a < b
			}
			return got[i] < got[j]
		})
		v.factors = append(v.factors, factor{name: k, low: got[0], high: got[1]})
	}
	for _, f := range lines {
		if f[0] == v.baseline {
			continue
		}
		got := map[string]string{}
		for _, kv := range f[1:] {
			k, val, _ := strings.Cut(kv, "=")
			got[k] = val
		}
		lv := make([]int, len(v.factors))
		vv := make([]string, len(v.factors))
		for i, fa := range v.factors {
			val, ok := got[fa.name]
			if !ok {
				return nil, fmt.Errorf("variant %s does not set %s — a screening design moves every factor in every run", f[0], fa.name)
			}
			vv[i] = val
			switch val {
			case fa.high:
				lv[i] = 1
			case fa.low:
				lv[i] = -1
			default:
				return nil, fmt.Errorf("variant %s sets %s=%s, which is neither its low (%s) nor its high (%s)", f[0], fa.name, val, fa.low, fa.high)
			}
		}
		v.runs = append(v.runs, f[0])
		v.level[f[0]] = lv
		v.values[f[0]] = vv
	}
	return v, nil
}

// row is one measured transfer out of run-variants.sh's rows.tsv.
type row struct {
	variant string
	cell    string
	pair    string
	mbps    float64
	hashOK  bool
}

func readRows(path string) ([]row, error) {
	f, err := os.Open(path) //nolint:gosec // a path the operator typed
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck,gosec
	var out []row
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		c := strings.Split(line, "\t")
		if len(c) < 6 {
			continue
		}
		v, err := strconv.ParseFloat(c[4], 64)
		if err != nil {
			continue
		}
		out = append(out, row{variant: c[0], cell: c[1], pair: c[3], mbps: v, hashOK: c[5] == "1"})
	}
	return out, sc.Err()
}

// ratios turns the rows into, per cell, per run, the run's PAIR RATIOS against
// the baseline row of the same pair. A row whose transfer did not hash-verify is
// dropped with its partner: half a pair is not a ratio.
func ratios(rows []row, baseline string) (map[string]map[string][]float64, int) {
	base := map[string]float64{} // cell\tpair -> baseline MB/s
	dropped := 0
	for _, r := range rows {
		if r.variant != baseline {
			continue
		}
		if !r.hashOK || r.mbps <= 0 {
			dropped++
			continue
		}
		base[r.cell+"\t"+r.pair] = r.mbps
	}
	out := map[string]map[string][]float64{}
	for _, r := range rows {
		if r.variant == baseline {
			continue
		}
		if !r.hashOK || r.mbps <= 0 {
			dropped++
			continue
		}
		b, ok := base[r.cell+"\t"+r.pair]
		if !ok {
			dropped++
			continue
		}
		if out[r.cell] == nil {
			out[r.cell] = map[string][]float64{}
		}
		out[r.cell][r.variant] = append(out[r.cell][r.variant], r.mbps/b)
	}
	return out, dropped
}

func mean(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	s := 0.0
	for _, x := range v {
		s += x
	}
	return s / float64(len(v))
}

// effect is one factor's main effect in one cell.
type effect struct {
	factor  factor
	value   float64 // mean(high runs) - mean(low runs), in ratio units
	se      float64
	nHigh   int
	nLow    int
	signifi bool
}

// estimate computes every factor's main effect in one cell, and the pooled
// standard error from the replicate scatter.
//
// Each run contributes r pair ratios. The pooled within-run variance s^2 is the
// scatter of those ratios about their own run's mean, over all runs; the
// variance of a run's mean is then s^2/r, and the variance of an effect — a
// difference of two averages of run means — is s^2/r * (1/nHigh + 1/nLow). With
// one ratio per run there is no scatter to pool and the standard error is
// reported as 0, which the table prints as "-" rather than pretending to a
// significance it cannot support.
func estimate(fs []factor, level map[string][]int, byRun map[string][]float64) []effect {
	var (
		ss   float64
		dof  int
		reps int
	)
	for _, rs := range byRun {
		if len(rs) < 2 {
			continue
		}
		m := mean(rs)
		for _, x := range rs {
			ss += (x - m) * (x - m)
		}
		dof += len(rs) - 1
		reps += len(rs)
	}
	var s2 float64
	if dof > 0 {
		s2 = ss / float64(dof)
	}
	r := 1.0
	if n := len(byRun); n > 0 && reps > 0 {
		r = float64(reps) / float64(n)
	}
	out := make([]effect, 0, len(fs))
	for j, f := range fs {
		var hi, lo []float64
		for name, rs := range byRun {
			lv, ok := level[name]
			if !ok || j >= len(lv) || len(rs) == 0 {
				continue
			}
			if lv[j] > 0 {
				hi = append(hi, mean(rs))
			} else {
				lo = append(lo, mean(rs))
			}
		}
		e := effect{factor: f, nHigh: len(hi), nLow: len(lo)}
		if len(hi) > 0 && len(lo) > 0 {
			e.value = mean(hi) - mean(lo)
			if s2 > 0 && r > 0 {
				e.se = math.Sqrt(s2 / r * (1/float64(len(hi)) + 1/float64(len(lo))))
			}
			e.signifi = e.se > 0 && math.Abs(e.value) > 2*e.se
		}
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool { return math.Abs(out[i].value) > math.Abs(out[j].value) })
	return out
}

// cellsOf returns the cell names in a stable order: downloads before uploads,
// smaller before larger, so a table reads the way the runner measured.
func cellsOf(m map[string]map[string][]float64) []string {
	var out []string
	for c := range m {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

func runAnalyze(args []string) error {
	fs := flag.NewFlagSet("analyze", flag.ContinueOnError)
	bar := fs.Float64("bar", 2, "how many standard errors an effect must clear to be called real")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: screen analyze [-bar 2] <variants file> <out dir>")
	}
	vf, err := os.Open(fs.Arg(0)) //nolint:gosec // a path the operator typed
	if err != nil {
		return err
	}
	defer vf.Close() //nolint:errcheck,gosec
	v, err := parseVariants(vf)
	if err != nil {
		return fmt.Errorf("%s: %w", fs.Arg(0), err)
	}
	dir := fs.Arg(1)
	path := filepath.Join(dir, "sweep", "rows.tsv")
	if _, err := os.Stat(path); err != nil {
		path = filepath.Join(dir, "rows.tsv")
	}
	rows, err := readRows(path)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("%s holds no rows", path)
	}
	byCell, dropped := ratios(rows, v.baseline)
	if len(byCell) == 0 {
		return fmt.Errorf("%s holds no pair a ratio could be taken from (baseline %q)", path, v.baseline)
	}
	fmt.Printf("screen: %s, %d factors, %d runs, baseline %q, %d rows (%d dropped: failed hash or no partner)\n",
		fs.Arg(0), len(v.factors), len(v.runs), v.baseline, len(rows), dropped)
	best := map[string]float64{}
	for _, cell := range cellsOf(byCell) {
		byRun := byCell[cell]
		reps := 0
		for _, rs := range byRun {
			reps += len(rs)
		}
		fmt.Printf("\ncell %s — %d run(s) measured, %d pair ratio(s)\n", cell, len(byRun), reps)
		fmt.Printf("  %-4s %-28s %-9s %-9s %9s %9s %6s\n", "rank", "factor", "low", "high", "effect", "se", ">"+strconv.FormatFloat(*bar, 'g', -1, 64)+"se")
		for i, e := range estimate(v.factors, v.level, byRun) {
			se := fmt.Sprintf("%9s", "-")
			flag := "-"
			if e.se > 0 {
				se = fmt.Sprintf("%9.4f", e.se)
				flag = "no"
				if math.Abs(e.value) > *bar*e.se {
					flag = "YES"
				}
			}
			fmt.Printf("  %-4d %-28s %-9s %-9s %+9.4f %s %6s\n", i+1, e.factor.name, e.factor.low, e.factor.high, e.value, se, flag)
			if e.se > 0 && math.Abs(e.value) > *bar*e.se {
				if z := math.Abs(e.value) / e.se; z > best[e.factor.name] {
					best[e.factor.name] = z
				}
			}
		}
	}
	var named []string
	for n := range best {
		named = append(named, n)
	}
	sort.Slice(named, func(i, j int) bool { return best[named[i]] > best[named[j]] })
	fmt.Println()
	switch {
	case len(named) == 0:
		fmt.Printf("recommendation: no factor cleared %g standard errors — either the effects are smaller than this link's noise or the screen needs more trials per run (SWEEP_TRIALS); do not spend tuner rounds on any of these yet.\n", *bar)
	default:
		if len(named) > 3 {
			named = named[:3]
		}
		fmt.Printf("recommendation: give %s to the tuner (bench/tune -knobs '%s'), and leave the rest at their defaults — they did not move this rig.\n",
			strings.Join(named, ", "), tuneSpec(named, v.factors))
	}
	return nil
}

// tuneSpec spells the surviving factors as a bench/tune -knobs grid: the two
// screened levels are the ends of the grid the tuner walks.
func tuneSpec(names []string, fs []factor) string {
	by := map[string]factor{}
	for _, f := range fs {
		by[f.name] = f
	}
	var parts []string
	for _, n := range names {
		f := by[n]
		parts = append(parts, fmt.Sprintf("%s=%s,%s", n, f.low, f.high))
	}
	return strings.Join(parts, ";")
}

func usage() {
	fmt.Fprint(os.Stderr, `screen — a two-level screening design over the live bench knobs

  screen gen -factors <file> -runs 12|16|24 > variants.txt
  screen analyze [-bar 2] variants.txt <out dir>

gen reads '<knob> <low> <high>' lines and writes a bench/run-variants.sh
variants file: the baseline first, then one variant per design point.
analyze reads that file back together with the sweep's rows.tsv and prints each
factor's main effect per cell, its standard error from the replicate scatter,
and which factors deserve bench/tune.
`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "gen":
		err = runGen(os.Args[2:])
	case "analyze":
		err = runAnalyze(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "screen:", err)
		os.Exit(1)
	}
}
