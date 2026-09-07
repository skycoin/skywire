// Package clihv cmd/skywire-cli/commands/hv/eval.go c4-vis-cli
// `hv eval` evaluates JavaScript in a browser, over CDP or WebDriver BiDi.
//
// On Chromium/Brave it addresses a SPECIFIC CDP target by its
// webSocketDebuggerUrl — so an OOPIF iframe, a worker or a second wasm instance
// can be inspected directly, which URL-matching (cmd/hvinspect) cannot
// disambiguate when several targets share a URL:
//
//	curl -s localhost:9222/json/list | jq -r '.[] | "\(.type) \(.url) \(.webSocketDebuggerUrl)"'
//	skywire cli hv eval ws://localhost:9222/devtools/page/ABC 'typeof skywireVisor'
//
// On Waterfox/Firefox there is no per-target websocket to address: BiDi is one
// endpoint and one session for the whole browser, so give the debug port and
// the protocol is detected:
//
//	skywire cli hv eval --port 9223 'document.title'
//	skywire cli hv eval --driver 127.0.0.1:9224 'document.title'
//
// Prefer --driver, pointed at a running `hv drive`: Firefox allows one BiDi
// session and does not release it when a socket drops, so a session per
// invocation is a hazard the persistent driver exists to remove. Without it
// this releases the session on every exit path, signals included.
//
// This was cmd/cdpeval, a standalone binary that never shipped with skywire;
// it lives here so it is present wherever the CLI is, alongside `hv shell`
// (which drives the visor terminal) and `hv drive`.
package clihv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/0magnet/wfdrive/bidi"
	"github.com/coder/websocket"
	"github.com/spf13/cobra"
)

var (
	evalTimeout int
	evalPort    string
	evalBrowser string
	evalDriver  string
)

func init() {
	evalCmd.Flags().IntVar(&evalTimeout, "timeout", 30, "seconds to wait for the result")
	evalCmd.Flags().StringVar(&evalPort, "port", "9222", "debug port of the running browser, when no webSocketDebuggerUrl is given")
	evalCmd.Flags().StringVar(&evalBrowser, "browser", "auto", "protocol to speak: auto, cdp (Chromium/Brave) or bidi (Waterfox/Firefox)")
	evalCmd.Flags().StringVar(&bidiTab, "tab", "", "BiDi only: substring of the URL of the already-open tab to attach to; the first tab otherwise")
	evalCmd.Flags().StringVar(&evalDriver, "driver", "", "control port of a running \"hv drive\", e.g. 127.0.0.1:9224 — evaluates through its session instead of opening one")
	RootCmd.AddCommand(evalCmd)
}

var evalCmd = &cobra.Command{
	Use:   "eval [webSocketDebuggerUrl] <js-expression>",
	Short: "Evaluate JavaScript in a browser over CDP or WebDriver BiDi",
	Long: `Evaluate JavaScript in a running browser.

With a webSocketDebuggerUrl it addresses one specific CDP target, which is the
point on Chromium/Brave: several targets in a page can share a URL (OOPIF
iframes, workers, a second wasm instance), and matching by URL cannot tell them
apart. List them with:
  curl -s localhost:9222/json/list | jq -r '.[] | "\(.type) \(.url) \(.webSocketDebuggerUrl)"'

With only an expression it uses --port and detects the protocol: Chromium
answers /json/version, Waterfox/Firefox does not and speaks WebDriver BiDi
instead. On CDP it evaluates in the first page target; on BiDi it drives the
one session Firefox allows.

  skywire cli hv eval ws://localhost:9222/devtools/page/ABC 'typeof skywireVisor'
  skywire cli hv eval --port 9223 'document.title'
  skywire cli hv eval --driver 127.0.0.1:9224 'document.title'

--driver is the safe way to use Firefox repeatedly: it evaluates through a
running "hv drive" session instead of taking the single session for itself.`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(_ *cobra.Command, args []string) {
		if err := evalRun(args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

func evalRun(args []string) error {
	timeout := time.Duration(evalTimeout) * time.Second
	expr := args[len(args)-1]

	if evalDriver != "" {
		body, err := driverGet(evalDriver, "/eval", map[string][]string{"expr": {expr}}, timeout)
		if err != nil {
			return err
		}
		out, err := driverString(body, "eval")
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	}

	// An explicit webSocketDebuggerUrl is a CDP target by construction; BiDi
	// has no such address, so there is nothing to detect.
	if len(args) == 2 {
		return cdpEval(args[0], expr, timeout)
	}

	proto, err := resolveProto(evalBrowser, evalPort)
	if err != nil {
		return err
	}
	if proto == protoBiDi {
		return withBiDi(evalPort, tabCurrent, timeout, func(d *bidi.Driver) error {
			out, err := d.Eval(expr)
			if err != nil {
				return err
			}
			fmt.Println(out)
			return nil
		})
	}
	ws, err := firstPageTarget(evalPort)
	if err != nil {
		return err
	}
	return cdpEval(ws, expr, timeout)
}

// firstPageTarget picks a page target to evaluate in when the caller gave only
// a port. It never opens a tab: `hv eval` is for inspecting state that is
// already there, and a fresh tab has none of it.
func firstPageTarget(port string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+port+"/json/list", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("list targets: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	var targets []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		WS   string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return "", fmt.Errorf("list targets: %w", err)
	}
	for _, t := range targets {
		if t.Type == "page" && t.WS != "" {
			fmt.Fprintf(os.Stderr, "evaluating in %s\n", t.URL)
			return t.WS, nil
		}
	}
	return "", fmt.Errorf("no page target on port %s; pass a webSocketDebuggerUrl to address one directly", port)
}

func cdpEval(wsURL, expr string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("cdp dial: %w", err)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	c.SetReadLimit(64 << 20)

	send := func(id int, method string, params map[string]interface{}) {
		b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params}) //nolint:errcheck
		_ = c.Write(ctx, websocket.MessageText, b)                                                 //nolint:errcheck
	}
	send(1, "Runtime.enable", map[string]interface{}{})
	send(2, "Runtime.evaluate", map[string]interface{}{
		"expression": expr, "awaitPromise": true, "returnByValue": true,
	})
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var m map[string]interface{}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if id, ok := m["id"].(float64); ok && int(id) == 2 {
			b, _ := json.Marshal(m["result"]) //nolint:errcheck
			fmt.Println(string(b))
			return nil
		}
	}
}
