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
//	curl 'http://127.0.0.1:9224/eval?expr=<js>'   # evaluate, no navigation
//	curl 'http://127.0.0.1:9224/shoot?out=/tmp/x' # screenshot as it stands
//	curl  http://127.0.0.1:9224/health            # session + tab usable?
//	curl  http://127.0.0.1:9224/quit              # session.end + exit
//
// /nav, /shoot and /eval write <out>.png + <out>.console.txt and return JSON.
// Splitting navigation, evaluation and capture apart matters for anything that
// has to settle between steps: /nav evaluates immediately before it shoots, so
// a test that needs to toggle something and wait would otherwise have to cram
// the whole sequence into one expression.
//
// The driver heals itself. Closing its tab in the browser, or losing the
// socket, used to brick it until a restart — and restarting is precisely what
// strands the single session, so a closed tab could cost a browser restart.
// Every operation now runs through withTab, which re-opens the tab (and
// re-establishes the session if the connection went too) and retries once. Testing through
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

	// port and bctx let the driver rebuild itself. The tab is the fragile
	// part: close it in the browser and every later command fails with "no
	// such frame" until a new one is opened, which used to mean restarting
	// wfdrive — and restarting is exactly what strands Firefox's single BiDi
	// session. Holding them here lets recover() re-open a tab, or re-dial and
	// re-establish the session, in place.
	port string
	bctx string
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
	d := &driver{ctx: ctx, waiters: map[int]chan bidiMsg{}, port: port}
	if err := d.dial(); err != nil {
		return nil, "", err
	}
	if err := d.newSession(); err != nil {
		return nil, "", err
	}
	if err := d.openTab(); err != nil {
		return nil, "", err
	}
	return d, d.bctx, nil
}

// dial opens the BiDi websocket and starts the reader.
func (d *driver) dial() error {
	c, _, err := websocket.Dial(d.ctx, "ws://127.0.0.1:"+d.port+"/session", nil)
	if err != nil {
		return fmt.Errorf("BiDi dial: %w", err)
	}
	c.SetReadLimit(96 << 20)
	d.mu.Lock()
	d.c = c
	d.waiters = map[int]chan bidiMsg{}
	d.mu.Unlock()
	go d.pump()
	return nil
}

