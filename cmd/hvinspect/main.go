// hvinspect — headless inspector for the Skywire hypervisor UI (wasm-visor
// harness and the native HV UI). Drives the installed Brave via chromedp (pure
// Go, no cgo) to capture, for any hash route:
//   - console output (log/info/warn/error + uncaught exceptions)
//   - the rendered DOM (document.documentElement.outerHTML)
//   - a full-page screenshot
//
// Usage: hvinspect <url> [waitSeconds] [outPrefix]
//   hvinspect 'https://localhost:8443/#/nodes/list/1' 7 /tmp/nodelist
//
// Writes <outPrefix>.html, <outPrefix>.console.txt, <outPrefix>.png and echoes
// the console to stdout. Self-signed certs are accepted (the harness uses one).
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

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

	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath("/usr/bin/brave"),
		chromedp.Flag("headless", true),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.WindowSize(1280, 1400),
	)
	allocCtx, cancelA := chromedp.NewExecAllocator(context.Background(), opts...)
	defer cancelA()
	ctx, cancelC := chromedp.NewContext(allocCtx)
	defer cancelC()
	ctx, cancelT := context.WithTimeout(ctx, wait+45*time.Second)
	defer cancelT()

	var console []string
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
		}
	})

	var html string
	var shot []byte
	err := chromedp.Run(ctx,
		runtime.Enable(),
		chromedp.Navigate(url),
		chromedp.Sleep(wait),
		chromedp.OuterHTML("html", &html, chromedp.ByQuery),
		chromedp.FullScreenshot(&shot, 80),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "inspect error:", err)
		// still write whatever we captured
	}

	_ = os.WriteFile(outPrefix+".html", []byte(html), 0o644)
	_ = os.WriteFile(outPrefix+".console.txt", []byte(strings.Join(console, "\n")+"\n"), 0o644)
	if len(shot) > 0 {
		_ = os.WriteFile(outPrefix+".png", shot, 0o644)
	}

	fmt.Printf("=== %s ===\n", url)
	fmt.Printf("DOM: %s.html (%d bytes)  screenshot: %s.png  console: %d lines\n",
		outPrefix, len(html), outPrefix, len(console))
	fmt.Println("---- console ----")
	for _, l := range console {
		fmt.Println(l)
	}
}
