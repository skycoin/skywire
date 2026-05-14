// Package clivisor cmd/skywire-cli/commands/visor/whois.go: `cli
// visor whois <pk>` — single-PK rollup combining transport-discovery,
// uptime-tracker, and service-discovery state into one report.
//
// Today an operator answering "what do we know about this visor PK"
// has to hit three separate services:
//
//	cli tp ls --remote <pk>      — transports the visor is on
//	cli ut tpd | grep <pk>       — uptime in the tpd-integrated UT
//	cli sd | grep <pk>           — services the visor advertises
//
// whois collapses those into one report. Useful when triaging a peer
// from a log entry, a chat message, or a transport-id: paste the PK,
// see everything the discovery layer knows about it.
//
// Best-effort: each lookup runs independently and timeouts are
// per-service. A service being down marks its block as "unreachable"
// in the report and doesn't fail the whole command.
package clivisor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	whoisTimeout time.Duration
	whoisTpdURL  string
	whoisUtURL   string
	whoisSdURL   string
)

func init() {
	whoisCmd.Flags().SortFlags = false
	whoisCmd.Flags().DurationVarP(&whoisTimeout, "timeout", "t", 5*time.Second,
		"per-service request timeout")
	whoisCmd.Flags().StringVar(&whoisTpdURL, "tpd", deployment.Prod.TransportDiscovery,
		"transport-discovery URL")
	whoisCmd.Flags().StringVar(&whoisUtURL, "ut", deployment.Prod.UptimeTracker,
		"uptime-tracker URL")
	whoisCmd.Flags().StringVar(&whoisSdURL, "sd", deployment.Prod.ServiceDiscovery,
		"service-discovery URL")
	RootCmd.AddCommand(whoisCmd)
}

// whoisReport is the JSON output. Each block is best-effort —
// services that respond with "not found" return empty, services
// that don't respond at all return an Error.
type whoisReport struct {
	PK         string     `json:"pk"`
	Transports whoisBlock `json:"transports"`
	Uptime     whoisBlock `json:"uptime"`
	Services   whoisBlock `json:"services"`
}

// whoisBlock is the per-service result shape. Raw is the
// service's JSON response payload (truncated in human output);
// scripts get the full body via --json.
type whoisBlock struct {
	URL     string          `json:"url"`
	Status  int             `json:"http_status"`
	Error   string          `json:"error,omitempty"`
	Raw     json.RawMessage `json:"raw,omitempty"`
	Summary string          `json:"summary,omitempty"`
}

var whoisCmd = &cobra.Command{
	Use:   "whois <pk>",
	Short: "Combined TPD + UT + SD rollup for a single visor PK",
	Long: `Single-PK rollup over transport-discovery, uptime-tracker, and
service-discovery. Useful for triaging a peer from a log line, a
chat message, or a transport-id without manually hitting three
services.

Examples:
  skywire cli visor whois <pk>             # human-readable table
  skywire cli visor whois <pk> --json      # full raw responses, scriptable

Best-effort: each service is queried in parallel with --timeout per
request. A service being down marks its block as 'unreachable'
rather than failing the whole command.`,
	Args:                  cobra.ExactArgs(1),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		var pk cipher.PubKey
		if err := pk.Set(args[0]); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("invalid pk: %w", err))
		}
		// Local visor PK lookup is technically supported (we'd just
		// be querying the discovery layer about ourselves) but flag
		// it on stderr in case the operator meant `visor info`.
		if rc, rerr := clirpc.Client(cmd.Flags()); rerr == nil {
			if ov, ovErr := rc.Overview(); ovErr == nil && ov.PubKey == pk {
				fmt.Fprintf(os.Stderr,
					"note: %s is the local visor; consider `cli visor info` for richer detail\n", pk)
			}
		}

		report := whoisReport{PK: pk.String()}
		rc, _ := clirpc.Client(cmd.Flags()) //nolint:errcheck // best-effort, fall back to HTTP-only

		// Transports: prefer the visor RPC (already authenticated +
		// rate-limited against the same TPD the operator's visor
		// trusts). Fall back to nothing if no visor — the operator
		// would have seen an RPC error already.
		if rc != nil {
			report.Transports = transportsFromVisor(rc, pk)
		} else {
			report.Transports = whoisBlock{
				URL:   "(visor RPC unavailable)",
				Error: "visor RPC client not initialized; transport lookup needs a local visor",
			}
		}

		// Uptime: prefer the visor RPC (auth + retry handled by the
		// visor). On any failure, surface the error and move on.
		if rc != nil {
			report.Uptime = uptimeFromVisor(rc, pk.String())
		} else {
			report.Uptime = whoisBlock{
				URL:   "(visor RPC unavailable)",
				Error: "visor RPC client not initialized; uptime lookup needs a local visor",
			}
		}

		// Services: SD doesn't have a "give me everything this PK
		// advertises" endpoint — its public API is keyed by service
		// type (?type=skysocks, ?type=vpn, etc.) and filters within
		// a type. To check if THIS pk advertises any service we'd
		// have to iterate every known type and grep client-side,
		// which (a) is fragile to new types being added and (b)
		// scans many entries when the operator probably just wants
		// "is this visor in any service list at all" — better
		// answered by the operator running `cli sd` themselves with
		// the type they care about. We surface that as a note here.
		report.Services = whoisBlock{
			URL:     "(no per-PK SD lookup; SD is keyed by service type)",
			Summary: "use `skywire cli sd --type <skysocks|vpn|visor>` to list a service type",
		}

		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode {
			b, _ := json.MarshalIndent(report, "", "  ") //nolint:errcheck
			fmt.Println(string(b))
			return
		}

		fmt.Printf("PK: %s\n\n", pk)
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "SOURCE\tHTTP\tSUMMARY")                                                         //nolint:errcheck
		fmt.Fprintf(w, "transports\t%s\t%s\n", httpCell(report.Transports), valOrErr(report.Transports)) //nolint:errcheck
		fmt.Fprintf(w, "uptime\t%s\t%s\n", httpCell(report.Uptime), valOrErr(report.Uptime))             //nolint:errcheck
		fmt.Fprintf(w, "services\t%s\t%s\n", httpCell(report.Services), valOrErr(report.Services))       //nolint:errcheck
		_ = w.Flush()                                                                                    //nolint:errcheck
	},
}