// newSession establishes the one session Firefox allows, waiting out a prior
// teardown that has not landed yet.
func (d *driver) newSession() error {
	var serr error
	for i := 0; i < 15; i++ {
		if _, serr = d.cmd("session.new", map[string]interface{}{"capabilities": map[string]interface{}{}}); serr == nil {
			_, _ = d.cmd("session.subscribe", map[string]interface{}{"events": []string{"log.entryAdded"}}) //nolint:errcheck
			return nil
		}
		if !strings.Contains(serr.Error(), "Maximum number of active") {
			return fmt.Errorf("session.new: %w", serr)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("session busy (restart Waterfox once to clear a stuck session): %w", serr)
}

// openTab opens the tab this driver drives.
func (d *driver) openTab() error {
	cr, err := d.cmd("browsingContext.create", map[string]interface{}{"type": "tab"})
	if err != nil {
		return fmt.Errorf("browsingContext.create: %w", err)
	}
	var cc struct {
		Context string `json:"context"`
	}
	if e := json.Unmarshal(cr, &cc); e != nil || cc.Context == "" {
		return fmt.Errorf("could not create a browsing context")
	}
	d.mu.Lock()
	d.bctx = cc.Context
	d.mu.Unlock()
	return nil
}

// gone reports whether an error means the tab or the whole connection went
// away, rather than the command itself being wrong.
func gone(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, s := range []string{
		"no such frame", "no such node", "browsing context",
		"invalid session id", "failed to write", "broken pipe",
		"use of closed", "websocket", "eof", "context canceled",
	} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// recover re-opens the tab, and re-dials and re-establishes the session if the
// socket itself has gone. It is the difference between a closed tab being a
// hiccup and being a restart — and since restarting is what leaks Firefox's
// single session, recovering in place is what keeps that session healthy.
func (d *driver) recover() error {
	if err := d.openTab(); err == nil {
		return nil
	}
	if d.c != nil {
		d.c.Close(websocket.StatusNormalClosure, "reconnecting") //nolint:errcheck,gosec
	}
	if err := d.dial(); err != nil {
		return err
	}
	if err := d.newSession(); err != nil {
		return err
	}
	return d.openTab()
}

// withTab runs fn against the current tab, recovering once if the tab or the
// connection disappeared underneath it.
func (d *driver) withTab(fn func(bctx string) error) error {
	err := fn(d.bctx)
	if err == nil || !gone(err) {
		return err
	}
	if rerr := d.recover(); rerr != nil {
		return fmt.Errorf("%v (recovery failed: %v)", err, rerr)
	}
	return fn(d.bctx)
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
	// Release the session on EVERY exit path, not just the graceful ones.
	// Firefox allows one BiDi session and does not reclaim it when the socket
	// drops, so a wfdrive that dies after connecting — a port already in use,
	// say — strands that session until the browser is restarted. endOnce makes
	// returning early cost nothing.
	var endOnce sync.Once
	end := func() {
		endOnce.Do(func() {
			_, _ = d.cmd("session.end", map[string]interface{}{}) //nolint:errcheck
			d.c.Close(websocket.StatusNormalClosure, "")          //nolint:errcheck,gosec
		})
	}
	defer end()

	srv := &http.Server{Addr: ctrlAddr, ReadHeaderTimeout: 5 * time.Second}
	mux := http.NewServeMux()

	// report writes the screenshot + console for a step and answers as JSON.
	report := func(w http.ResponseWriter, out, evalOut string, warn error) {
		if out == "" {
			out = "/tmp/wfdrive"
		}
		_ = d.withTab(func(bctx string) error { d.shoot(bctx, out); return nil }) //nolint:errcheck
		con := d.dumpConsole()
		_ = os.WriteFile(out+".console.txt", []byte(con+"\n"), 0o644) //nolint:errcheck,gosec
		body := map[string]interface{}{"png": out + ".png", "eval": evalOut, "console": con}
		if warn != nil {
			body["warning"] = warn.Error()
		}
		resp, _ := json.Marshal(body) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp) //nolint:errcheck
	}
	mux.HandleFunc("/nav", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		wait := 8
		if n, e := strconv.Atoi(q.Get("wait")); e == nil {
			wait = n
		}
		d.resetConsole()
		var warn error
		if u := q.Get("url"); u != "" {
			warn = d.withTab(func(bctx string) error {
				_, e := d.cmd("browsingContext.navigate",
					map[string]interface{}{"context": bctx, "url": u, "wait": "complete"})
				return e
			})
		}
		time.Sleep(time.Duration(wait) * time.Second)
		evalOut := ""
		if ev := q.Get("eval"); ev != "" {
			evalOut = d.evalStr(d.bctx, ev)
		}
		report(w, q.Get("out"), evalOut, warn)
	})

	// /shoot screenshots the tab as it stands, without navigating — so a test
	// can drive the page through several steps (settle, toggle, settle) and
	// capture each one, instead of having to fold everything into the single
	// eval that /nav runs immediately before its shot.
	mux.HandleFunc("/shoot", func(w http.ResponseWriter, r *http.Request) {
		report(w, r.URL.Query().Get("out"), "", nil)
	})

	// /eval evaluates in the tab without navigating or resetting the console.
	mux.HandleFunc("/eval", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		out := ""
		_ = d.withTab(func(bctx string) error { //nolint:errcheck
			out = d.evalStr(bctx, q.Get("expr"))
			if strings.HasPrefix(out, "ERROR: ") {
				return fmt.Errorf("%s", strings.TrimPrefix(out, "ERROR: "))
			}
			return nil
		})
		resp, _ := json.Marshal(map[string]interface{}{"eval": out}) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp) //nolint:errcheck
	})

	// /health reports whether the session and tab are still usable, recovering
	// them if not.
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		err := d.withTab(func(bctx string) error {
			_, e := d.cmd("browsingContext.getTree", map[string]interface{}{"root": bctx})
			return e
		})
		status := "ok"
		if err != nil {
			status = "unhealthy: " + err.Error()
		}
		resp, _ := json.Marshal(map[string]interface{}{"status": status, "tab": d.bctx}) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(resp) //nolint:errcheck
	})

	mux.HandleFunc("/quit", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("bye\n"))                                            //nolint:errcheck
		go func() { time.Sleep(200 * time.Millisecond); end(); _ = srv.Close() }() //nolint:errcheck
	})
	srv.Handler = mux

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)
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
