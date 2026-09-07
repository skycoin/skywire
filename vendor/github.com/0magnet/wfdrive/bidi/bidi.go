// Package bidi drives Waterfox and Firefox over WebDriver BiDi.
//
// Waterfox 6.6 (Firefox ESR 128+) dropped the CDP Remote Agent, so
// chromedp-shaped tooling no longer works against it; the BiDi endpoint is
// built into the browser, so nothing else has to be in the loop — no
// geckodriver, no Selenium, one dependency (github.com/coder/websocket).
//
// The one constraint that shapes this package: Firefox permits ONE active BiDi
// session and does NOT release it when the owning socket drops. A caller that
// dials, works and dies without calling End strands that session until the
// browser restarts. Two things follow, and both are handled here rather than
// left to callers:
//
//   - NewSession waits out a lagging teardown ("Maximum number of active
//     sessions") instead of failing immediately, so a session released a moment
//     ago is not a hard error.
//   - Every operation runs through WithTab, which classifies dropped-tab and
//     dropped-socket errors and repairs in place — reopen the tab, and if that
//     fails redial, re-create the session, reopen the tab, retry once. Closing
//     a tab by hand does not cost a browser restart.
//
// A long-lived Driver is therefore the cheapest way to use this; Serve exposes
// one over a small local HTTP control port for callers that are not Go.
package bidi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Msg is the slice of the BiDi wire shape this package reads.
type Msg struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Error  string          `json:"error,omitempty"`
	Msg    string          `json:"message,omitempty"`
}

// Driver is one BiDi session and one tab, kept alive across commands.
type Driver struct {
	c       *websocket.Conn
	ctx     context.Context
	mu      sync.Mutex
	nextID  int
	waiters map[int]chan Msg
	console []string
	onEvent func(Msg)

	// port and bctx let the driver rebuild itself. The tab is the fragile
	// part: close it in the browser and every later command fails with "no
	// such frame" until a new one is opened, which would otherwise mean
	// restarting — and restarting is exactly what strands Firefox's single
	// BiDi session.
	port string
	bctx string

	endOnce sync.Once
}

// ReadLimit is the websocket read limit; screenshots of a large page are big.
const ReadLimit = 96 << 20

// Connect dials BiDi on port, establishes the single session (waiting out a
// lagging prior teardown), subscribes to console logs and opens one tab.
//
// The caller MUST call End when done, on every exit path including signals: a
// connection that simply drops leaves the session held until the browser is
// restarted.
func Connect(ctx context.Context, port string) (*Driver, error) {
	d := &Driver{ctx: ctx, waiters: map[int]chan Msg{}, port: port}
	if err := d.dial(); err != nil {
		return nil, err
	}
	if err := d.NewSession(); err != nil {
		return nil, err
	}
	if err := d.OpenTab(); err != nil {
		return nil, err
	}
	return d, nil
}

// Tab reports the browsing context this driver is driving.
func (d *Driver) Tab() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.bctx
}

// OnEvent registers a callback for unsolicited BiDi events. Console entries are
// still accumulated for Console regardless; this is for callers that want to
// stream them as they arrive.
func (d *Driver) OnEvent(fn func(Msg)) {
	d.mu.Lock()
	d.onEvent = fn
	d.mu.Unlock()
}

func (d *Driver) pump() {
	for {
		_, data, err := d.c.Read(d.ctx)
		if err != nil {
			return
		}
		var m Msg
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
		d.mu.Lock()
		fn := d.onEvent
		d.mu.Unlock()
		if fn != nil {
			fn(m)
		}
	}
}

// Command sends one BiDi command and waits for its reply.
func (d *Driver) Command(method string, params map[string]interface{}) (json.RawMessage, error) {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	ch := make(chan Msg, 1)
	d.waiters[id] = ch
	c := d.c
	d.mu.Unlock()
	body, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params}) //nolint:errcheck
	if err := c.Write(d.ctx, websocket.MessageText, body); err != nil {
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

// ResetConsole discards accumulated console output.
func (d *Driver) ResetConsole() { d.mu.Lock(); d.console = nil; d.mu.Unlock() }

// Console returns the console output accumulated since the last ResetConsole.
func (d *Driver) Console() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.Join(d.console, "\n")
}

