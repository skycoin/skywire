// Package cliskychat cmd/skywire-cli/commands/skychat/tui_unified_io.go
//
// IO helpers for the unified TUI. Wraps the chat-app's /history/peers
// HTTP endpoint and the visor's group RPC surface so the bubbletea
// model can stay focused on the view shape.
//
// Group poll auto-reconnects on RPC connection drops using the
// quiet-client pattern from #2516, so a visor halt/restart during a
// long TUI session doesn't spam the chat history with reconnect noise.

package cliskychat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	skychatgroup "github.com/skycoin/skywire/cmd/apps/skychat/group"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
	"github.com/skycoin/skywire/pkg/visor"
)

// fetchHistoryPeers calls the chat-app's /history/peers endpoint and
// returns the de-duplicated list of PKs we have any 1:1 history with.
// Quiet when persistence is disabled (returns an empty list, no
// error) so a fresh visor with no persistence doesn't surface noise.
func fetchHistoryPeers(addr string) ([]string, error) {
	url := fmt.Sprintf("http://%s/history/peers", addr)
	hc := &http.Client{Timeout: 5 * time.Second}
	resp, err := hc.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusServiceUnavailable {
		// Persistence off — no peers known via this path. Not a
		// hard error; just an empty list.
		return nil, nil
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck
		return nil, fmt.Errorf("history/peers %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var peers []string
	if err := json.NewDecoder(resp.Body).Decode(&peers); err != nil {
		return nil, err
	}
	// Drop self-PK if it sneaks in.
	myPK := localVisorPKFromStatus(addr)
	out := peers[:0]
	for _, p := range peers {
		if p != myPK {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}

// localVisorPKFromStatus reads /status to learn the local visor PK
// (without an RPC round-trip). Returns "" on any failure — only used
// as a sanity filter, not load-bearing.
func localVisorPKFromStatus(addr string) string {
	url := fmt.Sprintf("http://%s/status", addr)
	hc := &http.Client{Timeout: 1 * time.Second}
	resp, err := hc.Get(url) //nolint:gosec
	if err != nil {
		return ""
	}
	defer resp.Body.Close() //nolint:errcheck
	var s struct {
		VisorPK string `json:"visor_pk"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		return ""
	}
	return s.VisorPK
}

// fetchGroupList queries the visor RPC for the operator's known
// groups. Quietly returns an empty slice if grouping is disabled or
// the RPC dial fails — picker still renders DMs.
func fetchGroupList() ([]visor.GroupInfo, error) {
	c, err := clirpc.ClientQuiet(nil)
	if err != nil {
		return nil, err
	}
	return c.GroupList()
}

// loadPickerInline is the synchronous version of unifiedModel.loadPicker
// — used after mutations (join, create) when the model needs an
// immediate refresh before the next Init cycle. Returns the entries
// + DM-load error + group-load error as strings (concat'd for display).
func loadPickerInline() ([]pickerEntry, string, string) {
	var entries []pickerEntry
	dmErr := ""
	groupErr := ""

	peers, err := fetchHistoryPeers(defaultAddrForRefresh())
	if err != nil {
		dmErr = err.Error()
	}
	for _, p := range peers {
		e := pickerEntry{kind: "dm", id: p, display: p}
		if alias := lookupAlias(p); alias != "" {
			e.display = alias
		}
		entries = append(entries, e)
	}

	groups, gErr := fetchGroupList()
	if gErr != nil {
		groupErr = gErr.Error()
	}
	for _, g := range groups {
		entries = append(entries, pickerEntry{
			kind:    "group",
			id:      g.ID,
			display: g.Name,
			subline: fmt.Sprintf("%d members · %s", len(g.Members), g.Role),
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].kind != entries[j].kind {
			return entries[i].kind == "dm"
		}
		return entries[i].display < entries[j].display
	})

	return entries, dmErr, groupErr
}

// defaultAddrForRefresh returns the package-level httpAddr — used by
// the inline refresh path because tea.Msg producers don't have a
// closure over the model's addr field. httpAddr is set by the chat
// cmd's PersistentFlags wiring at startup.
func defaultAddrForRefresh() string {
	return httpAddr
}

// groupSendRPC publishes a message to a group via the visor's RPC.
// Mirrors the standalone `skychat group send` CLI but returns the
// error directly so the TUI can render it in-pane.
func groupSendRPC(groupID, text string) error {
	if strings.TrimSpace(groupID) == "" {
		return errors.New("group id required")
	}
	c, err := clirpc.ClientQuiet(nil)
	if err != nil {
		return err
	}
	return c.GroupSend(visor.GroupSendArgs{ID: groupID, Text: text})
}

// groupJoinRPC accepts an invite token and joins the group via RPC.
// Token is the base64url-encoded payload (the part after the
// "skychat:invite:" prefix the CLI prints, OR the full string —
// stripped here).
func groupJoinRPC(invite string) error {
	invite = strings.TrimSpace(invite)
	invite = strings.TrimPrefix(invite, "skychat:invite:")
	if invite == "" {
		return errors.New("empty invite")
	}
	c, err := clirpc.ClientQuiet(nil)
	if err != nil {
		return err
	}
	_, err = c.GroupJoin(visor.GroupJoinArgs{Invite: "skychat:invite:" + invite})
	return err
}

// groupCreateRPC creates a public group with the given name (owner =
// local visor). Returns nil on success; the caller refreshes the
// picker to pick up the new entry.
func groupCreateRPC(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("group name required")
	}
	c, err := clirpc.ClientQuiet(nil)
	if err != nil {
		return err
	}
	_, _, err = c.GroupCreate(visor.GroupCreateArgs{
		Name: name,
		Mode: skychatgroup.ModePublic,
	})
	return err
}

// runGroupPollLoop pumps inbound group messages onto inCh until ctx
// is canceled. Implements the same auto-reconnect pattern as the
// standalone `group listen` command — silent on the reconnect path
// (only the model sees structured errors via errCh), exponential
// backoff capped at 30s.
func runGroupPollLoop(ctx context.Context, inCh chan<- groupTUIMsg, errCh chan<- error) {
	const (
		pollInterval = 1 * time.Second
		minBackoff   = 1 * time.Second
		maxBackoff   = 30 * time.Second
	)
	defer close(inCh)

	var since time.Time
	rpc, err := clirpc.ClientQuiet(nil)
	if err != nil {
		select {
		case errCh <- err:
		case <-ctx.Done():
			return
		}
		// Try to recover by reconnect loop below.
	}
	backoff := minBackoff
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if rpc == nil {
			if newCl, cErr := clirpc.ClientQuiet(nil); cErr == nil {
				rpc = newCl
				backoff = minBackoff
			} else {
				select {
				case <-ctx.Done():
					return
				case <-time.After(backoff):
				}
				if backoff < maxBackoff {
					backoff *= 2
					if backoff > maxBackoff {
						backoff = maxBackoff
					}
				}
				continue
			}
		}
		msgs, err := rpc.GroupPoll(since)
		if err != nil {
			rpc = nil
			select {
			case errCh <- err:
			default:
			}
			continue
		}
		for _, msg := range msgs {
			if msg.TS.After(since) {
				since = msg.TS
			}
			select {
			case <-ctx.Done():
				return
			case inCh <- groupTUIMsg{
				GroupID:  msg.GroupID,
				SenderPK: msg.SenderPK.Hex(),
				Body:     msg.Text,
				When:     msg.TS,
			}:
			}
		}
	}
}
