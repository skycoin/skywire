// Package clivisor cmd/skywire-cli/commands/visor/doctor.go: `cli
// visor doctor` rolls a healthy-or-not verdict + the underlying
// signals into one command. Today an operator answers "is this
// visor healthy?" by running five commands (visor info, tp ls, rg
// ls, dmsg diag, svc health) and reading skychat /status separately
// — doctor collapses that into a single output with a GREEN /
// YELLOW / RED top-line verdict and the per-subsystem breakdown.
//
// No new visor RPC surface — composes existing Summary + the
// chat-app's HTTP /status endpoint. Skychat is queried best-effort:
// when the app isn't running or the address is wrong, doctor still
// returns the visor-side verdict and notes the skychat probe error
// in the JSON output.
package clivisor

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

const (
	verdictGreen  = "GREEN"
	verdictYellow = "YELLOW"
	verdictRed    = "RED"
)

var (
	doctorSkychatAddr string
	doctorSkychatHTTP time.Duration
)

func init() {
	doctorCmd.Flags().SortFlags = false
	doctorCmd.Flags().StringVar(&doctorSkychatAddr, "skychat", "127.0.0.1:8001",
		"skychat HTTP address to probe; empty disables the skychat probe entirely")
	doctorCmd.Flags().DurationVar(&doctorSkychatHTTP, "skychat-timeout", 2*time.Second,
		"per-request timeout for the skychat probe")
	RootCmd.AddCommand(doctorCmd)
}

// doctorReport is the JSON output shape. Stable so scripts can rely
// on the field names. Verdict is the bottom-line classification an
// operator alarming-pipeline can key on (RED → page, YELLOW →
// dashboard amber, GREEN → silent).
type doctorReport struct {
	Verdict string   `json:"verdict"`
	Reasons []string `json:"reasons"`

	Visor   doctorVisorBlock   `json:"visor"`
	Skychat *doctorSkychatBlock `json:"skychat,omitempty"`
}

type doctorVisorBlock struct {
	Reachable             bool    `json:"reachable"`
	ReachabilityError     string  `json:"reachability_error,omitempty"`
	Ready                 bool    `json:"ready"`
	PubKey                string  `json:"public_key,omitempty"`
	Version               string  `json:"version,omitempty"`
	UptimeSeconds         float64 `json:"uptime_seconds,omitempty"`
	ServicesHealth        string  `json:"services_health,omitempty"`
	UptimeTrackerHealth   string  `json:"uptime_tracker_health,omitempty"`
	AutoconnectHealth     string  `json:"autoconnect_health,omitempty"`
	TransportabilityHealth string `json:"transportability_health,omitempty"`
	TransportCount        int     `json:"transport_count"`
	RouteGroupCount       int     `json:"route_group_count"`
	DMSGServerCount       int     `json:"dmsg_server_count"`
	RoutesCount           int     `json:"routes_count"`
}

type doctorSkychatBlock struct {
	Reachable          bool   `json:"reachable"`
	ReachabilityError  string `json:"reachability_error,omitempty"`
	ActivePeerConns    int    `json:"active_peer_conns"`
	InboundMsgCount    uint64 `json:"inbound_msg_count"`
	OutboundMsgCount   uint64 `json:"outbound_msg_count"`
	InboundDropCount   uint64 `json:"inbound_drop_count"`
	OutboundFailCount  uint64 `json:"outbound_fail_count"`
	OutboundRetryCount uint64 `json:"outbound_retry_count"`
	SSEDropCount       uint64 `json:"sse_drop_count"`
	SSESubscribers     int    `json:"sse_subscribers"`
	GroupsCount        int    `json:"groups_count"`
	GroupsError        string `json:"groups_error,omitempty"`
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "One-shot visor health rollup with a GREEN/YELLOW/RED verdict",
	Long: `One-shot visor health rollup.

Composes the existing Summary RPC + a best-effort skychat /status probe
into a single verdict (GREEN, YELLOW, or RED) plus the underlying
breakdown that drove it. No new visor RPC surface — purely a
client-side rollup.

Examples:
  skywire cli visor doctor             # human-readable
  skywire cli visor doctor --json      # JSON, for alerting/automation
  skywire cli visor doctor --skychat=  # disable the skychat probe

Verdict semantics:
  GREEN  — visor ready, all subsystems healthy, skychat (if probed) clean.
  YELLOW — at least one degraded signal (unhealthy subsystem, accumulated
           outbound failures, SSE drops, missing groups[] in /status).
           Visor is still serving but operator should investigate.
  RED    — visor RPC unreachable, not ready, or in a panic-equivalent
           state. Page someone.

Exit code mirrors the verdict: 0 GREEN, 1 YELLOW, 2 RED. Lets scripts
gate on doctor without parsing the output.`,
	Run: func(cmd *cobra.Command, _ []string) {
		report := runDoctor(cmd)
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode {
			b, _ := json.MarshalIndent(report, "", "  ") //nolint:errcheck
			fmt.Println(string(b))
		} else {
			fmt.Println(formatDoctorHuman(report))
		}
		switch report.Verdict {
		case verdictRed:
			os.Exit(2)
		case verdictYellow:
			os.Exit(1)
		}
	},
}