// dial opens the BiDi websocket and starts the reader.
func (d *Driver) dial() error {
	c, _, err := websocket.Dial(d.ctx, "ws://127.0.0.1:"+d.port+"/session", nil)
	if err != nil {
		return fmt.Errorf("BiDi dial: %w", err)
	}
	c.SetReadLimit(ReadLimit)
	d.mu.Lock()
	d.c = c
	d.waiters = map[int]chan Msg{}
	d.mu.Unlock()
	go d.pump()
	return nil
}

// NewSession establishes the one session Firefox allows, waiting out a prior
// teardown that has not landed yet.
func (d *Driver) NewSession() error {
	var serr error
	for i := 0; i < 15; i++ {
		if _, serr = d.Command("session.new", map[string]interface{}{"capabilities": map[string]interface{}{}}); serr == nil {
			_, _ = d.Command("session.subscribe", map[string]interface{}{"events": []string{"log.entryAdded"}}) //nolint:errcheck
			return nil
		}
		if !strings.Contains(serr.Error(), "Maximum number of active") {
			return fmt.Errorf("session.new: %w", serr)
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("session busy — another BiDi client holds it, or a dead one stranded it: %w", serr)
}

// Subscribe adds BiDi events to the session subscription.
func (d *Driver) Subscribe(events ...string) error {
	_, err := d.Command("session.subscribe", map[string]interface{}{"events": events})
	return err
}

// OpenTab opens the tab this driver drives.
func (d *Driver) OpenTab() error {
	cr, err := d.Command("browsingContext.create", map[string]interface{}{"type": "tab"})
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

// Gone reports whether an error means the tab or the whole connection went
// away, rather than the command itself being wrong.
func Gone(err error) bool {
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

// Recover re-opens the tab, and re-dials and re-establishes the session if the
// socket itself has gone. It is the difference between a closed tab being a
// hiccup and being a restart — and since restarting is what strands Firefox's
// single session, recovering in place is what keeps that session healthy.
func (d *Driver) Recover() error {
	if err := d.OpenTab(); err == nil {
		return nil
	}
	if d.c != nil {
		d.c.Close(websocket.StatusNormalClosure, "reconnecting") //nolint:errcheck,gosec
	}
	if err := d.dial(); err != nil {
		return err
	}
	if err := d.NewSession(); err != nil {
		return err
	}
	return d.OpenTab()
}

// WithTab runs fn against the current tab, recovering once if the tab or the
// connection disappeared underneath it.
func (d *Driver) WithTab(fn func(bctx string) error) error {
	err := fn(d.Tab())
	if err == nil || !Gone(err) {
		return err
	}
	if rerr := d.Recover(); rerr != nil {
		return fmt.Errorf("%v (recovery failed: %v)", err, rerr)
	}
	return fn(d.Tab())
}

// EvalIn evaluates expr in a named browsing context and returns the result
// rendered as a string.
func (d *Driver) EvalIn(bctx, expr string) (string, error) {
	res, err := d.Command("script.evaluate", map[string]interface{}{
		"expression": expr, "target": map[string]interface{}{"context": bctx},
		"awaitPromise": true, "resultOwnership": "none",
	})
	if err != nil {
		return "", err
	}
	var r struct {
		Type   string `json:"type"`
		Result struct {
			Value json.RawMessage `json:"value"`
			Type  string          `json:"type"`
		} `json:"result"`
		ExceptionDetails struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(res, &r) //nolint:errcheck
	if r.Type == "exception" {
		return "", fmt.Errorf("script.evaluate: %s", r.ExceptionDetails.Text)
	}
	out := string(r.Result.Value)
	if out == "" {
		out = r.Result.Type
	}
	return strings.Trim(out, `"`), nil
}

// Eval evaluates expr in this driver's tab, recovering the tab if it went away.
func (d *Driver) Eval(expr string) (string, error) {
	var out string
	err := d.WithTab(func(bctx string) error {
		var e error
		out, e = d.EvalIn(bctx, expr)
		return e
	})
	return out, err
}

// Navigate loads url in this driver's tab and waits for the load to complete.
func (d *Driver) Navigate(url string) error {
	return d.WithTab(func(bctx string) error {
		_, e := d.Command("browsingContext.navigate",
			map[string]interface{}{"context": bctx, "url": url, "wait": "complete"})
		return e
	})
}

// Screenshot captures this driver's tab as PNG bytes.
func (d *Driver) Screenshot() ([]byte, error) {
	var png []byte
	err := d.WithTab(func(bctx string) error {
		shot, e := d.Command("browsingContext.captureScreenshot", map[string]interface{}{"context": bctx})
		if e != nil {
			return e
		}
		var s struct {
			Data string `json:"data"`
		}
		if e := json.Unmarshal(shot, &s); e != nil {
			return e
		}
		b, e := base64.StdEncoding.DecodeString(s.Data)
		if e != nil {
			return e
		}
		png = b
		return nil
	})
	return png, err
}

// Health reports whether the session and tab are still usable, recovering them
// if they are not.
func (d *Driver) Health() error {
	return d.WithTab(func(bctx string) error {
		_, e := d.Command("browsingContext.getTree", map[string]interface{}{"root": bctx})
		return e
	})
}

// End releases the session and closes the socket. It is safe to call more than
// once, and it must be called on every exit path: Firefox does not reclaim the
// session when the socket merely drops.
func (d *Driver) End() {
	d.endOnce.Do(func() {
		_, _ = d.Command("session.end", map[string]interface{}{}) //nolint:errcheck
		if d.c != nil {
			d.c.Close(websocket.StatusNormalClosure, "") //nolint:errcheck,gosec
		}
	})
}

// Context is one browsing context in the tree browsingContext.getTree returns.
type Context struct {
	Context  string    `json:"context"`
	URL      string    `json:"url"`
	Children []Context `json:"children"`
}

// Attach connects and establishes the session WITHOUT opening a tab, leaving
// the driver pointed at nothing until UseTab or OpenTab.
//
// This is what a one-shot inspection wants. Connect opens a fresh tab, which is
// right for watching a load and wrong for reading state: a new tab has none of
// the state the caller came to look at, and answers document.title with the
// empty string rather than saying so.
//
// The caller MUST still call End on every exit path.
func Attach(ctx context.Context, port string) (*Driver, error) {
	d := &Driver{ctx: ctx, waiters: map[int]chan Msg{}, port: port}
	if err := d.dial(); err != nil {
		return nil, err
	}
	if err := d.NewSession(); err != nil {
		return nil, err
	}
	return d, nil
}

// Tabs lists the top-level browsing contexts currently open.
func (d *Driver) Tabs() ([]Context, error) {
	res, err := d.Command("browsingContext.getTree", map[string]interface{}{})
	if err != nil {
		return nil, err
	}
	var t struct {
		Contexts []Context `json:"contexts"`
	}
	if err := json.Unmarshal(res, &t); err != nil {
		return nil, err
	}
	return t.Contexts, nil
}

// UseTab points this driver at an existing browsing context.
func (d *Driver) UseTab(bctx string) {
	d.mu.Lock()
	d.bctx = bctx
	d.mu.Unlock()
}

// CloseTab closes the tab this driver is pointed at. A one-shot that opened its
// own tab should call this before End, or every invocation leaves a blank tab
// behind.
func (d *Driver) CloseTab() error {
	bctx := d.Tab()
	if bctx == "" {
		return nil
	}
	_, err := d.Command("browsingContext.close", map[string]interface{}{"context": bctx})
	return err
}

// AttachTab points the driver at a tab that is already open, chosen by a
// substring of its URL — empty takes the first — and returns the URL it
// settled on.
//
// The URL is returned rather than logged because the caller has to say it:
// Firefox has no per-tab address to pass around the way CDP does, so "the first
// tab" is the whole selector by default, and a browser with several tabs open
// would otherwise answer about the wrong one without either side noticing.
func (d *Driver) AttachTab(want string) (string, error) {
	tabs, err := d.Tabs()
	if err != nil {
		return "", fmt.Errorf("list tabs: %w", err)
	}
	if len(tabs) == 0 {
		return "", fmt.Errorf("no tabs open")
	}
	open := make([]string, 0, len(tabs))
	for _, t := range tabs {
		if want == "" || strings.Contains(t.URL, want) {
			d.UseTab(t.Context)
			return t.URL, nil
		}
		open = append(open, t.URL)
	}
	return "", fmt.Errorf("no open tab matches %q; open: %s", want, strings.Join(open, ", "))
}
