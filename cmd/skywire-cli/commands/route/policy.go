// Package cliroute cmd/skywire-cli/commands/route/policy.go —
// operator tooling for the Starlark routing-policy DSL
// (RFC #2882). `skywire cli route policy test` previews what a
// script would decide for a synthetic dial; `bench` runs the
// script 1M times and reports per-call latency.
package cliroute

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/router/policy"
)

var (
	policyScriptPath string
	policyDialJSON   string
	policyBenchIter  int
)

func init() {
	routeCmd.AddCommand(policyCmd)
	policyCmd.AddCommand(policyTestCmd, policyBenchCmd)

	policyTestCmd.Flags().StringVarP(&policyScriptPath, "script", "s", "",
		"path to the Starlark policy file (.star)")
	policyTestCmd.Flags().StringVarP(&policyDialJSON, "dial", "d", "{}",
		"synthetic dial context as JSON — see the docs for the schema")

	policyBenchCmd.Flags().StringVarP(&policyScriptPath, "script", "s", "",
		"path to the Starlark policy file (.star)")
	policyBenchCmd.Flags().IntVarP(&policyBenchIter, "iter", "n", 100_000,
		"number of evaluations to run")
}

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Routing-policy DSL tooling (RFC #2882)",
	Long: `Operator tooling for the per-dial routing policy:

  test    Preview what a policy would decide for a synthetic dial
          context. No actual dial, no visor RPC, no router state —
          purely deterministic evaluation of the script.

  bench   Run the policy many times and report per-call latency
          (p50/avg). Use this before deploying a complex policy to
          confirm it stays inside the per-dial timeout budget.

See docs/routing_policy_rfc.md for the policy DSL design and
docs/examples/routing-policies/ for example scripts.`,
}

