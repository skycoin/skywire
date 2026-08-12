// Package clihv cmd/skywire-cli/commands/hv/eval.go c4-vis-cli
// `hv eval` evaluates JavaScript on a SPECIFIC CDP target, addressed by its
// webSocketDebuggerUrl — so an OOPIF iframe, a worker or a second wasm
// instance can be inspected directly, which URL-matching (cmd/hvinspect)
// cannot disambiguate when several targets share a URL.
//
// List the targets first:
//
//	curl -s localhost:9222/json/list | jq -r '.[] | "\(.type) \(.url) \(.webSocketDebuggerUrl)"'
//	skywire cli hv eval ws://localhost:9222/devtools/page/ABC 'typeof skywireVisor'
//
// This was cmd/cdpeval, a standalone binary that never shipped with skywire;
// it lives here so it is present wherever the CLI is, alongside `hv shell`
// (which drives the visor terminal) and the Waterfox rig.
package clihv

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
)

var evalTimeout int

func init() {
	evalCmd.Flags().IntVar(&evalTimeout, "timeout", 30, "seconds to wait for the result")
	RootCmd.AddCommand(evalCmd)
}

var evalCmd = &cobra.Command{
	Use:   "eval <webSocketDebuggerUrl> <js-expression>",
	Short: "Evaluate JavaScript on a specific CDP target",
	Long: `Evaluate JavaScript on a specific CDP target, addressed by its
webSocketDebuggerUrl.

Addressing the target directly is the point: several targets in a page can
share a URL (OOPIF iframes, workers, a second wasm instance), and matching by
URL cannot tell them apart.

List them with:
  curl -s localhost:9222/json/list | jq -r '.[] | "\(.type) \(.url) \(.webSocketDebuggerUrl)"'`,
	Args: cobra.ExactArgs(2),
	Run: func(_ *cobra.Command, args []string) {
		if err := cdpEval(args[0], args[1], time.Duration(evalTimeout)*time.Second); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
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
