// Package main cmd/wfdrive/main.go c4-vis-cli
// wfdrive — inspector/driver for Waterfox/Firefox, the BiDi analog of
// cmd/hvinspect (which drives Brave/Chromium over CDP). Waterfox 6.6 (Firefox
// ESR 128+) dropped the CDP Remote Agent but still speaks WebDriver BiDi, so this
// drives it over the BiDi websocket instead — using the main module's already-
// vendored github.com/coder/websocket (no new dependency, so unlike hvinspect it
// lives in the main module rather than its own).
//
// Firefox BiDi permits ONE active session and does NOT release it when the owning
// socket drops, so one-shot invocations churn into "Maximum number of active
// sessions". wfdrive therefore runs as a PERSISTENT driver (like geckodriver):
// one long-lived session + tab, driven over a tiny local HTTP control port.
//
// Attach to a Waterfox started with:
//
//	waterfox --remote-debugging-port=9223 --remote-allow-origins=*
//
// Serve mode (persistent — run in the background, then curl it):
//
//	wfdrive serve 9223 127.0.0.1:9224
//	curl 'http://127.0.0.1:9224/nav?url=https://magnetosphere.net/&wait=10&out=/tmp/mag'
//	curl 'http://127.0.0.1:9224/nav?url=...&eval=<js>&out=/tmp/x'
//	curl  http://127.0.0.1:9224/quit          # session.end + exit
//
// /nav writes <out>.png + <out>.console.txt and returns JSON. Testing through
// Waterfox exercises the REAL client path (dmsg/skynet through the host visor's
// resolving SOCKS5 proxy), unlike the in-UI iframe browser.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
)

type bidiMsg struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Error  string          `json:"error,omitempty"`
	Msg    string          `json:"message,omitempty"`
}

type driver struct {
	c       *websocket.Conn
	ctx     context.Context
	mu      sync.Mutex
	nextID  int
	waiters map[int]chan bidiMsg
	console []string
}

func (d *driver) pump() {
	for {
		_, data, err := d.c.Read(d.ctx)
		if err != nil {
			return
		}
		var m bidiMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			d.mu.Lock()
			ch := d.waiters[m.ID]
			delete(d.waiters, m.ID)
			d.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		if m.Method == "log.entryAdded" {
			var p struct{ Level, Text string }
			_ = json.Unmarshal(m.Params, &p) //nolint:errcheck
			d.mu.Lock()
			d.console = append(d.console, fmt.Sprintf("[%s] %s", p.Level, p.Text))
			d.mu.Unlock()
		}
	}
}

func (d *driver) cmd(method string, params map[string]interface{}) (json.RawMessage, error) {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	ch := make(chan bidiMsg, 1)
	d.waiters[id] = ch
	d.mu.Unlock()
	body, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params}) //nolint:errcheck
	if err := d.c.Write(d.ctx, websocket.MessageText, body); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if m.Error != "" {
			return nil, fmt.Errorf("%s: %s %s", method, m.Error, m.Msg)
		}
		return m.Result, nil
	case <-d.ctx.Done():
		return nil, d.ctx.Err()
	}
}

func (d *driver) resetConsole() { d.mu.Lock(); d.console = nil; d.mu.Unlock() }
func (d *driver) dumpConsole() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.console, "\n")
}

// connect dials BiDi, establishes the single session (waiting out a lagging
// prior teardown), subscribes to console logs, and opens one tab to drive.
func connect(ctx context.Context, port string) (*driver, string, error) {
	c, _, err := websocket.Dial(ctx, "ws://127.0.0.1:"+port+"/session", nil)
	if err != nil {
		return nil, "", fmt.Errorf("BiDi dial: %w", err)
	}
	c.SetReadLimit(96 << 20)
	d := &driver{c: c, ctx: ctx, waiters: map[int]chan bidiMsg{}}
	go d.pump()
	var serr error
	for i := 0; i < 15; i++ {
		if _, serr = d.cmd("session.new", map[string]interface{}{"capabilities": map[string]interface{}{}}); serr == nil {
			break
		}
		if !strings.Contains(serr.Error(), "Maximum number of active") {
			return nil, "", fmt.Errorf("session.new: %w", serr)
		}
		time.Sleep(2 * time.Second)
	}
	if serr != nil {
		return nil, "", fmt.Errorf("session busy (restart Waterfox once to clear a stuck session): %w", serr)
	}
	_, _ = d.cmd("session.subscribe", map[string]interface{}{"events": []string{"log.entryAdded"}}) //nolint:errcheck
	var bctx string
	if cr, e := d.cmd("browsingContext.create", map[string]interface{}{"type": "tab"}); e == nil {
		var cc struct {
			Context string `json:"context"`
		}
		_ = json.Unmarshal(cr, &cc) //nolint:errcheck
		bctx = cc.Context
	}
	if bctx == "" {
		return nil, "", fmt.Errorf("could not create a browsing context")
	}
	return d, bctx, nil
}

