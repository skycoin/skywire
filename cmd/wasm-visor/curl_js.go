//go:build js && wasm

// Package main cmd/wasm-visor/curl_js.go c3-vis-wasm
//
// curl with -x support: the shell's curl, upgraded from websh's fetch()-based
// applet to Go's net/http so it can honor a proxy flag —
//
//	curl -x socks5h://127.0.0.1:1080 https://example.com/
//
// speaks SOCKS5 over the page's virtual loopback to whatever listens there
// (canonically the in-process skysocks-client app started with
// `skywire cli proxy start <exit>` in another terminal — the Linux ritual),
// with TLS terminated in-tab. Without -x it uses Go's default js transport
// (the browser's fetch, CORS applies), matching the applet it replaces.
// Registered AFTER browser.Register() so this definition wins the name.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"
)

const curlHelp = "fetch a URL: curl [-s] [-i] [-o file] [-X method] [-H 'K: V']... [-d body] [-x socks5h://host:port] <url>"

func registerCurl() {
	shell.RegisterApplet("curl", curlHelp, runCurlX)
}

func runCurlX(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
	var (
		silent, include bool
		outFile, method string
		proxyAddr, data string
		headers         []string
		rawURL          string
	)
	i := 0
	next := func(flag string) (string, bool) {
		i++
		if i >= len(args) {
			fmt.Fprintf(hc.Stderr, "curl: %s needs a value\n", flag)
			return "", false
		}
		return args[i], true
	}
	for ; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-s" || a == "--silent":
			silent = true
		case a == "-i" || a == "--include":
			include = true
		case a == "-o" || a == "--output":
			v, ok := next(a)
			if !ok {
				return 2
			}
			outFile = v
		case a == "-X" || a == "--request":
			v, ok := next(a)
			if !ok {
				return 2
			}
			method = v
		case a == "-H" || a == "--header":
			v, ok := next(a)
			if !ok {
				return 2
			}
			headers = append(headers, v)
		case a == "-d" || a == "--data":
			v, ok := next(a)
			if !ok {
				return 2
			}
			data = v
		case a == "-x" || a == "--proxy":
			v, ok := next(a)
			if !ok {
				return 2
			}
			proxyAddr = v
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(hc.Stderr, "curl: unknown flag %s\nusage: %s\n", a, curlHelp)
			return 2
		default:
			rawURL = a
		}
	}
	if rawURL == "" {
		fmt.Fprintf(hc.Stderr, "usage: %s\n", curlHelp)
		return 2
	}
	if !strings.Contains(rawURL, "://") {
		rawURL = "http://" + rawURL
	}
	if method == "" {
		if data != "" {
			method = "POST"
		} else {
			method = "GET"
		}
	}

	client := &http.Client{Timeout: 120 * time.Second}
	if proxyAddr != "" {
		sa := socksAddrOf(proxyAddr)
		if sa == "" {
			fmt.Fprintf(hc.Stderr, "curl: unsupported proxy %q (use socks5h://host:port)\n", proxyAddr)
			return 2
		}
		var err error
		client, err = socksProxyHTTPClient(sa)
		if err != nil {
			fmt.Fprintf(hc.Stderr, "curl: proxy %s: %v\n", sa, err)
			return 1
		}
	}

	var body io.Reader
	if data != "" {
		body = strings.NewReader(data)
	}
	req, err := http.NewRequest(method, rawURL, body)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "curl: %v\n", err)
		return 1
	}
	if data != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, h := range headers {
		if k, v, ok := strings.Cut(h, ":"); ok {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		hint := ""
		if proxyAddr == "" {
			hint = " (no -x: browser fetch, cross-origin needs CORS)"
		}
		fmt.Fprintf(hc.Stderr, "curl: %v%s\n", err, hint)
		return 1
	}
	defer resp.Body.Close() //nolint:errcheck
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		fmt.Fprintf(hc.Stderr, "curl: read body: %v\n", err)
		return 1
	}
	if !silent {
		fmt.Fprintf(hc.Stderr, "curl: %s → %s (%d bytes)\n", rawURL, resp.Status, len(b))
	}
	var head string
	if include {
		lines := []string{resp.Proto + " " + resp.Status}
		keys := make([]string, 0, len(resp.Header))
		for k := range resp.Header {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			lines = append(lines, k+": "+resp.Header.Get(k))
		}
		head = strings.Join(lines, "\r\n") + "\r\n\r\n"
	}
	if outFile != "" {
		if err := os.WriteFile(outFile, b, 0o644); err != nil { //nolint:gosec
			fmt.Fprintf(hc.Stderr, "curl: write %s: %v\n", outFile, err)
			return 1
		}
		if !silent {
			fmt.Fprintf(hc.Stderr, "curl: saved %s\n", outFile)
		}
		return 0
	}
	if head != "" {
		fmt.Fprint(hc.Stdout, head)
	}
	_, _ = hc.Stdout.Write(b) //nolint:errcheck
	if len(b) > 0 && b[len(b)-1] != '\n' {
		fmt.Fprintln(hc.Stdout)
	}
	return 0
}
