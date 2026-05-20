// Command treeprobe consumes an NDJSON event stream from
// `cli visor ping tree-stream` on stdin and writes CSV rows to
// stdout, one per (level, parent_pk, remote_pk) cell. Also writes
// a summary line to stderr.
//
// Driver shape:
//
//	skywire cli visor ping tree-stream --hops 1 --tries 5 \
//	  | treeprobe > /tmp/measure-hops1-tries5.csv
//
// This binary is intentionally minimal — all logic lives in
// pkg/util/treeprobe, which has the unit tests. main.go is the
// io wiring.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/skycoin/skywire/pkg/util/treeprobe"
)

func main() {
	p := treeprobe.NewParser(os.Stdin)
	a := treeprobe.NewAggregator()

	var events int
	for {
		d, err := p.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "treeprobe: parse: %v\n", err)
			os.Exit(1)
		}
		a.Observe(d)
		events++
	}

	rows, err := treeprobe.WriteCSV(os.Stdout, a)
	if err != nil {
		fmt.Fprintf(os.Stderr, "treeprobe: write csv: %v\n", err)
		os.Exit(1)
	}

	// Summary on stderr — the driver can grep it without polluting
	// the CSV stdout that downstream tools consume.
	run := a.RunDone()
	fmt.Fprintf(os.Stderr,
		"treeprobe: events=%d cells=%d levels=%d cache_hit_rate=%.2f%%",
		events, rows, len(a.Levels()), 100*a.CacheHitRate())
	if run != nil {
		fmt.Fprintf(os.Stderr,
			" total_discovered=%d total_pinged=%d total_succeeded=%d total_failed=%d wall_time_ms=%.0f termination=%s",
			run.TotalDiscovered, run.TotalPinged, run.TotalSucceeded, run.TotalFailed,
			float64(int64(run.WallTimeNs))/1e6, run.TerminationReason)
	}
	if e := a.ServerError(); e != nil {
		fmt.Fprintf(os.Stderr, " SERVER_ERROR(%s): %s", e.Code, e.Message)
	}
	fmt.Fprintln(os.Stderr)
}
