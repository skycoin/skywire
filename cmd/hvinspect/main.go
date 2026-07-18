// Package main cmd/hvinspect/main.go c4-vis-cli
// hvinspect — headless inspector for the Skywire hypervisor UI (wasm-visor
// harness and the native HV UI). Drives the installed Brave via chromedp (pure
// Go, no cgo) to capture, for any hash route:
//   - console output (log/info/warn/error + uncaught exceptions)
//   - the rendered DOM (document.documentElement.outerHTML)
//   - a full-page screenshot
//
// Usage: hvinspect <url> [waitSeconds] [outPrefix]
//
//	hvinspect 'https://localhost:8443/#/nodes/list/1' 7 /tmp/nodelist
//
// Writes <outPrefix>.html, <outPrefix>.console.txt, <outPrefix>.png and echoes
// the console to stdout. Self-signed certs are accepted (the harness uses one).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	cdplog "github.com/chromedp/cdproto/log"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

// resolveCDP attaches to an already-running browser via the Chrome DevTools
// Protocol. base is the devtools HTTP endpoint (e.g. http://127.0.0.1:9222) of a
// browser launched with --remote-debugging-port. It returns the browser-level
// websocket URL (for NewRemoteAllocator) plus the target id of the open PAGE
// whose URL matches want's host:port — so hvinspect drives the user's VISIBLE
// tab rather than spawning a headless one. Falls back to the first page target.
func resolveCDP(base, want string) (wsURL string, tid target.ID, err error) {
	var ver struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err = getJSON(base+"/json/version", &ver); err != nil {
		return "", "", fmt.Errorf("GET /json/version: %w", err)
	}
	if ver.WebSocketDebuggerURL == "" {
		return "", "", fmt.Errorf("no browser websocket at %s (is --remote-debugging-port set?)", base)
	}
	var pages []struct {
		Type string `json:"type"`
		URL  string `json:"url"`
		ID   string `json:"id"`
	}
	if err = getJSON(base+"/json", &pages); err != nil {
		return "", "", fmt.Errorf("GET /json: %w", err)
	}
	host := want
	if u, e := url.Parse(want); e == nil && u.Host != "" {
		host = u.Host
	}
	for _, p := range pages {
		if p.Type == "page" && strings.Contains(p.URL, host) {
			return ver.WebSocketDebuggerURL, target.ID(p.ID), nil
		}
	}
	// No tab matches the wanted host. Return an empty target id (NOT a random
	// other tab — grabbing an unrelated tab would hijack/close the user's other
	// work); the caller opens a fresh tab instead.
	return ver.WebSocketDebuggerURL, target.ID(""), nil
}

