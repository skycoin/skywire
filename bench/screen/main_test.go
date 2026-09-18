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

// --- the design ---------------------------------------------------------------

// TestDesignIsBalanced is the property the whole method rests on: every column
// carries as many highs as lows (so a main effect is not a level count in
// disguise), and every PAIR of columns carries each of the four level
// combinations equally often (so two factors cannot be confounded with each
// other). Both are asserted for every design size and every factor count the
// generator will hand out, rather than trusting the generating rows.
func TestDesignIsBalanced(t *testing.T) {
	for _, runs := range []int{12, 16, 24} {
		d, err := design(runs, runs-1)
		if err != nil {
			t.Fatalf("design(%d): %v", runs, err)
		}
		if len(d) != runs {
			t.Fatalf("design(%d) has %d rows, want %d", runs, len(d), runs)
		}
		k := runs - 1
		for c := 0; c < k; c++ {
			hi := 0
			for r := 0; r < runs; r++ {
				switch d[r][c] {
				case 1:
					hi++
				case -1:
				default:
					t.Fatalf("runs=%d [%d][%d] = %d, want +-1", runs, r, c, d[r][c])
				}
			}
			if hi != runs/2 {
				t.Errorf("runs=%d column %d has %d highs, want %d", runs, c, hi, runs/2)
			}
		}
		for a := 0; a < k; a++ {
			for b := a + 1; b < k; b++ {
				count := map[[2]int]int{}
				for r := 0; r < runs; r++ {
					count[[2]int{d[r][a], d[r][b]}]++
				}
				for _, combo := range [][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}} {
					if got := count[combo]; got != runs/4 {
						t.Errorf("runs=%d columns %d,%d: combination %v appears %d times, want %d",
							runs, a, b, combo, got, runs/4)
					}
				}
			}
		}
	}
}

// A design is also balanced when only the first k columns are taken — which is
// what a 12-factor screen in a 16-run design actually uses.
func TestDesignSubsetIsBalanced(t *testing.T) {
	d, err := design(16, 12)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range d {
		if len(row) != 12 {
			t.Fatalf("row has %d columns, want 12", len(row))
		}
	}
	for c := 0; c < 12; c++ {
		hi := 0
		for r := range d {
			if d[r][c] == 1 {
				hi++
			}
		}
		if hi != 8 {
			t.Errorf("column %d has %d highs, want 8", c, hi)
		}
	}
}

func TestDesignRefusesTooManyFactors(t *testing.T) {
	if _, err := design(12, 12); err == nil {
		t.Fatal("a 12-run design accepted 12 factors")
	} else if !strings.Contains(err.Error(), "-runs 16") {
		t.Errorf("the refusal does not name the design that would fit: %v", err)
	}
	if _, err := design(13, 3); err == nil {
		t.Fatal("13 runs was accepted")
	}
}

// --- gen ----------------------------------------------------------------------