// runDoctor performs the probes and computes the verdict. Returns
// the full report unconditionally (even on RED) so the caller has
// something to print.
func runDoctor(cmd *cobra.Command) doctorReport {
	r := doctorReport{Verdict: verdictGreen}

	rpcClient, rpcErr := clirpc.Client(cmd.Flags())
	if rpcErr != nil {
		r.Verdict = verdictRed
		r.Reasons = append(r.Reasons, "visor RPC client init failed")
		r.Visor = doctorVisorBlock{Reachable: false, ReachabilityError: rpcErr.Error()}
		return r
	}

	probeVisor(&r, rpcClient)
	if doctorSkychatAddr != "" {
		probeSkychat(&r)
	}
	return r
}

// probeVisor populates r.Visor from the Summary + IsStartupComplete
// RPC calls. Reachability errors at this layer escalate the verdict
// to RED; degraded subsystems escalate to YELLOW.
func probeVisor(r *doctorReport, rc visor.API) {
	summary, err := rc.Summary()
	if err != nil {
		r.Verdict = verdictRed
		r.Reasons = append(r.Reasons, "Summary RPC failed: "+err.Error())
		r.Visor = doctorVisorBlock{Reachable: false, ReachabilityError: err.Error()}
		return
	}
	r.Visor.Reachable = true
	r.Visor.Ready = rc.IsStartupComplete()
	r.Visor.UptimeSeconds = summary.Uptime
	r.Visor.RoutesCount = summary.Overview.RoutesCount
	r.Visor.TransportCount = len(summary.Overview.Transports)
	r.Visor.RouteGroupCount = len(summary.RouteGroups)
	r.Visor.DMSGServerCount = len(summary.DMSGServers)
	r.Visor.PubKey = summary.Overview.PubKey.String()
	if summary.Overview.BuildInfo != nil {
		r.Visor.Version = summary.Overview.BuildInfo.Version
	}
	if summary.Health != nil {
		r.Visor.ServicesHealth = summary.Health.ServicesHealth
		r.Visor.UptimeTrackerHealth = summary.Health.UptimeTrackerHealth
		r.Visor.AutoconnectHealth = summary.Health.AutoconnectHealth
		r.Visor.TransportabilityHealth = summary.Health.TransportabilityHealth
	}
	if !r.Visor.Ready {
		bumpVerdict(r, verdictRed, "visor not ready (IsStartupComplete=false)")
	}
	for label, val := range map[string]string{
		"services_health":         r.Visor.ServicesHealth,
		"uptime_tracker_health":   r.Visor.UptimeTrackerHealth,
		"autoconnect_health":      r.Visor.AutoconnectHealth,
		"transportability_health": r.Visor.TransportabilityHealth,
	} {
		// Empty string means "not surfaced by this visor build" —
		// not a degraded state. We only flag explicit non-healthy
		// values.
		if val != "" && !isHealthyValue(val) {
			bumpVerdict(r, verdictYellow, label+"="+val)
		}
	}
	if r.Visor.DMSGServerCount == 0 {
		bumpVerdict(r, verdictYellow, "no connected DMSG servers")
	}
}

