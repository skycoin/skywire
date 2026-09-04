// Command profile samples a CDP page target's JS CPU profile for N seconds and
// prints the hottest functions by self time — the diagnostic for "the wasm desk
// tab pegs a core": a spin loop names itself at the top of this list, where a
// screenshot or eval cannot (evals queue behind the spin; the profiler runs in
// the inspector and samples through it).
//
//	go run ./scripts/tabshot/profile ws://127.0.0.1:9222/devtools/page/<id> <seconds>
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

type node struct {
	ID       int `json:"id"`
	HitCount int `json:"hitCount"`
	Frame    struct {
		FunctionName string `json:"functionName"`
		URL          string `json:"url"`
		LineNumber   int    `json:"lineNumber"`
	} `json:"callFrame"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: profile <webSocketDebuggerUrl> <seconds>")
		os.Exit(2)
	}
	secs, err := strconv.Atoi(os.Args[2])
	if err != nil || secs < 1 || secs > 120 {
		fmt.Fprintln(os.Stderr, "seconds must be 1-120")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(secs+60)*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, os.Args[1], &websocket.DialOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "dial:", err)
		os.Exit(1)
	}
	c.SetReadLimit(256 << 20) // profiles of a busy tab run tens of MB
	defer c.Close(websocket.StatusNormalClosure, "")

	send := func(id int, method string, params map[string]interface{}) {
		b, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params})
		if err := c.Write(ctx, websocket.MessageText, b); err != nil {
			fmt.Fprintln(os.Stderr, "write:", err)
			os.Exit(1)
		}
	}
	// waitID reads until the reply for id arrives (events interleave freely).
	waitID := func(id int) json.RawMessage {
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
			if json.Unmarshal(msg, &m) == nil && m.ID == id {
				return m.Result
			}
		}
	}

	send(1, "Profiler.enable", nil)
	waitID(1)
	// 1ms sampling: fine enough to catch a hot loop, cheap enough not to be one.
	send(2, "Profiler.setSamplingInterval", map[string]interface{}{"interval": 1000})
	waitID(2)
	send(3, "Profiler.start", nil)
	waitID(3)
	time.Sleep(time.Duration(secs) * time.Second)
	send(4, "Profiler.stop", nil)
	res := waitID(4)

	var prof struct {
		Profile struct {
			Nodes     []node `json:"nodes"`
			StartTime int64  `json:"startTime"`
			EndTime   int64  `json:"endTime"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(res, &prof); err != nil {
		fmt.Fprintln(os.Stderr, "profile parse:", err)
		os.Exit(1)
	}
	nodes := prof.Profile.Nodes
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].HitCount > nodes[j].HitCount })
	// V8 meta-nodes ((idle), (program), (garbage collector), (root)) are not
	// script execution — counting them as "busy" turns an idle tab into a
	// false spin alarm, so utilization is computed from real frames only.
	meta := map[string]bool{"(idle)": true, "(program)": true, "(garbage collector)": true, "(root)": true}
	total, busy := 0, 0
	for _, n := range nodes {
		total += n.HitCount
		if !meta[n.Frame.FunctionName] {
			busy += n.HitCount
		}
	}
	wallMs := float64(prof.Profile.EndTime-prof.Profile.StartTime) / 1000
	fmt.Printf("samples=%d wall=%.0fms (≈%.0f%% of one core busy)\n", busy, wallMs,
		100*float64(busy)/(wallMs)) // 1 sample/ms ⇒ samples/ms ≈ core utilization
	shown := 0
	for _, n := range nodes {
		if shown >= 70 || n.HitCount == 0 {
			break
		}
		if meta[n.Frame.FunctionName] {
			continue
		}
		shown++
		name := n.Frame.FunctionName
		if name == "" {
			name = "(anonymous)"
		}
		fmt.Printf("%6d  %5.1f%%  %s  %s:%d\n", n.HitCount, 100*float64(n.HitCount)/float64(total),
			name, n.Frame.URL, n.Frame.LineNumber)
	}
}
