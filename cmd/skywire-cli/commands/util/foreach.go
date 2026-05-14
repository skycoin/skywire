// Package cliutil cmd/skywire-cli/commands/util/foreach.go: `cli util
// foreach` — run a templated shell command against a list of public
// keys (or arbitrary tokens) in parallel and print collated output
// with per-target headers.
//
// Today operators dispatching the same probe / config check across
// the visor fleet write a shell loop:
//
//	for pk in $(cat peers.txt); do
//	  echo "=== $pk ==="
//	  skywire cli dmsg ping --dmsg $pk
//	done
//
// foreach folds that into one CLI:
//
//	skywire cli util foreach @peers.txt 'skywire cli dmsg ping --dmsg {pk}'
//
// Behaviorally equivalent to GNU parallel's most common use case
// — but bundled with the skywire CLI so an operator on a fresh
// box doesn't need to install parallel first, and we can layer
// skywire-aware features (skychat alias resolution, future PK
// validation) on top without forking parallel.
package cliutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

var (
	foreachParallel int
	foreachTimeout  time.Duration
	foreachShell    string
	foreachStopOn   string
	foreachQuiet    bool
)

func init() {
	foreachCmd.Flags().SortFlags = false
	foreachCmd.Flags().IntVarP(&foreachParallel, "parallel", "p", 4,
		"max concurrent processes")
	foreachCmd.Flags().DurationVarP(&foreachTimeout, "timeout", "t", 60*time.Second,
		"per-target timeout — kills the subprocess when exceeded")
	foreachCmd.Flags().StringVar(&foreachShell, "shell", "/bin/sh",
		"shell binary used to run each expanded command (sh -c <cmd>)")
	foreachCmd.Flags().StringVar(&foreachStopOn, "stop-on", "",
		"stop dispatching new work when an earlier target's process matches: 'never' (default), 'first-error' (any non-zero exit), 'first-success' (zero exit)")
	foreachCmd.Flags().BoolVarP(&foreachQuiet, "quiet", "q", false,
		"suppress per-target header lines; emit only the subprocess output (useful for | downstream consumers)")
	RootCmd.AddCommand(foreachCmd)
}

var foreachCmd = &cobra.Command{
	Use:   "foreach <targets> <command-template>",
	Short: "Run a templated shell command against each target in parallel",
	Long: `Run a shell command against each target — public key, alias, or
arbitrary token — substituting placeholders in the command template.

Placeholders in the command template:
  {pk}    the current target string verbatim
  {i}     the target's 0-indexed position in the input list

Targets input forms:
  - Comma-separated:   '02f9...,03d1...,0323...'
  - File:              '@peers.txt'  (one target per line; '#' comments OK)
  - Mixed:             '02f9...,@more.txt,0323...'

Examples:
  cli util foreach '02f9...,03d1...' 'cli dmsg ping --dmsg {pk}'
  cli util foreach @peers.txt 'cli visor doctor --rpc {pk}:3435'
  cli util foreach @peers.txt 'cli dmsg probe {pk} 80' --parallel 16
  cli util foreach 02f9... 'cli skychat send -t {pk} -m hi'

Output (default, headers on):
  === [0] 02f9...cd6c ===
  <subprocess stdout/stderr>
  exit=0 dur=42ms

Output with --quiet: only subprocess stdout/stderr, no headers,
exit-status footer.

--json: emits an NDJSON record per target with id/target/stdout/
stderr/exit_code/duration_ms fields. Useful for downstream
log-aggregation.

The subprocess is launched as: <shell> -c <expanded-command>
so shell features (pipes, redirections, env) all work as expected.

Exit codes:
  0 — all targets exited 0
  1 — at least one target exited non-zero
  2 — one or more targets timed out`,
	Args:                  cobra.ExactArgs(2),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		targets, err := expandTargets(args[0])
		if err != nil {
			return err
		}
		if len(targets) == 0 {
			return errors.New("foreach: target list is empty")
		}
		template := args[1]
		if !strings.Contains(template, "{pk}") && !strings.Contains(template, "{i}") {
			fmt.Fprintln(os.Stderr,
				"warning: command template contains no {pk}/{i} placeholders — every target will run the same command")
		}

		jsonMode, _ := cmd.Flags().GetBool("json") //nolint:errcheck
		results := runForeach(cmd.Context(), targets, template)

		anyNonZero, anyTimeout := false, false
		for _, r := range results {
			if r.TimedOut {
				anyTimeout = true
			}
			if r.ExitCode != 0 {
				anyNonZero = true
			}
		}

		if jsonMode {
			enc := json.NewEncoder(os.Stdout)
			for _, r := range results {
				_ = enc.Encode(r) //nolint:errcheck
			}
		}
		// Non-JSON output was already streamed by runForeach. Just
		// emit the exit code.
		if anyTimeout {
			os.Exit(2)
		}
		if anyNonZero {
			os.Exit(1)
		}
		return nil
	},
}

// foreachResult captures the per-target run shape. The fields are
// what the JSON output emits, in the order they're populated.
type foreachResult struct {
	Index      int    `json:"index"`
	Target     string `json:"target"`
	Stdout     string `json:"stdout"`
	Stderr     string `json:"stderr"`
	ExitCode   int    `json:"exit_code"`
	DurationMS int64  `json:"duration_ms"`
	TimedOut   bool   `json:"timed_out,omitempty"`
}

