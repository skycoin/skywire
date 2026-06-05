// Package skychat — events.go: `skywire cli skychat events`, a structured
// NDJSON stream of chat events from the chat app's GET /events SSE endpoint.
//
// Replaces fragile `tail -F | awk | grep` log monitors (which broke under
// logrotate copytruncate). Each event carries a monotonic seq cursor; on
// reconnect the CLI resends --since=<last seq> so no events are missed, and
// dedupes by id. Agreed schema (Beta+Gamma consensus).
package cliskychat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
)

var (
	eventsChannel string
	eventsSince   uint64
)

func init() {
	eventsCmd.Flags().StringVar(&eventsChannel, "channel", "",
		"comma-separated channels: dm,group,pair,system,all (default/all = dm+group+pair; system is opt-in)")
	eventsCmd.Flags().Uint64Var(&eventsSince, "since", 0,
		"resume after this seq cursor (0 = from the start of the server's buffer)")
}

// cliChatEvent mirrors the chat app's chatEvent NDJSON shape.
type cliChatEvent struct {
	Seq       uint64    `json:"seq"`
	ID        string    `json:"id"`
	TS        time.Time `json:"ts"`
	Channel   string    `json:"channel"`
	Transport string    `json:"transport"`
	Dir       string    `json:"dir"`
	From      string    `json:"from,omitempty"`
	To        string    `json:"to,omitempty"`
	GroupID   string    `json:"group_id,omitempty"`
	Text      string    `json:"text"`
	ReplyToID string    `json:"reply_to_id,omitempty"`
	Len       int       `json:"len"`
}

var eventsCmd = &cobra.Command{
	Use:   "events",
	Short: "Stream structured chat events (NDJSON) from the chat app",
	Long: `Stream the chat app's structured event feed, one NDJSON event per line:
  {"seq","id","ts","channel","transport","dir","from","to","group_id","text","reply_to_id","len"}

Lossless resume: each event carries a monotonic seq; on reconnect the CLI
resends --since=<last seq> so no events are missed, and dedupes by id.
Channels: dm | group | pair | system | all (default/all = dm+group+pair;
"system" is opt-in). This replaces fragile log-file tailing.`,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		out := cmd.OutOrStdout()
		errOut := cmd.ErrOrStderr()
		jsonMode, _ := cmd.Flags().GetBool(internal.JSONString) //nolint:errcheck

		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		lastSeq := eventsSince
		seen := newIDDedupe(20000)
		delay := minReconnectDelay

		for {
			err := streamEventsOnce(ctx, out, jsonMode, eventsChannel, &lastSeq, seen)
			if ctx.Err() != nil {
				return
			}
			if err != nil {
				fmt.Fprintf(errOut, "events stream error: %v; reconnecting in %s (since=%d)\n", err, delay, lastSeq) //nolint:errcheck
				select {
				case <-ctx.Done():
					return
				case <-time.After(delay):
				}
				if delay < maxReconnectDelay {
					delay *= 2
				}
				continue
			}
			// Clean server close — reconnect promptly from where we left off.
			delay = minReconnectDelay
		}
	},
}

// streamEventsOnce opens one /events stream from since=*lastSeq and pumps
// events to out until the stream ends or errors. It advances *lastSeq to the
// highest seq seen so a reconnect resumes losslessly, and skips ids already
// emitted. Returns nil on a clean server-side close.
func streamEventsOnce(ctx context.Context, out io.Writer, jsonMode bool, channel string, lastSeq *uint64, seen *idDedupe) error {
	url := fmt.Sprintf("http://%s/events?since=%d", httpAddr, *lastSeq)
	if channel != "" {
		url += "&channel=" + channel
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chat app /events: HTTP %s", resp.Status)
	}

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue // ": ping"/": connected" keepalives and blank lines
		}
		payload := strings.TrimPrefix(line, "data: ")
		var ev cliChatEvent
		if err := json.Unmarshal([]byte(payload), &ev); err != nil {
			continue
		}
		if ev.Seq > *lastSeq {
			*lastSeq = ev.Seq
		}
		if ev.ID != "" {
			if seen.has(ev.ID) {
				continue
			}
			seen.add(ev.ID)
		}
		if jsonMode {
			fmt.Fprintln(out, payload) //nolint:errcheck // pass the NDJSON through verbatim
			continue
		}
		fmt.Fprintln(out, formatEvent(ev)) //nolint:errcheck
	}
	return sc.Err()
}

// formatEvent renders a human one-liner for non-JSON mode.
func formatEvent(ev cliChatEvent) string {
	ts := ev.TS.Local().Format("15:04:05")
	who := shortPK(ev.From)
	if ev.Dir == "out" && ev.To != "" {
		who = shortPK(ev.From) + "→" + shortPK(ev.To)
	}
	scope := ev.Channel
	if ev.GroupID != "" {
		scope = ev.Channel + ":" + ev.GroupID
	}
	return fmt.Sprintf("[%s] #%d %-6s %-3s %s  %s", ts, ev.Seq, scope, ev.Dir, who, ev.Text)
}

// idDedupe is a bounded id-seen set: insertion-ordered, evicting the oldest
// half when it exceeds cap. Cheap and good enough — the server's seq-based
// replay already prevents most duplicates; this guards the reconnect boundary.
type idDedupe struct {
	set   map[string]struct{}
	order []string
	cap   int
}

func newIDDedupe(capacity int) *idDedupe {
	return &idDedupe{set: make(map[string]struct{}, capacity), cap: capacity}
}

func (d *idDedupe) has(id string) bool {
	_, ok := d.set[id]
	return ok
}

func (d *idDedupe) add(id string) {
	d.set[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > d.cap {
		drop := d.cap / 2
		for _, old := range d.order[:drop] {
			delete(d.set, old)
		}
		d.order = append(d.order[:0], d.order[drop:]...)
	}
}