// probeSkychat fetches /status from the chat-app. The chat-app is
// independent of the visor — failure here is YELLOW (degraded
// agent-coordination path), not RED (visor still works).
func probeSkychat(r *doctorReport) {
	r.Skychat = &doctorSkychatBlock{}
	client := &http.Client{Timeout: doctorSkychatHTTP}
	resp, err := client.Get("http://" + doctorSkychatAddr + "/status") //nolint:gosec,noctx
	if err != nil {
		r.Skychat.Reachable = false
		r.Skychat.ReachabilityError = err.Error()
		bumpVerdict(r, verdictYellow, "skychat /status unreachable")
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		r.Skychat.Reachable = false
		r.Skychat.ReachabilityError = fmt.Sprintf("HTTP %d", resp.StatusCode)
		bumpVerdict(r, verdictYellow, fmt.Sprintf("skychat /status HTTP %d", resp.StatusCode))
		return
	}
	r.Skychat.Reachable = true
	var raw struct {
		ActivePeerConns    int    `json:"active_peer_conns"`
		InboundMsgCount    uint64 `json:"inbound_msg_count"`
		OutboundMsgCount   uint64 `json:"outbound_msg_count"`
		InboundDropCount   uint64 `json:"inbound_drop_count"`
		OutboundFailCount  uint64 `json:"outbound_fail_count"`
		OutboundRetryCount uint64 `json:"outbound_retry_count"`
		SSEDropCount       uint64 `json:"sse_drop_count"`
		SSESubscribers     int    `json:"sse_subscribers"`
		Groups             []struct {
			ID string `json:"id"`
		} `json:"groups"`
		GroupsError string `json:"groups_error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		r.Skychat.Reachable = false
		r.Skychat.ReachabilityError = "decode: " + err.Error()
		bumpVerdict(r, verdictYellow, "skychat /status decode failed")
		return
	}
	r.Skychat.ActivePeerConns = raw.ActivePeerConns
	r.Skychat.InboundMsgCount = raw.InboundMsgCount
	r.Skychat.OutboundMsgCount = raw.OutboundMsgCount
	r.Skychat.InboundDropCount = raw.InboundDropCount
	r.Skychat.OutboundFailCount = raw.OutboundFailCount
	r.Skychat.OutboundRetryCount = raw.OutboundRetryCount
	r.Skychat.SSEDropCount = raw.SSEDropCount
	r.Skychat.SSESubscribers = raw.SSESubscribers
	r.Skychat.GroupsCount = len(raw.Groups)
	r.Skychat.GroupsError = raw.GroupsError

	// Only ESCALATE on counters that signal current-or-recent
	// degradation. inbound_drop_count is a cumulative since-startup
	// number that includes normal peer-disconnect ReadFrame errors;
	// a non-zero value isn't a fault signal on its own, just an
	// informational stat. outbound_fail_count and sse_drop_count
	// both represent data the caller / listener will have NOT
	// received — those are real degradation evidence.
	if raw.OutboundFailCount > 0 {
		bumpVerdict(r, verdictYellow,
			fmt.Sprintf("skychat outbound_fail_count=%d (data loss surfaced to caller)", raw.OutboundFailCount))
	}
	if raw.SSEDropCount > 0 {
		bumpVerdict(r, verdictYellow,
			fmt.Sprintf("skychat sse_drop_count=%d (subscriber buffer overruns)", raw.SSEDropCount))
	}
	if raw.GroupsError != "" {
		// "pair-rpc-disabled" is a config choice, not a fault.
		// Everything else is a degraded state.
		if raw.GroupsError != "pair-rpc-disabled" {
			bumpVerdict(r, verdictYellow, "skychat groups_error="+raw.GroupsError)
		}
	}
}

// bumpVerdict promotes the report's verdict to at least target.
// RED outranks YELLOW outranks GREEN. Each reason is appended once;
// duplicates are a non-issue here since callers don't repeat.
func bumpVerdict(r *doctorReport, target string, reason string) {
	r.Reasons = append(r.Reasons, reason)
	switch r.Verdict {
	case verdictGreen:
		r.Verdict = target
	case verdictYellow:
		if target == verdictRed {
			r.Verdict = verdictRed
		}
	}
}

// isHealthyValue is the central rule for what counts as a "healthy"
// per-subsystem string. The visor today emits "healthy" on success
// and various values ("connecting", "unhealthy", error strings) on
// failure. Whitelist "healthy" and treat everything else as degraded.
func isHealthyValue(v string) bool { return v == "healthy" }

// formatDoctorHuman renders the report as human-friendly text.
// Top-line verdict + a single block per subsystem. Designed for
// terminal eyeballs, not for parsing.
func formatDoctorHuman(r doctorReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Verdict: %s\n", r.Verdict)
	if len(r.Reasons) > 0 {
		fmt.Fprintln(&b, "Reasons:")
		for _, x := range r.Reasons {
			fmt.Fprintf(&b, "  - %s\n", x)
		}
	}
	fmt.Fprintln(&b, "")
	fmt.Fprintln(&b, "Visor:")
	if !r.Visor.Reachable {
		fmt.Fprintf(&b, "  reachable:  false (%s)\n", r.Visor.ReachabilityError)
	} else {
		fmt.Fprintf(&b, "  reachable:  true\n")
		fmt.Fprintf(&b, "  ready:      %v\n", r.Visor.Ready)
		if r.Visor.PubKey != "" {
			fmt.Fprintf(&b, "  pk:         %s\n", r.Visor.PubKey)
		}
		if r.Visor.Version != "" {
			fmt.Fprintf(&b, "  version:    %s\n", r.Visor.Version)
		}
		fmt.Fprintf(&b, "  uptime_s:   %.0f\n", r.Visor.UptimeSeconds)
		fmt.Fprintf(&b, "  transports: %d\n", r.Visor.TransportCount)
		fmt.Fprintf(&b, "  route_grps: %d\n", r.Visor.RouteGroupCount)
		fmt.Fprintf(&b, "  routes:     %d\n", r.Visor.RoutesCount)
		fmt.Fprintf(&b, "  dmsg_srvs:  %d\n", r.Visor.DMSGServerCount)
		fmt.Fprintf(&b, "  services:   %s\n", r.Visor.ServicesHealth)
		if r.Visor.UptimeTrackerHealth != "" {
			fmt.Fprintf(&b, "  ut:         %s\n", r.Visor.UptimeTrackerHealth)
		}
		if r.Visor.AutoconnectHealth != "" {
			fmt.Fprintf(&b, "  autoconn:   %s\n", r.Visor.AutoconnectHealth)
		}
		if r.Visor.TransportabilityHealth != "" {
			fmt.Fprintf(&b, "  tprt:       %s\n", r.Visor.TransportabilityHealth)
		}
	}
	if r.Skychat != nil {
		fmt.Fprintln(&b, "")
		fmt.Fprintln(&b, "Skychat:")
		if !r.Skychat.Reachable {
			fmt.Fprintf(&b, "  reachable:  false (%s)\n", r.Skychat.ReachabilityError)
		} else {
			fmt.Fprintf(&b, "  reachable:  true\n")
			fmt.Fprintf(&b, "  peers:      %d\n", r.Skychat.ActivePeerConns)
			fmt.Fprintf(&b, "  in/out:     %d / %d msgs\n", r.Skychat.InboundMsgCount, r.Skychat.OutboundMsgCount)
			fmt.Fprintf(&b, "  in_drops:   %d\n", r.Skychat.InboundDropCount)
			fmt.Fprintf(&b, "  out_fails:  %d\n", r.Skychat.OutboundFailCount)
			fmt.Fprintf(&b, "  out_retry:  %d\n", r.Skychat.OutboundRetryCount)
			fmt.Fprintf(&b, "  sse_drops:  %d\n", r.Skychat.SSEDropCount)
			fmt.Fprintf(&b, "  sse_subs:   %d\n", r.Skychat.SSESubscribers)
			fmt.Fprintf(&b, "  groups:     %d\n", r.Skychat.GroupsCount)
			if r.Skychat.GroupsError != "" {
				fmt.Fprintf(&b, "  groups_err: %s\n", r.Skychat.GroupsError)
			}
		}
	}
	return b.String()
}
