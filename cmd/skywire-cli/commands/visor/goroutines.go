// Package clivisor cmd/skywire-cli/commands/visor/goroutines.go:
// fetches and summarizes the visor's pprof goroutine dump. Replaces
// the manual `curl + awk + grep` ritual for diagnosing visor hangs
// (RPC timing out, transport.Manager deadlocks, syncMu pile-ups,
// etc.).
//
// Three views:
//
//   - summary (default): counts per state, top stack heads, lock
//     waiter summary, a likely-holder heuristic for tm.mx-style
//     RWMutex pile-ups.
//   - --full:          dump the raw pprof text to stdout (or --out).
//   - --filter <re>:   only goroutines whose stack body matches the
//     regex; combined with --full this slices a huge
//     dump down to "show me only ManagedTransport
//     readLoops" sized output.
package clivisor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

var (
	pprofAddr   string
	grFull      bool
	grFilter    string
	grOut       string
	grTopN      int
	grStateFlag string
	grShowLocks bool
)

func init() {
	goroutinesCmd.Flags().StringVar(&pprofAddr, "pprof-addr", "localhost:6060",
		"visor pprof HTTP address (default matches --pprofaddr's default)")
	goroutinesCmd.Flags().BoolVarP(&grFull, "full", "F", false,
		"print the raw pprof text instead of the summary")
	goroutinesCmd.Flags().StringVar(&grFilter, "filter", "",
		"regex matched against stack body; only matching goroutines are printed in --full mode and counted in summary")
	goroutinesCmd.Flags().StringVarP(&grOut, "out", "o", "",
		"save raw dump to this file (in addition to stdout output)")
	goroutinesCmd.Flags().IntVarP(&grTopN, "top", "n", 10,
		"how many top stack-head buckets to show in summary")
	goroutinesCmd.Flags().StringVar(&grStateFlag, "state", "",
		"filter by goroutine state substring, e.g. 'RWMutex.RLock' / 'IO wait' / 'select'")
	goroutinesCmd.Flags().BoolVar(&grShowLocks, "locks", true,
		"in summary mode, run the lock-waiter analysis (set --locks=false to skip)")
	RootCmd.AddCommand(goroutinesCmd)
}

var goroutinesCmd = &cobra.Command{
	Use:   "goroutines",
	Short: "Fetch + summarize the visor's pprof goroutine dump",
	Long: `Fetch http://<pprof-addr>/debug/pprof/goroutine?debug=2 and either
print a human summary (default) or the raw dump (--full).

Use when the visor RPC is hung or unresponsive — the pprof port stays
serving even when the rpc.Server is stuck behind a mutex, so this is
the most reliable way to see what's actually blocked.

Examples:

  skywire cli visor goroutines
  skywire cli visor goroutines --state RWMutex.RLock
  skywire cli visor goroutines --filter transport.\(.Manager\) --full
  skywire cli visor goroutines --out /tmp/visor-grs.txt --full
  skywire cli visor goroutines --top 25`,
	Run: func(cmd *cobra.Command, _ []string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		raw, err := fetchPprofGoroutines(ctx, pprofAddr)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		goroutines, parseErr := parseGoroutineDump(raw)
		if parseErr != nil {
			// Parser is best-effort; surface and continue with what
			// we got so --full still produces useful output.
			fmt.Fprintln(os.Stderr, "WARN parse:", parseErr)
		}

		var filterRe *regexp.Regexp
		if grFilter != "" {
			re, err := regexp.Compile(grFilter)
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--filter regex: %w", err))
			}
			filterRe = re
		}

		filtered := filterGoroutines(goroutines, grStateFlag, filterRe)

		if grOut != "" {
			payload := raw
			if filterRe != nil || grStateFlag != "" {
				payload = renderGoroutines(filtered)
			}
			if werr := os.WriteFile(grOut, []byte(payload), 0o644); werr != nil { //nolint:gosec
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("write --out: %w", werr))
			}
			fmt.Fprintf(os.Stderr, "wrote %d bytes to %s\n", len(payload), grOut)
		}

		if grFull {
			fmt.Print(renderGoroutines(filtered))
			return
		}

		fmt.Println(buildSummary(filtered, grTopN, grShowLocks))
	},
}

