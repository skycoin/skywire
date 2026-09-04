// Command jseval evaluates a JS expression in a CDP page target and prints

// the JSON result — direct Runtime.evaluate, no serve-harness needed.
//
//	go run ./scripts/tabshot/jseval ws://127.0.0.1:9222/devtools/page/<id> <expr>
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
		fmt.Fprintln(os.Stderr, "usage: jseval <webSocketDebuggerUrl> <expr>")
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
		"id": 1, "method": "Runtime.evaluate",
		"params": map[string]interface{}{"expression": os.Args[2], "returnByValue": true, "awaitPromise": true},
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
			fmt.Println(string(m.Result))
			return
		}
	}
}
