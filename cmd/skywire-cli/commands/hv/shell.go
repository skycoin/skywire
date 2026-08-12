// Package clihv cmd/skywire-cli/commands/hv/shell.go c4-vis-cli
// `hv shell` drives the visor shell in a Chromium/Brave tab over CDP: it opens
// the hypervisor UI, opens the console window, types commands into the
// terminal and captures what came back.
//
// It exists because the other browser rigs cannot type. cmd/hvinspect and
// cmd/cdpeval evaluate JS, and cmd/wfdrive drives Waterfox over BiDi — but the
// visor shell is an xterm canvas fed by keyboard events, so verifying that a
// shell applet works means synthesizing real key events and reading the result
// back. Doing that by hand, or from a throwaway script, is how a regression in
// an applet goes unnoticed.
//
// Attach to a browser started with --remote-debugging-port=9222:
//
//	skywire cli hv shell --url http://127.0.0.1:7998/ pk aliases
//	skywire cli hv shell --url http://127.0.0.1:8123/ --no-console 'echo hi'
//
// Each argument is one command line typed into the shell. The screenshot and
// the page console go to --out, and every console error and uncaught exception
// is printed, because a hung applet looks exactly like a working one in a
// screenshot until you read the log.
package clihv

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
)

var (
	shellPort      string
	shellURL       string
	shellOut       string
	shellLoad      int
	shellBoot      int
	shellSettle    int
	shellNoConsole bool
	shellLogAll    bool
	shellKeepTabs  bool
)

func init() {
	shellCmd.Flags().StringVar(&shellPort, "port", "9222", "CDP port of a running Chromium/Brave")
	shellCmd.Flags().StringVar(&shellURL, "url", "http://127.0.0.1:7998/", "page to open")
	shellCmd.Flags().StringVar(&shellOut, "out", "/tmp/hvshell", "screenshot + console output prefix")
	shellCmd.Flags().IntVar(&shellLoad, "load", 30, "seconds to wait for the page to load")
	shellCmd.Flags().IntVar(&shellBoot, "boot", 20, "seconds to wait for the shell wasm after opening the console")
	shellCmd.Flags().IntVar(&shellSettle, "settle", 8, "seconds to wait after each command")
	shellCmd.Flags().BoolVar(&shellNoConsole, "no-console", false, "skip opening the visor console window")
	shellCmd.Flags().BoolVar(&shellKeepTabs, "keep-tabs", false, "leave other tabs on the same origin open; by default they are closed first, because the visor is one SharedWorker per origin while every tab spawns its own shell instance against it")
	shellCmd.Flags().BoolVar(&shellLogAll, "log-all", false, "record every console level, not just errors and warnings — a wasm module's fatal error is written at log level")
	RootCmd.AddCommand(shellCmd)
}

// waitFor polls expr until it evaluates to "true" or the deadline passes, and
// reports whether the condition was met. Polling beats sleeping: the things
// being waited for here take milliseconds when they work, so a fixed sleep is
// either far too long (every run costs minutes) or too short (a working
// command is misread as broken) — and both mistakes were made before this
// existed.
func (d *cdp) waitFor(expr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if d.eval(expr) == "true" {
			return true
		}
		time.Sleep(150 * time.Millisecond)
	}
	return false
}

// doneMarker is appended to each typed command so the tool can tell when the
// shell has finished it, instead of guessing with a sleep. The shell runs it
// after the command whatever the command did, so a failing command is still
// detected as finished.
const doneMarker = `; js "window.__hvshellDone=(window.__hvshellDone||0)+1" >/dev/null`

var shellCmd = &cobra.Command{
	Use:   "shell [command]...",
	Short: "Drive the visor shell in a browser over CDP",
	Long: `Drive the visor shell in a browser over CDP.

Opens the hypervisor UI in a tab of a browser already running with
--remote-debugging-port, opens the console window, types each argument into the
terminal as a command line, and writes a screenshot plus the page console.

Needs a browser started with --remote-debugging-port=9222.`,
	Run: func(_ *cobra.Command, args []string) {
		if err := driveShell(shellPort, shellURL, shellOut, shellLoad, shellBoot,
			shellSettle, shellNoConsole, shellKeepTabs, args); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	},
}