func TestParseFactors(t *testing.T) {
	in := `# a comment
# why this one is in
leg.starve_ratio	3	12

ecf.max_window_bytes	4MiB	16MiB
`
	got, err := parseFactors(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].name != "leg.starve_ratio" || got[1].high != "16MiB" {
		t.Fatalf("parsed %+v", got)
	}
	for _, bad := range []string{"only.two 1\n", "a 1 1\n", "a 1 2\na 3 4\n", "# nothing\n"} {
		if _, err := parseFactors(strings.NewReader(bad)); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestGenRoundTrips(t *testing.T) {
	fs := []factor{
		{"a.one", "1", "2"},
		{"b.two", "lo", "hi"},
		{"c.three", "4MiB", "16MiB"},
	}
	d, err := design(12, len(fs))
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeVariants(&buf, fs, d, 12, "factors.tsv"); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	var body []string
	for _, l := range lines {
		if !strings.HasPrefix(l, "#") {
			body = append(body, l)
		}
	}
	if body[0] != "baseline" {
		t.Fatalf("first variant is %q, want baseline", body[0])
	}
	if len(body) != 13 {
		t.Fatalf("%d variants, want baseline + 12", len(body))
	}
	for _, l := range body[1:] {
		f := strings.Fields(l)
		if len(f) != 4 {
			t.Fatalf("variant %q does not set all three factors", l)
		}
	}
	// and the file reads back as the same design
	v, err := parseVariants(strings.NewReader(buf.String()))
	if err != nil {
		t.Fatal(err)
	}
	if v.baseline != "baseline" || len(v.runs) != 12 || len(v.factors) != 3 {
		t.Fatalf("round trip lost something: %+v", v)
	}
	for i, name := range v.runs {
		for j := range fs {
			if v.level[name][j] != d[i][j] {
				t.Fatalf("run %s factor %d: level %d, design %d", name, j, v.level[name][j], d[i][j])
			}
		}
	}
}

// A hand-written variants file has no `# factor` headers; the levels are then
// inferred, low being the smaller value.
func TestParseVariantsInfersLevels(t *testing.T) {
	in := "baseline\nr1\tk.a=2 k.b=10\nr2\tk.a=8 k.b=1\n"
	v, err := parseVariants(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if v.factors[0].low != "2" || v.factors[0].high != "8" {
		t.Fatalf("k.a levels: %+v", v.factors[0])
	}
	if v.level["r1"][0] != -1 || v.level["r2"][0] != 1 {
		t.Fatalf("levels: %+v", v.level)
	}
	if v.level["r1"][1] != 1 || v.level["r2"][1] != -1 {
		t.Fatalf("k.b levels: %+v", v.level)
	}
	if _, err := parseVariants(strings.NewReader("baseline\nr1\tk.a=1\nr2\tk.b=2\n")); err == nil {
		t.Error("a run that does not move every factor was accepted")
	}
}

// --- the estimator -------------------------------------------------------------

// synthRows writes a rows.tsv the way run-variants.sh writes one: for each run,
// `reps` A/B pairs whose ratio is 1 + sum(beta_j * level_j) plus a deterministic
// wobble. The baseline row of every pair sits at exactly 5 MB/s, so the ratio the
// estimator recovers is the one that was built in.
func synthRows(t *testing.T, dir string, fs []factor, d [][]int, beta []float64, reps int, wobble float64) {
	t.Helper()
	var b strings.Builder
	b.WriteString("# variant\tcell\ttrial\tpair\tMBps\thash_ok\tknobs\tnote\n")
	pair := 0
	for rep := 1; rep <= reps; rep++ {
		for i := range d {
			pair++
			ratio := 1.0
			for j := range fs {
				ratio += beta[j] * float64(d[i][j])
			}
			// a zero-sum wobble across the replicates of one run, so the
			// scatter is real but the run mean is exactly the built-in ratio
			w := wobble
			if rep%2 == 0 {
				w = -wobble
			}
			base := 5.0
			fmt.Fprintf(&b, "baseline\t50down\t%d\t%d\t%.6f\t1\t-\t-\n", rep, pair, base)
			fmt.Fprintf(&b, "r%02d\t50down\t%d\t%d\t%.6f\t1\t-\t-\n", i+1, rep, pair, base*(ratio+w))
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "sweep"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sweep", "rows.tsv"), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestEstimateRecoversAKnownEffect: one factor is worth +10 % at its high level
// (an effect of 0.20 between the two levels) and the rest are worth nothing. The
// estimator must recover 0.20 on that factor, ~0 on the others, and must call
// only the first one real.
func TestEstimateRecoversAKnownEffect(t *testing.T) {
	fs := []factor{{"a.one", "1", "2"}, {"b.two", "1", "2"}, {"c.three", "1", "2"}, {"d.four", "1", "2"}}
	d, err := design(12, len(fs))
	if err != nil {
		t.Fatal(err)
	}
	beta := []float64{0.10, 0, 0, 0}
	dir := t.TempDir()
	synthRows(t, dir, fs, d, beta, 4, 0.01)

	rows, err := readRows(filepath.Join(dir, "sweep", "rows.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	byCell, dropped := ratios(rows, "baseline")
	if dropped != 0 {
		t.Fatalf("%d rows dropped", dropped)
	}
	level := map[string][]int{}
	for i := range d {
		level[fmt.Sprintf("r%02d", i+1)] = d[i]
	}
	eff := estimate(fs, level, byCell["50down"])
	if len(eff) != 4 {
		t.Fatalf("%d effects", len(eff))
	}
	if eff[0].factor.name != "a.one" {
		t.Fatalf("the ranking puts %s first", eff[0].factor.name)
	}
	if math.Abs(eff[0].value-0.20) > 1e-6 {
		t.Errorf("effect of a.one = %.6f, want 0.20", eff[0].value)
	}
	if !eff[0].signifi {
		t.Errorf("a.one was not called real: effect %.4f se %.4f", eff[0].value, eff[0].se)
	}
	for _, e := range eff[1:] {
		if math.Abs(e.value) > 1e-6 {
			t.Errorf("%s has effect %.6f, want 0", e.factor.name, e.value)
		}
		if e.signifi {
			t.Errorf("%s was called real with effect %.6f se %.6f", e.factor.name, e.value, e.se)
		}
	}
	if eff[0].nHigh != 6 || eff[0].nLow != 6 {
		t.Errorf("a.one was measured at %d high / %d low runs, want 6/6", eff[0].nHigh, eff[0].nLow)
	}
}

// Noise the size of the effect must NOT be called real: the same effect measured
// against a scatter as large as itself falls under two standard errors.
func TestEstimateStaysQuietUnderNoise(t *testing.T) {
	fs := []factor{{"a.one", "1", "2"}, {"b.two", "1", "2"}, {"c.three", "1", "2"}}
	d, err := design(12, len(fs))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	synthRows(t, dir, fs, d, []float64{0.01, 0, 0}, 2, 0.5)
	rows, err := readRows(filepath.Join(dir, "sweep", "rows.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	byCell, _ := ratios(rows, "baseline")
	level := map[string][]int{}
	for i := range d {
		level[fmt.Sprintf("r%02d", i+1)] = d[i]
	}
	for _, e := range estimate(fs, level, byCell["50down"]) {
		if e.signifi {
			t.Errorf("%s called real: effect %.4f se %.4f", e.factor.name, e.value, e.se)
		}
	}
}

// A row that did not hash-verify takes its pair out of the ratios rather than
// contributing a rate that measured a failed transfer.
func TestRatiosDropUnverifiedPairs(t *testing.T) {
	dir := t.TempDir()
	body := "# h\n" +
		"baseline\t50down\t1\t1\t5.000\t1\t-\t-\n" +
		"r01\t50down\t1\t1\t6.000\t1\t-\t-\n" +
		"baseline\t50down\t2\t2\t5.000\t1\t-\t-\n" +
		"r01\t50down\t2\t2\t9.000\t0\t-\t-\n" +
		"baseline\t50down\t3\t3\t5.000\t0\t-\t-\n" +
		"r01\t50down\t3\t3\t7.000\t1\t-\t-\n"
	if err := os.WriteFile(filepath.Join(dir, "rows.tsv"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rows, err := readRows(filepath.Join(dir, "rows.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	byCell, dropped := ratios(rows, "baseline")
	got := byCell["50down"]["r01"]
	if len(got) != 1 || math.Abs(got[0]-1.2) > 1e-9 {
		t.Fatalf("ratios = %v, want exactly [1.2]", got)
	}
	if dropped != 3 {
		t.Errorf("%d rows dropped, want 3 (the failed row, its partner and the orphan)", dropped)
	}
}

// The shipped factors file must parse and must fit a design the tool can build.
func TestShippedFactorsFile(t *testing.T) {
	f, err := os.Open("factors-mux.tsv")
	if err != nil {
		t.Skipf("no shipped factors file: %v", err)
	}
	defer f.Close() //nolint:errcheck,gosec
	fs, err := parseFactors(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(fs) < 10 {
		t.Errorf("the shipped screen holds %d factors", len(fs))
	}
	if _, err := design(nextSize(len(fs)), len(fs)); err != nil {
		t.Errorf("the shipped factors do not fit the design nextSize picks: %v", err)
	}
}