func (d *driver) evalStr(bctx, expr string) string {
	res, e := d.cmd("script.evaluate", map[string]interface{}{
		"expression": expr, "target": map[string]interface{}{"context": bctx},
		"awaitPromise": true, "resultOwnership": "none",
	})
	if e != nil {
		return "ERROR: " + e.Error()
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
			Type  string          `json:"type"`
		} `json:"result"`
	}
	_ = json.Unmarshal(res, &r) //nolint:errcheck
	out := string(r.Result.Value)
	if out == "" {
		out = r.Result.Type
	}
	return strings.Trim(out, `"`)
}

func (d *driver) shoot(bctx, outPrefix string) {
	if shot, e := d.cmd("browsingContext.captureScreenshot", map[string]interface{}{"context": bctx}); e == nil {
		var s struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(shot, &s) //nolint:errcheck
		if b, de := base64.StdEncoding.DecodeString(s.Data); de == nil {
			_ = os.WriteFile(outPrefix+".png", b, 0o644) //nolint:errcheck,gosec
		}
	}
}

func serveMode() error {
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: wfdrive serve <bidiPort> <ctrlAddr>")
	}
	port, ctrlAddr := os.Args[2], os.Args[3]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d, bctx, err := connect(ctx, port)
	if err != nil {
		return err
	}
	end := func() {
		_, _ = d.cmd("session.end", map[string]interface{}{}) //nolint:errcheck
		d.c.Close(websocket.StatusNormalClosure, "")          //nolint:errcheck,gosec
	}

	srv := &http.Server{Addr: ctrlAddr, ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()
	mux.HandleFunc("/nav", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		wait := 8
		if n, e := strconv.Atoi(q.Get("wait")); e == nil {
			wait = n
		}
		out := q.Get("out")
		if out == "" {
			out = "/tmp/wfdrive"
		}
		d.resetConsole()
		if u := q.Get("url"); u != "" {
			if _, e := d.cmd("browsingContext.navigate", map[string]interface{}{"context": bctx, "url": u, "wait": "complete"}); e != nil {
				_, _ = fmt.Fprintf(w, `{"navWarning":%q}`+"\n", e.Error()) //nolint:errcheck,gosec
			}
		}
		time.Sleep(time.Duration(wait) * time.Second)
		evalOut := ""
		if ev := q.Get("eval"); ev != "" {
			evalOut = d.evalStr(bctx, ev)
		}
		d.shoot(bctx, out)
		con := d.dumpConsole()
		_ = os.WriteFile(out+".console.txt", []byte(con+"\n"), 0o644)                                         //nolint:errcheck,gosec
		resp, _ := json.Marshal(map[string]interface{}{"png": out + ".png", "eval": evalOut, "console": con}) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp) //nolint:errcheck
	})
	mux.HandleFunc("/quit", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bye\n"))                                            //nolint:errcheck
		go func() { time.Sleep(200 * time.Millisecond); end(); _ = srv.Close() }() //nolint:errcheck
	})
	srv.Handler = mux

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sig; end(); _ = srv.Close() }() //nolint:errcheck

	fmt.Printf("wfdrive serving control on http://%s (BiDi :%s, tab %s)\n", ctrlAddr, port, bctx)
	if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		end()
		return e
	}
	return nil
}

func main() {
	if len(os.Args) >= 2 && os.Args[1] == "serve" {
		if err := serveMode(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	fmt.Fprintln(os.Stderr, "usage: wfdrive serve <bidiPort> <ctrlAddr>   (persistent driver)")
	os.Exit(2)
}