func getJSON(u string, v interface{}) error {
	resp, err := http.Get(u) //nolint:gosec,noctx
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	return json.NewDecoder(resp.Body).Decode(v)
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: hvinspect <url> [waitSeconds] [outPrefix]")
		os.Exit(2)
	}
	url := os.Args[1]
	wait := 7 * time.Second
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			wait = time.Duration(n) * time.Second
		}
	}
	outPrefix := "/tmp/hvinspect"
	if len(os.Args) > 3 {
		outPrefix = os.Args[3]
	}

	// Attach mode: HVINSPECT_CDP=http://host:port points at a browser the user
	// launched with --remote-debugging-port. hvinspect then drives the user's
	// VISIBLE tab (no headless instance, no navigation/reload — it operates on
	// the page that's already loaded; an eval can hash-route within the SPA).
	// Unset → the default headless Brave is launched.
	cdpBase := os.Getenv("HVINSPECT_CDP")
	attach := cdpBase != ""

	var allocCtx context.Context
	var cancelA context.CancelFunc
	var attachID target.ID
	if attach {
		ws, tid, err := resolveCDP(cdpBase, url)
		if err != nil {
			fmt.Fprintln(os.Stderr, "CDP attach:", err)
			os.Exit(1)
		}
		attachID = tid
		allocCtx, cancelA = chromedp.NewRemoteAllocator(context.Background(), ws)
	} else {
		opts := append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath("/usr/bin/brave"),
			chromedp.Flag("headless", true),
			chromedp.Flag("ignore-certificate-errors", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.WindowSize(1280, 1400),
		)
		allocCtx, cancelA = chromedp.NewExecAllocator(context.Background(), opts...)
	}
	defer cancelA()

	// attachExisting: drive a tab the user already has open (by id). Otherwise we
	// create a tab — a headless one (no CDP) or, in attach mode when no tab
	// matched, a fresh visible tab in the user's browser to load the visor into.
	attachExisting := attach && attachID != ""
	var ctx context.Context
	var cancelC context.CancelFunc
	if attachExisting {
		ctx, cancelC = chromedp.NewContext(allocCtx, chromedp.WithTargetID(attachID))
	} else {
		ctx, cancelC = chromedp.NewContext(allocCtx)
	}
	// chromedp's context cancel sends Target.closeTarget. In attach mode that
	// would close the user's tab (existing OR the fresh visor tab we just opened
	// for them), so only close targets in headless mode. The process exits right
	// after either way; leaking the attach context just leaves the tab open.
	if !attach {
		defer cancelC()
	} else {
		_ = cancelC
	}
	ctx, cancelT := context.WithTimeout(ctx, wait+45*time.Second)
	defer cancelT()

	var console []string
	var netlog []string // network failures + non-2xx /api responses
	reqURL := map[network.RequestID]string{}
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *runtime.EventConsoleAPICalled:
			var b strings.Builder
			fmt.Fprintf(&b, "[%s] ", e.Type)
			for _, a := range e.Args {
				if len(a.Value) > 0 {
					b.WriteString(strings.Trim(string(a.Value), `"`))
				} else if a.Description != "" {
					b.WriteString(a.Description)
				}
				b.WriteByte(' ')
			}
			console = append(console, strings.TrimSpace(b.String()))
		case *runtime.EventExceptionThrown:
			console = append(console, "[exception] "+e.ExceptionDetails.Error())
		case *network.EventRequestWillBeSent:
			reqURL[e.RequestID] = e.Request.URL
		case *network.EventResponseReceived:
			// Record /api/* responses and any non-2xx — the signal for a
			// route that the backend doesn't serve / returns an error.
			u := e.Response.URL
			st := int(e.Response.Status)
			if strings.Contains(u, "/api/") || st < 200 || st >= 300 {
				netlog = append(netlog, fmt.Sprintf("HTTP %d  %s", st, u))
			}
		case *network.EventLoadingFailed:
			u := reqURL[e.RequestID]
			netlog = append(netlog, fmt.Sprintf("FAILED %s  %s", e.ErrorText, u))
		case *cdplog.EventEntryAdded:
			// Browser-emitted log entries (Log domain) — this is where
			// WebTransport / fetch failures like
			//   "Failed to establish a connection to https://ip:port/skywire:
			//    net::ERR_CONNECTION_REFUSED"
			// surface. They are NOT console.* calls, so without the Log domain
			// they were invisible to capture even though the user sees them in
			// the real DevTools console.
			en := e.Entry
			line := fmt.Sprintf("[%s/%s] %s", en.Source, en.Level, en.Text)
			if en.URL != "" {
				line += "  " + en.URL
			}
			netlog = append(netlog, line)
			if en.Level == cdplog.LevelError {
				console = append(console, "[browser-error] "+en.Text)
			}
		}
	})

	var html string
	var shot []byte
	evalExpr := os.Getenv("HVINSPECT_EVAL") // optional in-page JS probe (await-able)
	var evalResult string
	actions := []chromedp.Action{
		network.Enable(),
		runtime.Enable(),
		cdplog.Enable(),
	}
	if !attachExisting {
		// Headless, or attach-with-no-matching-tab: load the URL (headless tab, or
		// the fresh visor tab we opened in the user's browser).
		actions = append(actions, chromedp.Navigate(url))
	} else if os.Getenv("HVINSPECT_RELOAD") != "" {
		// Reload the ATTACHED tab (by id, so it stays on the same tab across the
		// reload — unlike re-matching by URL, which drifts while the page is
		// momentarily about:blank). Used to pick up new browse.js after a rebuild.
		actions = append(actions, chromedp.Reload())
	}
	actions = append(actions, chromedp.Sleep(wait))
	if evalExpr != "" {
		actions = append(actions, chromedp.Evaluate(evalExpr, &evalResult, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true).WithReturnByValue(true)
		}))
	}
	actions = append(actions,
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.FullScreenshot(&shot, 80),
	)
	if err := chromedp.Run(ctx, actions...); err != nil {
		fmt.Fprintln(os.Stderr, "inspect error:", err)
		// still write whatever we captured
	}

	_ = os.WriteFile(outPrefix+".html", []byte(html), 0o644)
	_ = os.WriteFile(outPrefix+".console.txt", []byte(strings.Join(console, "\n")+"\n"), 0o644)
	_ = os.WriteFile(outPrefix+".net.txt", []byte(strings.Join(netlog, "\n")+"\n"), 0o644)
	if len(shot) > 0 {
		_ = os.WriteFile(outPrefix+".png", shot, 0o644)
	}
	if evalExpr != "" {
		fmt.Printf("---- eval ----\n%s\n", evalResult)
	}
	fmt.Printf("---- network (%d) ----\n%s\n", len(netlog), strings.Join(netlog, "\n"))

	fmt.Printf("=== %s ===\n", url)
	fmt.Printf("DOM: %s.html (%d bytes)  screenshot: %s.png  console: %d lines\n",
		outPrefix, len(html), outPrefix, len(console))
	fmt.Println("---- console ----")
	for _, l := range console {
		fmt.Println(l)
	}
}