var policyTestCmd = &cobra.Command{
	Use:   "test",
	Short: "Preview a policy's decision for a synthetic dial context",
	Long: `Loads the supplied script, builds a RoutingContext from the
--dial JSON, evaluates decide_route, and prints the returned
RouteSpec as JSON.

Example:

  skywire cli route policy test \
    --script docs/examples/routing-policies/friday-id.star \
    --dial '{"app":"vpn-client","peer_pk":"abc","now":"2026-05-29T17:00:00Z"}'

The --dial JSON accepts: app (string), peer_pk (string), port
(int), now (RFC3339 timestamp). Missing fields default to zero
values.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if policyScriptPath == "" {
			return errors.New("--script is required")
		}
		src, err := os.ReadFile(policyScriptPath) //nolint:gosec
		if err != nil {
			return fmt.Errorf("read script: %w", err)
		}
		var dial dialJSON
		if err := json.Unmarshal([]byte(policyDialJSON), &dial); err != nil {
			return fmt.Errorf("parse --dial JSON: %w", err)
		}
		rctx, candidates, err := dial.toContext()
		if err != nil {
			return err
		}
		// Clock fixed to the dial's `now` (or system time if
		// unset) so the test result is deterministic regardless
		// of when the operator runs the command.
		clock := fixedClock{t: rctx.Now}
		eval, err := policy.NewEvaluator(policyScriptPath, string(src),
			policy.WithClock(clock),
			policy.WithFailureMode(policy.FailureDrop),
			policy.WithLogger(func(format string, args ...interface{}) {
				fmt.Fprintf(os.Stderr, "[policy log] "+format+"\n", args...)
			}),
		)
		if err != nil {
			return fmt.Errorf("load policy: %w", err)
		}
		start := time.Now()
		spec, err := eval.Decide(context.Background(), rctx, candidates)
		elapsed := time.Since(start)
		if err != nil {
			return fmt.Errorf("decide_route returned error: %w", err)
		}
		out, _ := json.MarshalIndent(specToJSON(spec), "", "  ") //nolint:errcheck
		fmt.Println(string(out))
		fmt.Fprintf(os.Stderr, "\nelapsed: %s\n", elapsed)
		return nil
	},
}

var policyBenchCmd = &cobra.Command{
	Use:   "bench",
	Short: "Benchmark a policy's per-call evaluation time",
	Long: `Runs the policy N times against a synthetic context and
reports p50 + average per-call eval time. Use this before
deploying a complex policy to confirm it stays inside the
50ms per-dial timeout budget.`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if policyScriptPath == "" {
			return errors.New("--script is required")
		}
		src, err := os.ReadFile(policyScriptPath) //nolint:gosec
		if err != nil {
			return fmt.Errorf("read script: %w", err)
		}
		eval, err := policy.NewEvaluator(policyScriptPath, string(src))
		if err != nil {
			return fmt.Errorf("load policy: %w", err)
		}
		rctx := policy.RoutingContext{App: "bench", PeerPK: "synth"}
		var candidates []policy.Candidate
		// Warmup: discount JIT-class effects.
		for i := 0; i < 100; i++ {
			_, _ = eval.Decide(context.Background(), rctx, candidates) //nolint:errcheck
		}
		samples := make([]time.Duration, policyBenchIter)
		start := time.Now()
		for i := 0; i < policyBenchIter; i++ {
			t0 := time.Now()
			_, _ = eval.Decide(context.Background(), rctx, candidates) //nolint:errcheck
			samples[i] = time.Since(t0)
		}
		total := time.Since(start)
		avg := total / time.Duration(policyBenchIter)
		p50 := percentile(samples, 50)
		p99 := percentile(samples, 99)
		fmt.Printf("script:  %s\n", policyScriptPath)
		fmt.Printf("iters:   %d\n", policyBenchIter)
		fmt.Printf("total:   %s\n", total)
		fmt.Printf("avg:     %s\n", avg)
		fmt.Printf("p50:     %s\n", p50)
		fmt.Printf("p99:     %s\n", p99)
		budget := 50 * time.Millisecond
		if p99 > budget {
			fmt.Fprintf(os.Stderr, "\nWARNING: p99 (%s) exceeds the 50ms per-dial budget.\n", p99)
		} else if p99 > budget/10 {
			fmt.Fprintf(os.Stderr, "\nnote: p99 (%s) is within budget but >10%% of it; complex policies may push over.\n", p99)
		}
		return nil
	},
}

// dialJSON is the schema the --dial flag accepts.
type dialJSON struct {
	App    string `json:"app"`
	PeerPK string `json:"peer_pk"`
	Port   uint16 `json:"port"`
	Now    string `json:"now"`
	Friday bool   `json:"friday,omitempty"` // convenience for "set now to a Friday at the given hour"
	Hour   int    `json:"hour,omitempty"`
	// Candidates omitted: this is a preview tool; candidates are
	// modeled separately. Operators who want to test with a
	// specific candidate set should call the bench command which
	// uses empty candidates.
}

func (d dialJSON) toContext() (policy.RoutingContext, []policy.Candidate, error) {
	rctx := policy.RoutingContext{
		App:    d.App,
		PeerPK: d.PeerPK,
		Port:   d.Port,
	}
	if d.Now != "" {
		t, err := time.Parse(time.RFC3339, d.Now)
		if err != nil {
			return rctx, nil, fmt.Errorf("parse now: %w", err)
		}
		rctx.Now = t
	} else if d.Friday {
		// Convenience: set to a known Friday at the given hour.
		// 2026-05-29 is a Friday in UTC.
		hour := d.Hour
		rctx.Now = time.Date(2026, 5, 29, hour, 0, 0, 0, time.UTC)
	} else {
		rctx.Now = time.Now()
	}
	return rctx, nil, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// specToJSON projects RouteSpec into a JSON-printable form. The
// Chosen field uses a *Candidate; we flatten it for display.
type specJSON struct {
	Chosen       *policy.Candidate `json:"chosen,omitempty"`
	Mux          int               `json:"mux"`
	MinHops      int               `json:"min_hops"`
	Fallback     string            `json:"fallback,omitempty"`
	Distribution string            `json:"distribution,omitempty"`
}

func specToJSON(s policy.RouteSpec) specJSON {
	return specJSON{
		Chosen:       s.Chosen,
		Mux:          s.Mux,
		MinHops:      s.MinHops,
		Fallback:     s.Fallback,
		Distribution: s.Distribution,
	}
}

// percentile returns the p-th percentile of an unsorted sample.
// Simple sort-then-index; for our N (100k) this is well under a
// second even on ARM. We could use quickselect for tighter loops
// but the CLI tool runs once.
func percentile(samples []time.Duration, p int) time.Duration {
	if len(samples) == 0 {
		return 0
	}
	// In-place sort.
	for i := 1; i < len(samples); i++ {
		j := i
		for j > 0 && samples[j-1] > samples[j] {
			samples[j-1], samples[j] = samples[j], samples[j-1]
			j--
		}
	}
	idx := (len(samples) * p) / 100
	if idx >= len(samples) {
		idx = len(samples) - 1
	}
	return samples[idx]
}

// Silence "unused import" if strings isn't reached above (template).
var _ = strings.TrimSpace
