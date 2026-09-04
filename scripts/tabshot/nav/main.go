// Command nav navigates a CDP page target to a URL — the missing half of
// hardreload for driving a fresh tab (PUT /json/new?url= no longer navigates
// in current Chromium).
//
//	go run ./scripts/tabshot/nav ws://127.0.0.1:9222/devtools/page/<id> <url>
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
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: nav <webSocketDebuggerUrl> <url>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, os.Args[1], &websocket.DialOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck
	b, err := json.Marshal(map[string]interface{}{
		"id": 1, "method": "Page.navigate",
		"params": map[string]interface{}{"url": os.Args[2]},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "marshal:", err)
		os.Exit(1)
	}
	if err := c.Write(ctx, websocket.MessageText, b); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}
	for {
		_, msg, err := c.Read(ctx)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read:", err)
			os.Exit(1)
		}
		var m struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal(msg, &m) == nil && m.ID == 1 {
			fmt.Println("navigated:", string(m.Result))
			return
		}
	}
}