// runForeach fans out the per-target work and streams headered
// output as each subprocess completes. Capped at foreachParallel
// concurrent processes. Results are collated in input-order for the
// final return slice; streaming output isn't necessarily in input
// order (it's in completion order, like xargs -P with --line-buffered).
func runForeach(ctx context.Context, targets []string, template string) []foreachResult {
	if ctx == nil {
		ctx = context.Background()
	}
	type tagged struct {
		i   int
		out foreachResult
	}
	in := make(chan tagged, len(targets))
	resultsMu := sync.Mutex{}
	results := make([]foreachResult, len(targets))
	stopCh := make(chan struct{})
	stopOnce := sync.Once{}
	requestStop := func() { stopOnce.Do(func() { close(stopCh) }) }

	for i, t := range targets {
		in <- tagged{i: i, out: foreachResult{Index: i, Target: t}}
	}
	close(in)

	var wg sync.WaitGroup
	sem := make(chan struct{}, foreachParallel)
	for work := range in {
		work := work
		select {
		case <-stopCh:
			// Stop dispatching; record skipped targets as exit=-1
			// (sentinel "did not run") so the consumer sees them.
			resultsMu.Lock()
			results[work.i] = foreachResult{Index: work.i, Target: work.out.Target, ExitCode: -1, Stderr: "skipped (stop-on triggered)"}
			resultsMu.Unlock()
			continue
		default:
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			r := runOne(ctx, work.out.Target, work.i, template)
			resultsMu.Lock()
			results[work.i] = r
			resultsMu.Unlock()
			if !foreachQuiet {
				fmt.Printf("=== [%d] %s ===\n", r.Index, shortTarget(r.Target))
				if r.Stdout != "" {
					fmt.Print(r.Stdout)
					if !strings.HasSuffix(r.Stdout, "\n") {
						fmt.Println()
					}
				}
				if r.Stderr != "" {
					fmt.Fprint(os.Stderr, r.Stderr)
					if !strings.HasSuffix(r.Stderr, "\n") {
						fmt.Fprintln(os.Stderr)
					}
				}
				status := fmt.Sprintf("exit=%d dur=%s", r.ExitCode, time.Duration(r.DurationMS)*time.Millisecond)
				if r.TimedOut {
					status = "timed-out " + status
				}
				fmt.Println(status)
			}
			// Stop-on triggers.
			switch foreachStopOn {
			case "first-error":
				if r.ExitCode != 0 {
					requestStop()
				}
			case "first-success":
				if r.ExitCode == 0 {
					requestStop()
				}
			}
		}()
	}
	wg.Wait()
	return results
}

// runOne executes one templated subprocess for the given target.
// The subprocess gets a per-call context wrapping foreachTimeout so
// a hung command can't pin the worker forever.
func runOne(parent context.Context, target string, idx int, template string) foreachResult {
	r := foreachResult{Index: idx, Target: target}
	expanded := strings.ReplaceAll(template, "{pk}", target)
	expanded = strings.ReplaceAll(expanded, "{i}", fmt.Sprintf("%d", idx))

	ctx, cancel := context.WithTimeout(parent, foreachTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, foreachShell, "-c", expanded) //nolint:gosec // foreach's purpose is to run an operator-supplied command per target
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	start := time.Now()
	err := cmd.Run()
	r.DurationMS = time.Since(start).Milliseconds()
	r.Stdout = stdout.String()
	r.Stderr = stderr.String()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			r.TimedOut = true
			r.ExitCode = -1
			if r.Stderr == "" {
				r.Stderr = fmt.Sprintf("timed out after %s\n", foreachTimeout)
			}
			return r
		}
		// exec.ExitError carries the child's exit code.
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			r.ExitCode = ee.ExitCode()
		} else {
			r.ExitCode = -1
			r.Stderr += err.Error() + "\n"
		}
	}
	return r
}

// expandTargets resolves the input spec into a flat list of target
// tokens. Comma-separated entries are split; '@file' entries are
// read line-by-line; lines starting with '#' and empty lines are
// skipped. Whitespace around each token is trimmed.
func expandTargets(spec string) ([]string, error) {
	out := make([]string, 0, 8)
	for _, raw := range strings.Split(spec, ",") {
		tok := strings.TrimSpace(raw)
		if tok == "" {
			continue
		}
		if strings.HasPrefix(tok, "@") {
			f, err := os.Open(tok[1:])
			if err != nil {
				return nil, fmt.Errorf("foreach: open %q: %w", tok[1:], err)
			}
			body, rerr := io.ReadAll(f)
			_ = f.Close() //nolint:errcheck
			if rerr != nil {
				return nil, fmt.Errorf("foreach: read %q: %w", tok[1:], rerr)
			}
			for _, line := range strings.Split(string(body), "\n") {
				l := strings.TrimSpace(line)
				if l == "" || strings.HasPrefix(l, "#") {
					continue
				}
				out = append(out, l)
			}
			continue
		}
		out = append(out, tok)
	}
	return out, nil
}

// shortTarget shortens a 66-char hex PK for the header line.
// Anything that's not 66 chars passes through verbatim (operator
// might be passing an alias or some other token).
func shortTarget(t string) string {
	if len(t) == 66 {
		return t[:6] + "…" + t[len(t)-6:]
	}
	return t
}