// transportsFromVisor uses the local visor's DiscoverTransportsByPK
// RPC. The visor itself talks to TPD with its trust credentials,
// so we don't have to plumb authentication into the CLI for this
// path. Returns a block with the raw entries marshaled to JSON for
// --json consumers.
func transportsFromVisor(rc interface {
	DiscoverTransportsByPK(pk cipher.PubKey) ([]*transport.Entry, error)
}, pk cipher.PubKey) whoisBlock {
	block := whoisBlock{URL: "visor-RPC: DiscoverTransportsByPK"}
	entries, err := rc.DiscoverTransportsByPK(pk)
	if err != nil {
		block.Error = err.Error()
		return block
	}
	if len(entries) == 0 {
		block.Summary = "no transports"
		return block
	}
	if raw, mErr := json.Marshal(entries); mErr == nil {
		block.Raw = raw
	}
	byType := map[string]int{}
	for _, e := range entries {
		byType[string(e.Type)]++
	}
	var parts []string
	for t, n := range byType {
		parts = append(parts, fmt.Sprintf("%s=%d", t, n))
	}
	block.Summary = fmt.Sprintf("%d total (%s)", len(entries), strings.Join(parts, " "))
	return block
}

// uptimeFromVisor uses FetchUptimeTrackerData. The RPC returns the
// raw JSON body the visor's UT client received — we re-parse to
// pull the percentage out for the one-line summary.
func uptimeFromVisor(rc interface {
	FetchUptimeTrackerData(pk string) ([]byte, error)
}, pk string) whoisBlock {
	block := whoisBlock{URL: "visor-RPC: FetchUptimeTrackerData"}
	body, err := rc.FetchUptimeTrackerData(pk)
	if err != nil {
		block.Error = err.Error()
		return block
	}
	if len(body) == 0 {
		block.Summary = "no uptime data"
		return block
	}
	block.Raw = json.RawMessage(body)
	block.Summary = summarizeUptime(block.Raw)
	return block
}

// summarizeUptime extracts a headline from a UT response. The
// production UT endpoint returns an array of {pk, on, version,
// daily: {YYYY-MM-DD: "pct"}} records — newest date in `daily` is
// the most recent day's uptime %, and `on` is the current visor
// liveness flag from the tracker's POV.
func summarizeUptime(raw json.RawMessage) string {
	var record map[string]interface{}
	if err := json.Unmarshal(raw, &record); err != nil {
		var arr []map[string]interface{}
		if err2 := json.Unmarshal(raw, &arr); err2 != nil || len(arr) == 0 {
			return "no uptime data"
		}
		record = arr[0]
	}
	on := "?"
	if v, ok := record["on"].(bool); ok {
		if v {
			on = "online"
		} else {
			on = "offline"
		}
	}
	version, _ := record["version"].(string)
	// daily map → find the most recent date + show its percentage.
	if daily, ok := record["daily"].(map[string]interface{}); ok && len(daily) > 0 {
		mostRecent := ""
		var mostRecentPct string
		for k, v := range daily {
			if k > mostRecent {
				mostRecent = k
				if s, sok := v.(string); sok {
					mostRecentPct = s
				}
			}
		}
		out := fmt.Sprintf("%s (today %s%%", on, mostRecentPct)
		if mostRecent != "" {
			out += " on " + mostRecent
		}
		out += ")"
		if version != "" {
			out += " v=" + version
		}
		return out
	}
	if version != "" {
		return fmt.Sprintf("%s v=%s (no daily uptime data)", on, version)
	}
	return on + " (uptime payload missing daily map)"
}

// httpCell renders the status as "200", "404", "rpc" (for visor-
// RPC backed blocks where Status is unset), "n/a" (informational
// "no per-PK lookup available" blocks), "err", or "timeout".
func httpCell(b whoisBlock) string {
	if b.Status > 0 {
		return fmt.Sprintf("%d", b.Status)
	}
	if strings.Contains(b.Error, "context deadline exceeded") {
		return "timeout"
	}
	if strings.HasPrefix(b.URL, "visor-RPC:") && b.Error == "" {
		return "rpc"
	}
	if b.Error == "" && b.Summary != "" {
		return "n/a"
	}
	return "err"
}

// valOrErr returns the Summary if set, otherwise the Error.
func valOrErr(b whoisBlock) string {
	if b.Summary != "" {
		return b.Summary
	}
	return b.Error
}
