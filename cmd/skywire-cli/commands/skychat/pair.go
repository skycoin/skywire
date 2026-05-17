// Package cliskychat — cmd/skywire-cli/commands/skychat/pair.go:
// CLI surface for the CXO-backed 1:1 chat path (test-matrix row 1c).
//
// Distinguishes from `cli skychat send`: that one POSTs /message
// which writes a chat-msg envelope over dmsg-pair or a skynet
// route — no CXO, no persistence. These subcommands target the
// chat-app's /pair endpoints which sit on top of the visor's
// Pair-RPC (PairAdd / PairSend / PairList / PairPoll). Each pair
// is a 2-member CXO group with per-side per-peer feeds; the
// underlying mechanism is the same shape as skychat-group's
// federated send (#2580) restricted to N=2.
//
// Operator use case: validate that CXO p2p works (catches bugs in
// CXO itself) before piling on the multi-subscriber convergence
// case (1d, full-mesh group) or the admin-aggregator topology (2,
// federated). If 1c (this surface) works but 1d breaks, we know
// the bug is in CXO multi-subscriber convergence, not in CXO
// itself.
//
// Pair-rpc is gated by `--pair-enable` on the chat-app — chat-app
// surfaces /pair endpoints only when that flag is set. CLI emits
// a clear "pair-rpc-disabled" error rather than a confused 404
// when the chat-app side hasn't enabled it.
package cliskychat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

var (
	pairTarget    string
	pairMessage   string
	pairVerbose   bool
	pairPollSince string
	pairPollLimit int
)

func init() {
	pairCmd.AddCommand(pairAddCmd, pairListCmd, pairSendCmd, pairPollCmd, pairAcceptCmd, pairDeclineCmd, pairRemoveCmd)

	pairAddCmd.Flags().StringVarP(&pairTarget, "to", "t", "", "peer public key to pair with (required)")
	pairAddCmd.MarkFlagRequired("to") //nolint:errcheck,gosec

	pairSendCmd.Flags().StringVarP(&pairTarget, "to", "t", "", "peer public key (required)")
	pairSendCmd.Flags().StringVarP(&pairMessage, "msg", "m", "", "message to send (required)")
	pairSendCmd.Flags().BoolVar(&pairVerbose, "verbose", false, "surface per-layer detail to stderr: POST request, response status, pair-counter deltas")
	pairSendCmd.MarkFlagRequired("to")  //nolint:errcheck,gosec
	pairSendCmd.MarkFlagRequired("msg") //nolint:errcheck,gosec

	pairPollCmd.Flags().StringVar(&pairPollSince, "since", "", "RFC3339 lower bound; empty drains everything new")
	pairPollCmd.Flags().IntVar(&pairPollLimit, "limit", 100, "max messages (1-1000)")

	pairAcceptCmd.Flags().StringVarP(&pairTarget, "from", "f", "", "inviter peer public key (required)")
	pairAcceptCmd.MarkFlagRequired("from") //nolint:errcheck,gosec

	pairDeclineCmd.Flags().StringVarP(&pairTarget, "from", "f", "", "inviter peer public key (required)")
	pairDeclineCmd.MarkFlagRequired("from") //nolint:errcheck,gosec

	pairRemoveCmd.Flags().StringVarP(&pairTarget, "to", "t", "", "peer public key to remove (required)")
	pairRemoveCmd.MarkFlagRequired("to") //nolint:errcheck,gosec

	RootCmd.AddCommand(pairCmd)
}

var pairCmd = &cobra.Command{
	Use:   "pair",
	Short: "CXO-backed 1:1 chat (paired contacts)",
	Long: `1:1 messaging over a CXO per-pair feed. Distinct from ` + "`cli skychat send`" + `:
that one rides direct dmsg-pair or a skynet route (no CXO, no
persistence). ` + "`cli skychat pair`" + ` uses the visor's Pair-RPC under the
hood, which sits on top of a 2-member CXO group with per-side
per-peer feeds.

Requires the chat-app started with --pair-enable. Subcommands
return a clear "pair-rpc-disabled" message when that flag isn't
set on the local chat-app.

Lifecycle:

  add <peer>          send a pair invite (status: pending)
  accept --from <pk>  accept an incoming invite (status: active)
  decline --from <pk> decline an incoming invite (status: removed)
  list                list known pairs + status
  send -t <pk> -m X   publish a message to a paired peer's feed
  poll [--since X]    drain inbound pair messages
  remove -t <pk>      remove a pair`,
}

var pairAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Send a pair invite to a peer",
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(pairTarget)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		body, err := json.Marshal(map[string]string{"peer_pk": pk.String()})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		url := fmt.Sprintf("http://%s/pair", httpAddr)
		resp, err := httpPost(url, body, 5*time.Second)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Invite sent to %s — peer must accept via `cli skychat pair accept --from %s`\n", pk.String(), getLocalPK(httpAddr)) //nolint:errcheck
	},
}

var pairListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known pairs",
	Run: func(cmd *cobra.Command, _ []string) {
		url := fmt.Sprintf("http://%s/pair", httpAddr)
		hc := &http.Client{Timeout: 5 * time.Second}
		resp, err := hc.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("pair list: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var pairs []struct {
			PeerPK    string `json:"peer_pk"`
			Status    string `json:"status"`
			AddedAt   string `json:"added_at,omitempty"`
			LastMsgAt string `json:"last_msg_at,omitempty"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&pairs); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("decode pair list: %w", err))
		}
		out := cmd.OutOrStdout()
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		if jsonMode {
			b, _ := json.MarshalIndent(pairs, "", "  ") //nolint:errcheck
			_, _ = out.Write(append(b, '\n'))           //nolint:errcheck
			return
		}
		if len(pairs) == 0 {
			fmt.Fprintln(out, "no pairs") //nolint:errcheck
			return
		}
		for _, p := range pairs {
			fmt.Fprintf(out, "%s  status=%s", p.PeerPK, p.Status) //nolint:errcheck
			if p.LastMsgAt != "" {
				fmt.Fprintf(out, "  last_msg=%s", p.LastMsgAt) //nolint:errcheck
			}
			fmt.Fprintln(out) //nolint:errcheck
		}
	},
}

var pairSendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send a message to a paired peer via CXO pair feed",
	Long: `Publishes the message via the visor's PairSend RPC (CXO per-pair
feed). Distinct from ` + "`cli skychat send`" + ` which rides direct dmsg/skynet.

Requires an active pair (status=active in ` + "`cli skychat pair list`" + `).
Peer poll their inbox via ` + "`cli skychat pair poll`" + ` or the SSE stream
(/sse) to see inbound messages.

--verbose surfaces:
  pre-send pair-list state for the target
  POST request + response detail
  post-send pair-list state
  delta in last_msg_at / status`,
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(pairTarget)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		errOut := cmd.ErrOrStderr()
		var prePair *pairStateSnap
		if pairVerbose {
			fmt.Fprintf(errOut, "[verbose] target: %s\n", pk.String())                                    //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] msg bytes: %d\n", len(pairMessage))                            //nolint:errcheck
			fmt.Fprintf(errOut, "[verbose] endpoint: http://%s/pair/%s/message\n", httpAddr, pk.String()) //nolint:errcheck
			if c, err := fetchStatusCounters(httpAddr); err == nil {
				fmt.Fprintf(errOut, "[verbose] pre-send counters: %s\n", c.summary()) //nolint:errcheck
			}
			if snap, err := fetchPairState(httpAddr, pk.String()); err == nil {
				prePair = snap
				fmt.Fprintf(errOut, "[verbose] pre-send pair state: status=%s last_msg=%s\n", snap.Status, snap.LastMsgAt) //nolint:errcheck
			} else {
				fmt.Fprintf(errOut, "[verbose] pre-send pair state: <fetch failed: %v>\n", err) //nolint:errcheck
			}
		}

		body, err := json.Marshal(map[string]string{"text": pairMessage})
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		url := fmt.Sprintf("http://%s/pair/%s/message", httpAddr, pk.String())
		start := time.Now()
		resp, err := httpPost(url, body, 10*time.Second)
		elapsed := time.Since(start)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if pairVerbose {
			fmt.Fprintf(errOut, "[verbose] response: %d in %s\n", resp.StatusCode, elapsed.Round(time.Millisecond)) //nolint:errcheck
		}
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}

		if pairVerbose {
			if c, err := fetchStatusCounters(httpAddr); err == nil {
				fmt.Fprintf(errOut, "[verbose] post-send counters: %s\n", c.summary()) //nolint:errcheck
			}
			if snap, err := fetchPairState(httpAddr, pk.String()); err == nil {
				fmt.Fprintf(errOut, "[verbose] post-send pair state: status=%s last_msg=%s\n", snap.Status, snap.LastMsgAt) //nolint:errcheck
				if prePair != nil && prePair.LastMsgAt != snap.LastMsgAt {
					fmt.Fprintf(errOut, "[verbose] pair last_msg advanced: %s → %s\n", prePair.LastMsgAt, snap.LastMsgAt) //nolint:errcheck
				}
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Pair message sent to %s in %s (no peer-ack — pair messages are CXO-published fire-and-forget; peer sees via poll/SSE)\n", pk.String(), elapsed.Round(time.Millisecond)) //nolint:errcheck
	},
}

var pairPollCmd = &cobra.Command{
	Use:   "poll",
	Short: "Drain inbound pair messages",
	Long:  `Polls the visor's PairPoll RPC and prints any messages newer than --since (default: all that are still in the inbox).`,
	Run: func(cmd *cobra.Command, _ []string) {
		if pairPollLimit < 1 || pairPollLimit > 1000 {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("--limit must be 1-1000, got %d", pairPollLimit))
		}
		url := fmt.Sprintf("http://%s/pair/inbox?limit=%d", httpAddr, pairPollLimit)
		if pairPollSince != "" {
			url += "&since=" + pairPollSince
		}
		hc := &http.Client{Timeout: 5 * time.Second}
		resp, err := hc.Get(url) //nolint:gosec
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("pair poll: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		var msgs []struct {
			Peer string    `json:"peer_pk"`
			Text string    `json:"text"`
			TS   time.Time `json:"ts"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("decode pair poll: %w", err))
		}
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck
		out := cmd.OutOrStdout()
		for _, m := range msgs {
			if jsonMode {
				b, _ := json.Marshal(m)           //nolint:errcheck
				_, _ = out.Write(append(b, '\n')) //nolint:errcheck
			} else {
				fmt.Fprintf(out, "%s [%s] %s\n", m.TS.UTC().Format(time.RFC3339), m.Peer, m.Text) //nolint:errcheck
			}
		}
	},
}

var pairAcceptCmd = &cobra.Command{
	Use:   "accept",
	Short: "Accept a pair invite",
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(pairTarget)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		url := fmt.Sprintf("http://%s/pair/invites/%s/accept", httpAddr, pk.String())
		resp, err := httpPost(url, nil, 5*time.Second)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Pair accepted with %s\n", pk.String()) //nolint:errcheck
	},
}

var pairDeclineCmd = &cobra.Command{
	Use:   "decline",
	Short: "Decline a pair invite",
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(pairTarget)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		url := fmt.Sprintf("http://%s/pair/invites/%s/decline", httpAddr, pk.String())
		resp, err := httpPost(url, nil, 5*time.Second)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Pair invite from %s declined\n", pk.String()) //nolint:errcheck
	},
}

var pairRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove an active pair",
	Run: func(cmd *cobra.Command, _ []string) {
		pk, err := resolveTarget(pairTarget)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		url := fmt.Sprintf("http://%s/pair/%s", httpAddr, pk.String())
		req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, url, nil)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		hc := &http.Client{Timeout: 5 * time.Second}
		resp, err := hc.Do(req)
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("pair remove: %w", err))
		}
		defer resp.Body.Close() //nolint:errcheck
		if err := checkOK(resp); err != nil {
			internal.PrintFatalError(cmd.Flags(), err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Pair with %s removed\n", pk.String()) //nolint:errcheck
	},
}

// pairStateSnap is the minimal view of a pair record the verbose
// path needs to compute a pre/post-send delta. Subset of the
// chat-app's /pair JSON shape.
type pairStateSnap struct {
	Status    string `json:"status"`
	LastMsgAt string `json:"last_msg_at,omitempty"`
}

// fetchPairState GETs /pair (the list) and filters for peerPK.
// Returns ErrPairNotFound when the list comes back but the peer
// isn't in it — distinct from network/decode errors.
func fetchPairState(addr, peerPK string) (*pairStateSnap, error) {
	url := fmt.Sprintf("http://%s/pair", addr)
	hc := &http.Client{Timeout: 2 * time.Second}
	resp, err := hc.Get(url) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("pair fetch: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, errors.New("pair-rpc-disabled (chat-app not started with --pair-enable)")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("pair fetch status %d: %s", resp.StatusCode, string(body))
	}
	var pairs []struct {
		PeerPK    string `json:"peer_pk"`
		Status    string `json:"status"`
		LastMsgAt string `json:"last_msg_at,omitempty"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&pairs); err != nil {
		return nil, fmt.Errorf("decode pair list: %w", err)
	}
	for _, p := range pairs {
		if p.PeerPK == peerPK {
			return &pairStateSnap{Status: p.Status, LastMsgAt: p.LastMsgAt}, nil
		}
	}
	return &pairStateSnap{Status: "not-paired"}, nil
}

