// Command evallat times CDP Runtime.evaluate round-trips on one open socket,
// isolating the evaluate from process startup and dial cost.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/coder/websocket"
)

func main() {
	n := 40
	if len(os.Args) > 2 {
		if v, err := strconv.Atoi(os.Args[2]); err == nil && v > 0 {
			n = v
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, os.Args[1], &websocket.DialOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	defer c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck

	var ms []float64
	for i := 1; i <= n; i++ {
		b, merr := json.Marshal(map[string]interface{}{
			"id": i, "method": "Runtime.evaluate",
			"params": map[string]interface{}{"expression": "1+1", "returnByValue": true},
		})
		if merr != nil {
			fmt.Fprintln(os.Stderr, "marshal:", merr)
			os.Exit(1)
		}
		st := time.Now()
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
				ID int `json:"id"`
			}
			if json.Unmarshal(msg, &m) == nil && m.ID == i {
				break
			}
		}
		ms = append(ms, float64(time.Since(st).Microseconds())/1000)
		time.Sleep(200 * time.Millisecond)
	}
	sort.Float64s(ms)
	var sum float64
	for _, v := range ms {
		sum += v
	}
	over := 0
	for _, v := range ms {
		if v > 100 {
			over++
		}
	}
	fmt.Printf("n=%d  min=%.1fms  p50=%.1fms  p95=%.1fms  max=%.1fms  mean=%.1fms  over_100ms=%d\n",
		len(ms), ms[0], ms[len(ms)/2], ms[len(ms)*95/100], ms[len(ms)-1], sum/float64(len(ms)), over)
}
