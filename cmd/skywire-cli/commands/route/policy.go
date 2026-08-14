// Package cliroute cmd/skywire-cli/commands/route/policy.go c4-vis-cli
// operator tooling for the skylark routing-policy DSL
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
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/cliroute"
	"github.com/skycoin/skywire/pkg/router/policy"
	"github.com/skycoin/skywire/pkg/router/policy/presets"
	policywasm "github.com/skycoin/skywire/pkg/router/policy/wasm"
)

// policyTestEngine is the minimal surface `route policy test` / `bench`
// need from either policy backend.
type policyTestEngine interface {
	Decide(ctx context.Context, rctx policy.RoutingContext, candidates []policy.Candidate) (policy.RouteSpec, error)
}

// loadPolicyEngine dispatches by file extension — ".wasm" loads the
// WASM backend (pkg/router/policy/wasm), anything else loads Starlark —
// mirroring the visor's SetAppRoutingPolicy dispatch so `route policy
// test` previews the SAME backend the visor would actually run. Before
// this, both subcommands fed the file to the Starlark evaluator
// unconditionally, so testing a .wasm policy failed with "missing
// decide_route function" (the wasm bytes don't parse as Starlark).
func loadPolicyEngine(path string, src []byte, clock fixedClock) (policyTestEngine, error) {
	logger := func(format string, args ...interface{}) {
		fmt.Fprintf(os.Stderr, "[policy log] "+format+"\n", args...)
	}
	if strings.EqualFold(filepath.Ext(path), ".wasm") {
		return policywasm.NewEvaluator(path, src,
			policywasm.WithClock(clock),
			policywasm.WithLogger(logger),
		)
	}
	return policy.NewEvaluator(path, string(src),
		policy.WithClock(clock),
		policy.WithFailureMode(policy.FailureDrop),
		policy.WithLogger(logger),
	)
}

var (
	policyScriptPath string
	policyPresetName string
	policyDialJSON   string
	policyBenchIter  int
)

func init() {
	routeCmd.AddCommand(policyCmd)
	policyCmd.AddCommand(policyListCmd, policyShowCmd, policyTestCmd, policyBenchCmd)

	policyTestCmd.Flags().StringVarP(&policyScriptPath, "script", "s", "",
		"path to the skylark policy file (.star)")
	policyTestCmd.Flags().StringVarP(&policyPresetName, "preset", "p", "",
		"built-in preset name to test instead of a --script file (see `route policy list`)")
	policyTestCmd.Flags().StringVarP(&policyDialJSON, "dial", "d", "{}",
		"synthetic dial context as JSON — see the docs for the schema")

	policyBenchCmd.Flags().StringVarP(&policyScriptPath, "script", "s", "",
		"path to the skylark policy file (.star)")
	policyBenchCmd.Flags().StringVarP(&policyPresetName, "preset", "p", "",
		"built-in preset name to benchmark instead of a --script file")
	policyBenchCmd.Flags().IntVarP(&policyBenchIter, "iter", "n", 100_000,
		"number of evaluations to run")
}

// presetSummary returns the first non-empty comment line of a preset's
// source — its human-readable one-liner. Empty when the preset is unknown
// or has no leading comment.
func presetSummary(name string) string {
	src, ok := presets.Source(name)
	if !ok {
		return ""
	}
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			if desc := strings.TrimSpace(strings.TrimPrefix(line, "#")); desc != "" {
				return desc
			}
			continue
		}
		if line != "" {
			break // hit code before any comment
		}
	}
	return ""
}

var policyListCmd = &cobra.Command{
	Use:   "list",
	Short: "List the built-in routing-policy presets",
	Long: `Lists the presets embedded in the binary. Select one without
compiling a file via the config value:

  "routing": { "policy_per_dial": "preset:<name>" }

Preview one with ` + "`route policy test --preset <name>`" + ` or read its
source with ` + "`route policy show <name>`" + `.`,
	RunE: func(_ *cobra.Command, _ []string) error {
		for _, name := range presets.Names() {
			if s := presetSummary(name); s != "" {
				fmt.Printf("%-20s %s\n", name, s)
			} else {
				fmt.Println(name)
			}
		}
		return nil
	},
}

var policyShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Print a built-in preset's Starlark source",
	Args:  cobra.ExactArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		src, ok := presets.Source(args[0])
		if !ok {
			return fmt.Errorf("unknown preset %q; available: %s", args[0], strings.Join(presets.Names(), ", "))
		}
		fmt.Print(src)
		return nil
	},
}

// resolvePolicySource returns (path, source) for whichever of --preset or
// --script the operator supplied, preferring --preset. The returned path is
// a display/dispatch label (preset:<name> is treated as Starlark). Errors
// when neither or both are given.
func resolvePolicySource() (string, []byte, error) {
	switch {
	case policyPresetName != "" && policyScriptPath != "":
		return "", nil, errors.New("pass only one of --preset or --script")
	case policyPresetName != "":
		src, ok := presets.Source(policyPresetName)
		if !ok {
			return "", nil, fmt.Errorf("unknown preset %q; available: %s", policyPresetName, strings.Join(presets.Names(), ", "))
		}
		return "preset:" + policyPresetName + ".star", []byte(src), nil
	case policyScriptPath != "":
		src, err := os.ReadFile(policyScriptPath) //nolint:gosec
		if err != nil {
			return "", nil, fmt.Errorf("read script: %w", err)
		}
		return policyScriptPath, src, nil
	default:
		return "", nil, errors.New("one of --preset or --script is required")
	}
}

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "skylark routing-policy tooling (RFC #2882)",
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
		path, src, err := resolvePolicySource()
		if err != nil {
			return err
		}
		var dial dialJSON
		if err := json.Unmarshal([]byte(policyDialJSON), &dial); err != nil {
			return fmt.Errorf("parse --dial JSON: %w", err)
		}
		rctx, err := dial.toContext()
		if err != nil {
			return err
		}
		var candidates []policy.Candidate
		// Clock fixed to the dial's `now` (or system time if
		// unset) so the test result is deterministic regardless
		// of when the operator runs the command.
		clock := fixedClock{t: rctx.Now}
		eval, err := loadPolicyEngine(path, src, clock)
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
		path, src, err := resolvePolicySource()
		if err != nil {
			return err
		}
		eval, err := loadPolicyEngine(path, src, fixedClock{t: time.Now()})
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
		if err := cliout.Print(cmd, cliroute.PolicyBench{
			Script: path, Iterations: policyBenchIter,
			Total: total.String(), Average: avg.String(),
			P50: p50.String(), P99: p99.String(),
		}); err != nil {
			return err
		}
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

func (d dialJSON) toContext() (policy.RoutingContext, error) {
	rctx := policy.RoutingContext{
		App:    d.App,
		PeerPK: d.PeerPK,
		Port:   d.Port,
	}
	if d.Now != "" {
		t, err := time.Parse(time.RFC3339, d.Now)
		if err != nil {
			return rctx, fmt.Errorf("parse now: %w", err)
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
	return rctx, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

// specToJSON projects RouteSpec into a JSON-printable form. The
// Chosen field uses a *Candidate; we flatten it for display.
type specJSON struct {
	Chosen         *policy.Candidate `json:"chosen,omitempty"`
	ReverseChosen  *policy.Candidate `json:"reverse_chosen,omitempty"`
	Mux            int               `json:"mux"`
	ForwardMux     int               `json:"forward_mux,omitempty"`
	ReverseMux     int               `json:"reverse_mux,omitempty"`
	MinHops        int               `json:"min_hops"`
	ForwardMinHops int               `json:"forward_min_hops,omitempty"`
	ReverseMinHops int               `json:"reverse_min_hops,omitempty"`
	Fallback       string            `json:"fallback,omitempty"`
	Distribution   string            `json:"distribution,omitempty"`
}

func specToJSON(s policy.RouteSpec) specJSON {
	return specJSON{
		Chosen:         s.Chosen,
		ReverseChosen:  s.ReverseChosen,
		Mux:            s.Mux,
		ForwardMux:     s.ForwardMux,
		ReverseMux:     s.ReverseMux,
		MinHops:        s.MinHops,
		ForwardMinHops: s.ForwardMinHops,
		ReverseMinHops: s.ReverseMinHops,
		Fallback:       s.Fallback,
		Distribution:   s.Distribution,
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