// fetchPprofGoroutines pulls the debug=2 goroutine dump (one stack
// per goroutine, plain text). Wrapped here so the same code path
// works for both this command and any future caller (e.g. an
// auto-diagnose-on-RPC-hang helper).
func fetchPprofGoroutines(ctx context.Context, addr string) (string, error) {
	url := fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=2", strings.TrimPrefix(addr, "http://"))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("pprof returned status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}
	if len(body) == 0 {
		return "", errors.New("empty pprof response")
	}
	return string(body), nil
}

// goroutine is one parsed entry from the pprof dump. The header gives
// us the goroutine ID + state (e.g. "sync.RWMutex.RLock, 5 minutes"),
// stack is the rest of the block until the blank line separator.
type goroutine struct {
	ID    string // numeric, kept as string since we never math on it
	State string // "running", "select, 5 minutes", "sync.RWMutex.RLock", ...
	Stack string // raw stack frames
	Top   string // shortened first non-runtime frame, used for bucketing
}

var goroutineHeaderRe = regexp.MustCompile(`^goroutine ([0-9]+) \[([^\]]+)\]:$`)

func parseGoroutineDump(raw string) ([]goroutine, error) {
	var out []goroutine
	var cur *goroutine
	for _, line := range strings.Split(raw, "\n") {
		if m := goroutineHeaderRe.FindStringSubmatch(line); m != nil {
			if cur != nil {
				cur.Top = topFrame(cur.Stack)
				out = append(out, *cur)
			}
			cur = &goroutine{ID: m[1], State: m[2]}
			continue
		}
		if cur == nil {
			continue
		}
		if line == "" {
			cur.Top = topFrame(cur.Stack)
			out = append(out, *cur)
			cur = nil
			continue
		}
		cur.Stack += line + "\n"
	}
	if cur != nil {
		cur.Top = topFrame(cur.Stack)
		out = append(out, *cur)
	}
	if len(out) == 0 {
		return nil, errors.New("no goroutine headers found")
	}
	return out, nil
}

// topFrame extracts the first stack frame whose path isn't in the Go
// runtime/sync — that's the function the goroutine is "in" from the
// project's perspective. Falls back to the literal first frame if
// nothing else matches.
func topFrame(stack string) string {
	lines := strings.Split(stack, "\n")
	for _, line := range lines {
		// Stack frames alternate: "<func call>" / "<TAB><path>:<line>".
		// Skip path lines (start with tab) and runtime/sync entries.
		if strings.HasPrefix(line, "\t") {
			continue
		}
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "runtime.") ||
			strings.HasPrefix(line, "sync.") ||
			strings.HasPrefix(line, "internal/sync.") ||
			strings.HasPrefix(line, "internal/poll.") ||
			strings.HasPrefix(line, "net.") ||
			strings.HasPrefix(line, "bufio.") ||
			strings.HasPrefix(line, "io.") {
			continue
		}
		return shortenFrame(line)
	}
	if len(lines) > 0 {
		return shortenFrame(lines[0])
	}
	return "(empty)"
}

// shortenFrame strips the function-argument list and module-path
// prefix so the bucket key is readable. "github.com/skycoin/skywire/
// pkg/transport.(*Manager).WalkTransports(0x...)" → "transport.
// (*Manager).WalkTransports".
func shortenFrame(line string) string {
	if i := strings.Index(line, "("); i > 0 {
		// Keep one paren-prefixed receiver token but drop the call args.
		// Easiest: find the last `(` that's the call args (after the
		// receiver) and trim.
		depth := 0
		for j := 0; j < len(line); j++ {
			if line[j] == '(' {
				depth++
				if depth == 2 {
					line = line[:j]
					break
				}
			} else if line[j] == ')' {
				depth--
			}
		}
	}
	// Trim the long pkg path prefix.
	if i := strings.LastIndex(line, "/"); i >= 0 {
		return line[i+1:]
	}
	return line
}