// httpPost is a small wrapper consolidating POST + JSON body across
// pair subcommands. Returns the response; caller closes Body. A nil
// body sends a zero-length POST (used for /accept and /decline).
func httpPost(url string, body []byte, timeout time.Duration) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	hc := &http.Client{Timeout: timeout}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post %s: %w", url, err)
	}
	return resp, nil
}

// checkOK turns a non-2xx HTTP status into an error with the
// server's body. 503 maps to a clearer "pair-rpc-disabled" hint so
// operators don't have to guess at the chat-app config.
func checkOK(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	if resp.StatusCode == http.StatusServiceUnavailable {
		return fmt.Errorf("pair-rpc-disabled (start chat-app with --pair-enable): %s", string(body))
	}
	return fmt.Errorf("server returned %d: %s", resp.StatusCode, string(body))
}

// getLocalPK is a best-effort lookup of the local visor's PK via
// the chat-app /status endpoint. Returns "<local>" on any error
// so the user message doesn't fail just because the lookup did.
func getLocalPK(addr string) string {
	url := fmt.Sprintf("http://%s/status", addr)
	hc := &http.Client{Timeout: 2 * time.Second}
	resp, err := hc.Get(url) //nolint:gosec
	if err != nil {
		return "<local>"
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "<local>"
	}
	var s struct {
		VisorPK string `json:"visor_pk"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil || s.VisorPK == "" {
		return "<local>"
	}
	return s.VisorPK
}
