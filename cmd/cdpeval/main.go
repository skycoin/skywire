// Command cdpeval is a throwaway debug helper: evaluate JS on a SPECIFIC CDP
// target (by its webSocketDebuggerUrl), so we can inspect OOPIF iframes / workers
// that hvinspect's URL-matching can't disambiguate. Usage:
//
//	cdpeval <webSocketDebuggerUrl> '<js-expression>'
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
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: cdpeval <wsUrl> <expr>")
		os.Exit(2)
	}
	ws, expr := os.Args[1], os.Args[2]
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, ws, nil)
	if err != nil {
		fmt.Println("ERR dial:", err)
		return
	}
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
			fmt.Println("ERR read:", err)
			return
		}
		var m map[string]interface{}
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if id, ok := m["id"].(float64); ok && int(id) == 2 {
			b, _ := json.Marshal(m["result"]) //nolint:errcheck
			fmt.Println(string(b))
			return
		}
	}
}
