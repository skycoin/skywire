// Command tabshot captures a PNG screenshot of an existing CDP page target.
//
//	go run ./scripts/tabshot ws://127.0.0.1:9222/devtools/page/<id> out.png
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/coder/websocket"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: tabshot <webSocketDebuggerUrl> <out.png>")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, os.Args[1], &websocket.DialOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	c.SetReadLimit(64 << 20)
	defer c.Close(websocket.StatusNormalClosure, "")
	req := map[string]interface{}{"id": 1, "method": "Page.captureScreenshot", "params": map[string]interface{}{"format": "png"}}
	b, _ := json.Marshal(req)
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
			ID     int `json:"id"`
			Result struct {
				Data string `json:"data"`
			} `json:"result"`
		}
		if json.Unmarshal(msg, &m) != nil || m.ID != 1 {
			continue
		}
		png, err := base64.StdEncoding.DecodeString(m.Result.Data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "b64:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], png, 0644); err != nil {
			fmt.Fprintln(os.Stderr, "save:", err)
			os.Exit(1)
		}
		fmt.Println("saved", os.Args[2], len(png), "bytes")
		return
	}
}
