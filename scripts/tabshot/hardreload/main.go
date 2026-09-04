// Command hardreload reloads a CDP page target bypassing the HTTP cache —
// what a stale cached bundle needs after the server was rebuilt.
//
//	go run ./scripts/tabshot/hardreload ws://127.0.0.1:9222/devtools/page/<id>
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: hardreload <webSocketDebuggerUrl>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, os.Args[1], &websocket.DialOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck // best-effort CDP goodbye
	send := func(id int, method string, params interface{}) {
		p, _ := json.Marshal(params)                                                                           //nolint:errcheck // static params cannot fail to marshal
		b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": json.RawMessage(p)}) //nolint:errcheck // static envelope cannot fail to marshal
		_ = c.Write(ctx, websocket.MessageText, b)                                                             //nolint:errcheck
	}
	send(1, "Page.enable", map[string]interface{}{})
	send(2, "Page.reload", map[string]interface{}{"ignoreCache": true})
	// Wait for the reload command's ack (id 2), then done.
	for {
		_, raw, err := c.Read(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			os.Exit(1)
		}
		var m struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(raw, &m) == nil && m.ID == 2 {
			fmt.Println("hard-reloaded")
			return
		}
	}
}