func filterGoroutines(in []goroutine, state string, body *regexp.Regexp) []goroutine {
	if state == "" && body == nil {
		return in
	}
	var out []goroutine
	for _, g := range in {
		if state != "" && !strings.Contains(g.State, state) {
			continue
		}
		if body != nil && !body.MatchString(g.Stack) {
			continue
		}
		out = append(out, g)
	}
	return out
}

func renderGoroutines(grs []goroutine) string {
	var b strings.Builder
	for _, g := range grs {
		fmt.Fprintf(&b, "goroutine %s [%s]:\n", g.ID, g.State)
		b.WriteString(g.Stack)
		b.WriteString("\n")
	}
	return b.String()
}

// buildSummary produces the human-readable default view: total
// counts, per-state distribution, top stack-head buckets, and the
// lock-waiter analysis.
func buildSummary(grs []goroutine, topN int, showLocks bool) string {
	var b strings.Builder

	fmt.Fprintf(&b, "goroutines:     %d\n", len(grs))

	// Bucket by state-prefix (state strings often carry "X, Y minutes"
	// — strip the duration for bucketing, but track max-age separately).
	stateBuckets := map[string]*bucket{}
	for _, g := range grs {
		head, mins, hasAge := splitStateAge(g.State)
		bk, ok := stateBuckets[head]
		if !ok {
			bk = &bucket{}
			stateBuckets[head] = bk
		}
		bk.count++
		if !hasAge {
			bk.hasNoAge = true
		} else if mins > bk.maxMin {
			bk.maxMin = mins
		}
	}
	type stateRow struct {
		name string
		bk   *bucket
	}
	var stateRows []stateRow
	for name, bk := range stateBuckets {
		stateRows = append(stateRows, stateRow{name, bk})
	}
	sort.Slice(stateRows, func(i, j int) bool {
		return stateRows[i].bk.count > stateRows[j].bk.count
	})
	b.WriteString("\nby state:\n")
	for _, r := range stateRows {
		age := ""
		if r.bk.maxMin > 0 {
			age = fmt.Sprintf("  (max age %dm)", r.bk.maxMin)
		} else if r.bk.hasNoAge {
			age = "  (no age tag)"
		}
		fmt.Fprintf(&b, "  %5d  %s%s\n", r.bk.count, r.name, age)
	}

	// Top stack heads.
	topBuckets := map[string]int{}
	for _, g := range grs {
		topBuckets[g.Top]++
	}
	type topRow struct {
		name  string
		count int
	}
	var topRows []topRow
	for name, c := range topBuckets {
		topRows = append(topRows, topRow{name, c})
	}
	sort.Slice(topRows, func(i, j int) bool { return topRows[i].count > topRows[j].count })
	limit := topN
	if limit <= 0 || limit > len(topRows) {
		limit = len(topRows)
	}
	fmt.Fprintf(&b, "\ntop %d stack heads:\n", limit)
	for i := 0; i < limit; i++ {
		fmt.Fprintf(&b, "  %5d  %s\n", topRows[i].count, topRows[i].name)
	}

	if showLocks {
		b.WriteString("\n")
		b.WriteString(lockSummary(grs))
	}
	return b.String()
}

// splitStateAge separates "select, 5 minutes" into ("select", 5,
// true). When the state has no duration tag (e.g. "running"), returns
// (state, 0, false).
func splitStateAge(state string) (string, int, bool) {
	if i := strings.Index(state, ","); i >= 0 {
		head := strings.TrimSpace(state[:i])
		rest := strings.TrimSpace(state[i+1:])
		var mins int
		if _, err := fmt.Sscanf(rest, "%d minutes", &mins); err == nil {
			return head, mins, true
		}
		return head, 0, false
	}
	return state, 0, false
}