// cdp is a minimal CDP client: request/response by id, plus the console and
// exception events worth reporting.
type cdp struct {
	c   *websocket.Conn
	ctx context.Context

	mu      sync.Mutex
	nextID  int
	waiters map[int]chan json.RawMessage
	logs    []string
}

type cdpMsg struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (d *cdp) pump() {
	for {
		_, data, err := d.c.Read(d.ctx)
		if err != nil {
			return
		}
		var m cdpMsg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		if m.ID != 0 {
			d.mu.Lock()
			ch := d.waiters[m.ID]
			delete(d.waiters, m.ID)
			d.mu.Unlock()
			if ch != nil {
				if m.Error != nil {
					ch <- json.RawMessage(`{"error":` + strconv(m.Error.Message) + `}`)
				} else {
					ch <- m.Result
				}
			}
			continue
		}
		d.note(m)
	}
}

// note records the events worth reporting: console errors and warnings, and
// uncaught exceptions.
func (d *cdp) note(m cdpMsg) {
	switch m.Method {
	case "Runtime.consoleAPICalled":
		var p struct {
			Type string `json:"type"`
			Args []struct {
				Value       interface{} `json:"value"`
				Description string      `json:"description"`
			} `json:"args"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		if !shellLogAll && p.Type != "error" && p.Type != "warning" {
			return
		}
		parts := make([]string, 0, len(p.Args))
		for _, a := range p.Args {
			if a.Value != nil {
				parts = append(parts, fmt.Sprint(a.Value))
			} else {
				parts = append(parts, a.Description)
			}
		}
		d.record("[" + p.Type + "] " + strings.Join(parts, " "))
	case "Runtime.exceptionThrown":
		var p struct {
			ExceptionDetails struct {
				Text      string `json:"text"`
				Exception struct {
					Description string `json:"description"`
				} `json:"exception"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(m.Params, &p) != nil {
			return
		}
		d.record("[exception] " + p.ExceptionDetails.Text + " " + p.ExceptionDetails.Exception.Description)
	}
}

func (d *cdp) record(s string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(s) > 400 {
		s = s[:400]
	}
	d.logs = append(d.logs, s)
}

func strconv(s string) string {
	b, _ := json.Marshal(s) //nolint:errcheck
	return string(b)
}

func (d *cdp) send(method string, params map[string]interface{}) (json.RawMessage, error) {
	d.mu.Lock()
	d.nextID++
	id := d.nextID
	ch := make(chan json.RawMessage, 1)
	d.waiters[id] = ch
	d.mu.Unlock()
	body, _ := json.Marshal(map[string]interface{}{"id": id, "method": method, "params": params}) //nolint:errcheck
	if err := d.c.Write(d.ctx, websocket.MessageText, body); err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r, nil
	case <-time.After(60 * time.Second):
		return nil, fmt.Errorf("%s timed out", method)
	}
}

// eval runs an expression in the page and returns its value as a string.
func (d *cdp) eval(expr string) string {
	res, err := d.send("Runtime.evaluate", map[string]interface{}{
		"expression": expr, "returnByValue": true, "awaitPromise": true,
	})
	if err != nil {
		return "ERR " + err.Error()
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
			Type  string          `json:"type"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text string `json:"text"`
		} `json:"exceptionDetails"`
	}
	_ = json.Unmarshal(res, &r) //nolint:errcheck
	if r.ExceptionDetails != nil {
		return "ERR " + r.ExceptionDetails.Text
	}
	if len(r.Result.Value) == 0 {
		return r.Result.Type
	}
	return strings.Trim(string(r.Result.Value), `"`)
}

// key sends one key press. Letters and digits carry a virtual key code, which
// is what the terminal's key handling keys off; punctuation and space need a
// char event as well or they never reach the line editor.
func (d *cdp) key(name string, code int, text string) {
	down := map[string]interface{}{"type": "keyDown", "key": name, "windowsVirtualKeyCode": code}
	if text != "" {
		down["text"] = text
	}
	_, _ = d.send("Input.dispatchKeyEvent", down)                   //nolint:errcheck
	_, _ = d.send("Input.dispatchKeyEvent", map[string]interface{}{ //nolint:errcheck
		"type": "keyUp", "key": name, "windowsVirtualKeyCode": code,
	})
}

func (d *cdp) typeLine(s string) {
	for _, r := range s {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			up := r
			if r >= 'a' && r <= 'z' {
				up = r - 32
			}
			d.key(string(r), int(up), string(r))
		default:
			code := 0
			if r == ' ' {
				code = 32
			}
			_, _ = d.send("Input.dispatchKeyEvent", map[string]interface{}{ //nolint:errcheck
				"type": "keyDown", "key": string(r), "windowsVirtualKeyCode": code,
			})
			_, _ = d.send("Input.dispatchKeyEvent", map[string]interface{}{ //nolint:errcheck
				"type": "char", "key": string(r), "text": string(r),
			})
			_, _ = d.send("Input.dispatchKeyEvent", map[string]interface{}{ //nolint:errcheck
				"type": "keyUp", "key": string(r), "windowsVirtualKeyCode": code,
			})
		}
	}
	d.key("Enter", 13, "\r")
}

