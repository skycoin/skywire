// Package clihv cmd/skywire-cli/commands/hv/notify.go c4-vis-cli
//
// The desktop host-app sink for the visor-global notification hub: subscribes
// to a visor's notification stream and posts each event to THIS machine's
// notification center.
//
// It exists because "the visor" and "the user" are often not the same machine:
//
//   - The visor runs in a container (the docker e2e env) or on a headless box.
//     pkg/osnotify finds no desktop session there and the hub's host-OS tier is
//     dead, so notifications silently drop no matter what the apps publish.
//   - You browse a visor's app UI over the LAN. Plain http on a non-loopback
//     address is not a secure context, so the browser's Notification API is
//     unavailable and the UI can't alert you either — and the visor's own
//     host-OS notification would pop on the visor's machine, where nobody is
//     sitting.
//
// Run this where the user actually is and both gaps close. It is the desktop
// counterpart of the Android foreground service, speaking the same SSE contract
// (see pkg/visor/hypervisor_handlers_notify.go), which is exactly what the
// hub's host-app tier was designed for.
//
// While it is connected the visor stops posting its own host-OS notifications
// (subscriber presence outranks the OS sink), so events are never doubled.
package clihv

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/osnotify"
	"github.com/skycoin/skywire/pkg/skyenv"
)

var (
	notifyAddr      string
	notifyUser      string
	notifyPass      string
	notifyPrintOnly bool
)

func init() {
	notifyCmd.Flags().StringVarP(&notifyAddr, "addr", "a", "http://127.0.0.1:8000", "hypervisor API address")
	notifyCmd.Flags().StringVarP(&notifyUser, "user", "u", "", "username, when the hypervisor has auth enabled")
	notifyCmd.Flags().StringVarP(&notifyPass, "pass", "p", "", "password, when the hypervisor has auth enabled")
	notifyCmd.Flags().BoolVar(&notifyPrintOnly, "print", false, "print events instead of posting them to the OS (for testing/headless use)")
	RootCmd.AddCommand(notifyCmd)
}

var notifyCmd = &cobra.Command{
	Use:   "notify",
	Short: "Show a visor's app notifications on this machine",
	Long: `Subscribe to a visor's notification stream and post each event to this
machine's notification center.

Use it when the visor is somewhere the user is not — a container, a headless
box, or a machine you only reach over the network. While it is connected the
visor stops posting its own host-OS notifications, so nothing is doubled.

Examples:
  skywire-cli hv notify
  skywire-cli hv notify --addr http://127.0.0.1:8000
  skywire-cli hv notify --addr https://hv.example.com -u admin -p secret
  skywire-cli hv notify --print          # just print, don't touch the OS`,
	RunE: func(cmd *cobra.Command, _ []string) error {
		// Ctrl-C must unsubscribe promptly: a lingering subscriber keeps the
		// visor's own host-OS sink suppressed.
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		base := strings.TrimSuffix(notifyAddr, "/")

		jar, err := cookiejar.New(nil)
		if err != nil {
			return fmt.Errorf("cookie jar: %w", err)
		}
		// No client Timeout: this is an endless stream, and a timeout here
		// would sever it exactly like the server-side one the endpoint had to
		// opt out of.
		client := &http.Client{Jar: jar}

		if notifyUser != "" {
			if err := notifyLogin(ctx, client, base); err != nil {
				return err
			}
		}

		if !notifyPrintOnly && !osnotify.Available() {
			cmd.PrintErrln("warning: no desktop notification backend on THIS machine either — " +
				"notifications will be printed only. Run this where you have a desktop session.")
			notifyPrintOnly = true
		}

		cmd.Printf("Listening for notifications from %s (Ctrl-C to stop)\n", base)

		// Reconnect forever: a visor restart, a laptop sleeping, or a dropped
		// link should not end the session. Backoff keeps a down visor from
		// becoming a busy loop.
		backoff := time.Second
		for ctx.Err() == nil {
			err := notifyStream(ctx, client, base, cmd)
			if ctx.Err() != nil {
				break
			}
			if err != nil {
				cmd.PrintErrf("stream ended (%v); retrying in %s\n", err, backoff)
			}
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
			}
		}
		cmd.Println("Stopped.")
		return nil
	},
}

// notifyLogin exchanges credentials for the session cookie the stream endpoint
// requires when the hypervisor runs with auth enabled. The cookie lands in the
// client's jar, so the subsequent GET carries it automatically.
func notifyLogin(ctx context.Context, client *http.Client, base string) error {
	body, err := json.Marshal(map[string]string{"username": notifyUser, "password": notifyPass})
	if err != nil {
		return fmt.Errorf("encode login: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/login", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body close on a short request

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login failed: %s", resp.Status)
	}
	return nil
}

// notifyEvent mirrors the SSE wire shape the hub emits.
type notifyEvent struct {
	App   string `json:"app"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag"`
}

// notifyStream holds one connection open, posting every event it receives. It
// returns when the stream ends, the context is canceled, or the connection
// fails — the caller reconnects.
func notifyStream(ctx context.Context, client *http.Client, base string, cmd *cobra.Command) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/notifications/stream", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck // stream teardown

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("unauthorized — the hypervisor has auth enabled; pass --user/--pass")
		}
		return fmt.Errorf("unexpected status %s", resp.Status)
	}

	// Long lines are possible (a title plus a 200-char body), so give the
	// scanner room rather than letting it error out mid-stream.
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 4096), 64*1024)

	for sc.Scan() {
		line := sc.Text()
		// Comments (": ping") and the blank line between events carry no data.
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev notifyEvent
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			continue // a frame we don't understand is not worth dying over
		}
		deliverNotifyEvent(ev, cmd)
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return fmt.Errorf("closed by server")
}

// deliverNotifyEvent posts one event to this machine, or prints it in --print
// mode. The title carries the app name so a user can tell at a glance which app
// spoke — the OS sink's own app label is not always visible.
func deliverNotifyEvent(ev notifyEvent, cmd *cobra.Command) {
	title := ev.Title
	if title == "" {
		title = "Skywire"
	}
	if notifyPrintOnly {
		cmd.Printf("%s  [%s] %s — %s\n", time.Now().Format("15:04:05"), ev.App, title, ev.Body)
		return
	}
	err := osnotify.Notify(osnotify.Notification{
		Title: title,
		Body:  ev.Body,
		// Same mapping the visor's own host-OS sink uses, so an event looks
		// identical whether it was posted locally or bridged from elsewhere.
		AppName: skyenv.AppDisplayName(ev.App),
	})
	if err != nil {
		cmd.PrintErrf("could not post notification: %v\n", err)
	}
}