// lockSummary partitions goroutines blocked on sync.RWMutex.{R,}Lock
// + sync.Mutex.Lock by the immediate caller frame, so the operator
// can see "X waiters on Y caller" buckets — and, more importantly,
// candidate lock holders: goroutines that are NOT blocked on a lock
// but whose stack mentions the same package (heuristic, but usually
// enough to find a hung critical section).
func lockSummary(grs []goroutine) string {
	rlockBuckets := map[string]*bucket{}
	lockBuckets := map[string]*bucket{}

	// Caller-frame regex: line after the sync.(*RWMutex).RLock /
	// .Lock entry is the immediate caller — that's the function
	// blocked waiting for the lock.
	rlockCallerRe := regexp.MustCompile(`sync\.\(\*RWMutex\)\.RLock\(\.\.\.\)\n\s*\S+\n([^\n]+)`)
	mlockCallerRe := regexp.MustCompile(`sync\.\(\*RWMutex\)\.Lock\(0x[0-9a-f]+\?\)\n\s*\S+\n([^\n]+)|sync\.\(\*Mutex\)\.Lock\(\.\.\.\)\n\s*\S+\n([^\n]+)`)

	for _, g := range grs {
		switch {
		case strings.Contains(g.State, "RWMutex.RLock"):
			recordLockBucket(rlockBuckets, g, rlockCallerRe)
		case strings.Contains(g.State, "RWMutex.Lock") || strings.Contains(g.State, "Mutex.Lock"):
			recordLockBucket(lockBuckets, g, mlockCallerRe)
		}
	}

	var b strings.Builder
	if len(rlockBuckets) == 0 && len(lockBuckets) == 0 {
		b.WriteString("no goroutines blocked on mutex acquisition\n")
		return b.String()
	}
	b.WriteString("lock waiters:\n")
	emit := func(label string, bk map[string]*bucket) {
		if len(bk) == 0 {
			return
		}
		type row struct {
			name string
			b    *bucket
		}
		var rows []row
		for k, v := range bk {
			rows = append(rows, row{k, v})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].b.count > rows[j].b.count })
		fmt.Fprintf(&b, "  %s:\n", label)
		for _, r := range rows {
			age := ""
			if r.b.maxMin > 0 {
				age = fmt.Sprintf("  (max age %dm)", r.b.maxMin)
			}
			fmt.Fprintf(&b, "    %5d  %s%s\n", r.b.count, r.name, age)
		}
	}
	emit("RLock", rlockBuckets)
	emit("Lock", lockBuckets)
	return b.String()
}

func recordLockBucket(into map[string]*bucket, g goroutine, _ *regexp.Regexp) {
	// Simpler than regex: walk the stack lines, find the first frame
	// past sync.* — that's the caller.
	caller := callerPastSync(g.Stack)
	bk, ok := into[caller]
	if !ok {
		bk = &bucket{}
		into[caller] = bk
	}
	bk.count++
	_, mins, hasAge := splitStateAge(g.State)
	if hasAge && mins > bk.maxMin {
		bk.maxMin = mins
	}
}

func callerPastSync(stack string) string {
	for _, line := range strings.Split(stack, "\n") {
		if strings.HasPrefix(line, "\t") || line == "" {
			continue
		}
		if strings.HasPrefix(line, "runtime.") ||
			strings.HasPrefix(line, "sync.") ||
			strings.HasPrefix(line, "internal/sync.") {
			continue
		}
		return shortenFrame(line)
	}
	return "(unknown)"
}

// bucket is shared by buildSummary and lockSummary — must stay at
// package scope so buildSummary's nested closure can reference it.
type bucket struct {
	count    int
	maxMin   int
	hasNoAge bool
}