// clickText clicks the last leaf element whose trimmed text matches, which is
// how the tour's "skip", the hamburger and the console item are reached
// without depending on the Angular UI's class names.
func (d *cdp) clickText(text string) string {
	return d.eval(fmt.Sprintf(`(() => {
      const want = %s.toLowerCase();
      const e = [...document.querySelectorAll('*')]
        .filter(x => (x.textContent||'').trim().toLowerCase() === want && x.children.length === 0);
      if (!e.length) { return 'not found'; }
      e[e.length-1].click();
      return 'clicked';
    })()`, strconv(text)))
}

func driveShell(port, pageURL, out string, load, boot, settle int, noConsole, keepTabs bool, cmds []string) error {
	if !keepTabs {
		if n, err := closeOrigin(port, pageURL); err != nil {
			fmt.Fprintln(os.Stderr, "could not close existing tabs:", err)
		} else if n > 0 {
			fmt.Printf("closed %d existing tab(s) on this origin\n", n)
		}
	}
	tab, err := newTab(port)
	if err != nil {
		return err
	}
	defer closeTab(port, tab.ID) //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	c, _, err := websocket.Dial(ctx, tab.WS, nil)
	if err != nil {
		return fmt.Errorf("cdp dial: %w", err)
	}
	c.SetReadLimit(96 << 20)
	d := &cdp{c: c, ctx: ctx, waiters: map[int]chan json.RawMessage{}}
	go d.pump()

	for _, m := range []string{"Runtime.enable", "Page.enable", "Network.enable"} {
		_, _ = d.send(m, nil) //nolint:errcheck
	}
	_, _ = d.send("Network.setCacheDisabled", map[string]interface{}{"cacheDisabled": true}) //nolint:errcheck
	_, _ = d.send("Emulation.setDeviceMetricsOverride", map[string]interface{}{              //nolint:errcheck
		"width": 1700, "height": 1000, "deviceScaleFactor": 1, "mobile": false,
	})
	if _, err := d.send("Page.navigate", map[string]interface{}{"url": pageURL}); err != nil {
		return err
	}
	// Ready when the UI has painted something interactive, not when a timer
	// says so. -load is now the ceiling, not the cost.
	if !d.waitFor(`(!!document.querySelector('#view-toggle') ||
		[...document.querySelectorAll('*')].some(x => (x.textContent||'').trim() === '☰'))`,
		time.Duration(load)*time.Second) {
		fmt.Fprintln(os.Stderr, "warning: the page never looked ready; carrying on")
	}

	if !noConsole {
		fmt.Println("tour:   ", d.clickText("skip"))
		time.Sleep(time.Second)
		fmt.Println("menu:   ", d.clickText("☰"))
		time.Sleep(1500 * time.Millisecond)
		fmt.Println("console:", d.clickText("console"))
		for i := 0; i < boot; i++ {
			time.Sleep(time.Second)
			if t := d.eval("typeof globalThis.skywireShell"); t == "object" || t == "function" {
				break
			}
		}
		fmt.Println("shell:  ", d.eval("typeof globalThis.skywireShell"))
		time.Sleep(3 * time.Second)
		d.eval("(document.querySelector('.winbox .xterm-helper-textarea')||{focus(){}}).focus()")
	} else {
		d.eval("(document.querySelector('.xterm-helper-textarea')||{focus(){}}).focus()")
	}
	time.Sleep(time.Second)

	for _, cmd := range cmds {
		before := d.eval("window.__hvshellDone|0")
		fmt.Print("typing:  ", cmd)
		start := time.Now()
		if noConsole {
			d.typeLine(cmd)
			time.Sleep(time.Duration(settle) * time.Second)
			fmt.Println()
			continue
		}
		d.typeLine(cmd + doneMarker)
		if d.waitFor(fmt.Sprintf("(window.__hvshellDone|0) > %s", before),
			time.Duration(settle)*time.Second) {
			fmt.Printf("   [%s]\n", time.Since(start).Round(time.Millisecond))
		} else {
			fmt.Printf("   [no completion within %ds — still running or wedged]\n", settle)
		}
	}

	// The terminal is a canvas, so the screen is read back as the xterm
	// buffer's text rather than from the pixels.
	screen := d.eval(`(() => {
      const t = (window.__tpvizTerm || null);
      const sel = document.querySelector('.winbox .xterm') || document.querySelector('.xterm');
      if (!sel) { return '(no terminal)'; }
      const rows = sel.querySelectorAll('.xterm-rows > div');
      if (rows.length) { return [...rows].map(r => r.textContent.replace(/\s+$/,'')).join('\n'); }
      return '(canvas renderer — screen text unavailable, see the screenshot)';
    })()`)
	fmt.Println("---- screen ----")
	fmt.Println(screen)

	if shot, err := d.send("Page.captureScreenshot", map[string]interface{}{"format": "png"}); err == nil {
		var s struct {
			Data string `json:"data"`
		}
		_ = json.Unmarshal(shot, &s) //nolint:errcheck
		if b, de := base64.StdEncoding.DecodeString(s.Data); de == nil {
			_ = os.WriteFile(out+".png", b, 0o600) //nolint:errcheck,gosec
			fmt.Println("screenshot:", out+".png")
		}
	}

	d.mu.Lock()
	logs := append([]string(nil), d.logs...)
	d.mu.Unlock()
	_ = os.WriteFile(out+".console.txt", []byte(strings.Join(logs, "\n")+"\n"), 0o600) //nolint:errcheck,gosec
	seen := map[string]bool{}
	fmt.Println("---- console errors ----")
	n := 0
	for _, l := range logs {
		k := l
		if len(k) > 90 {
			k = k[:90]
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		fmt.Println(" ", l)
		if n++; n >= 8 {
			break
		}
	}
	if n == 0 {
		fmt.Println("  none")
	}
	return nil
}

// closeOrigin closes existing pages on the same origin as pageURL and reports
// how many. The visor is one SharedWorker per origin, but every tab opens its
// own shell instance against it — so leftover tabs are extra wasm instances
// competing for the same visor, which is a good way to mistake test wreckage
// for a bug in the code under test.
func closeOrigin(port, pageURL string) (int, error) {
	u, err := url.Parse(pageURL)
	if err != nil {
		return 0, err
	}
	origin := u.Host
	if origin == "" {
		return 0, fmt.Errorf("no host in %q", pageURL)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://localhost:"+port+"/json/list", nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	var tabs []struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		URL  string `json:"url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tabs); err != nil {
		return 0, err
	}
	closed := 0
	for _, t := range tabs {
		if t.Type != "page" || !strings.Contains(t.URL, origin) {
			continue
		}
		if err := closeTab(port, t.ID); err == nil {
			closed++
		}
	}
	if closed > 0 {
		time.Sleep(2 * time.Second) // let the shared worker settle
	}
	return closed, nil
}

type tabInfo struct {
	ID string `json:"id"`
	WS string `json:"webSocketDebuggerUrl"`
}

func newTab(port string) (tabInfo, error) {
	var t tabInfo
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut,
		"http://localhost:"+port+"/json/new", nil)
	if err != nil {
		return t, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return t, fmt.Errorf("open tab: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return t, err
	}
	if t.WS == "" {
		return t, fmt.Errorf("no debugger url for the new tab")
	}
	return t, nil
}

func closeTab(port, id string) error {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
		"http://localhost:"+port+"/json/close/"+id, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}
