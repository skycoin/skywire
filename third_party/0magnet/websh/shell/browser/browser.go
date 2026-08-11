//go:build js && wasm

// Package browser adds the applets that only make sense in a browser: the
// JavaScript console (`js`, `logs`), transfers to and from the host machine
// (`download`, `upload`), the network (`curl`, `nc`) and the clipboard
// (`pbcopy`, `pbpaste`).
//
// It is a separate package from the shell so that embedders — a wasm page, a
// browser extension, the skywire visor tab — get them by calling Register(),
// while the shell itself stays free of syscall/js and testable natively.
package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"

	"github.com/skycoin/skywire/third_party/0magnet/sh/v3/interp"
	"github.com/skycoin/skywire/third_party/0magnet/websh/shell"
)

// Register adds every browser applet to the shell's applet set. Call it once,
// before PopulateBin so the commands show up in /bin.
func Register() {
	installConsoleCapture()
	shell.RegisterApplet("js", "evaluate JavaScript in the page: js 'document.title'", runJS)
	shell.RegisterApplet("logs", "browser console output (-f follow, -n N, -e errors only, -c clear)", runLogs)
	shell.RegisterApplet("download", "save a file from the shell to your Downloads", runDownload)
	shell.RegisterApplet("upload", "pick a local file and copy it into the shell", runUpload)
	shell.RegisterApplet("curl", "fetch a URL (-o file; CORS applies)", runCurl)
	shell.RegisterApplet("nc", "WebSocket netcat: pipe lines to/from a ws:// endpoint", runNc)
	shell.RegisterApplet("pbcopy", "copy stdin to the system clipboard", runPbcopy)
	shell.RegisterApplet("pbpaste", "paste the system clipboard to stdout", runPbpaste)
}

// await resolves a value that may be a promise, parking the calling goroutine
// (not the JS event loop) until it settles.
func await(v js.Value) (js.Value, error) {
	if v.Type() != js.TypeObject || v.Get("then").Type() != js.TypeFunction {
		return v, nil
	}
	done := make(chan struct{})
	var res js.Value
	var err error
	onOK := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			res = args[0]
		}
		close(done)
		return nil
	})
	onErr := js.FuncOf(func(_ js.Value, args []js.Value) any {
		err = errors.New(describe(args))
		close(done)
		return nil
	})
	defer onOK.Release()
	defer onErr.Release()
	v.Call("then", onOK).Call("catch", onErr)
	<-done
	return res, err
}

func describe(args []js.Value) string {
	if len(args) == 0 {
		return "rejected"
	}
	v := args[0]
	if v.Type() == js.TypeObject && v.Get("message").Truthy() {
		return v.Get("message").String()
	}
	return js.Global().Get("String").Invoke(v).String()
}

// format renders a JS value the way a console would: strings bare, objects as
// indented JSON, everything else via String().
func format(v js.Value) string {
	switch v.Type() {
	case js.TypeUndefined:
		return "undefined"
	case js.TypeNull:
		return "null"
	case js.TypeString:
		return v.String()
	case js.TypeObject, js.TypeFunction:
		json := js.Global().Get("JSON")
		out := func() (s string) {
			defer func() {
				if recover() != nil {
					// cyclic, a DOM node, a function: fall back to String()
					s = js.Global().Get("String").Invoke(v).String()
				}
			}()
			r := json.Call("stringify", v, js.Null(), 2)
			if r.Type() != js.TypeString {
				return js.Global().Get("String").Invoke(v).String()
			}
			return r.String()
		}()
		return out
	default:
		return js.Global().Get("String").Invoke(v).String()
	}
}

// runJS evaluates JavaScript in the page and prints the result — the browser
// console, from the shell. Promises are awaited, so `js 'fetch("/x")'` reports
// the response rather than a pending promise.
func runJS(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	expr := strings.TrimSpace(strings.Join(args, " "))
	if expr == "" {
		fmt.Fprintln(hc.Stderr, "usage: js <expression>    eg: js 'document.title'")
		return 2
	}
	var res js.Value
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				// a JS exception (including CSP blocking eval) arrives here
				err = fmt.Errorf("%v", r)
			}
		}()
		res = js.Global().Call("eval", expr)
		return nil
	}()
	if err != nil {
		fmt.Fprintf(hc.Stderr, "js: %v\n", err)
		return 1
	}
	settled, err := await(res)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "js: %v\n", err)
		return 1
	}
	fmt.Fprintln(hc.Stdout, format(settled))
	return 0
}

func runDownload(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(hc.Stderr, "usage: download <file>")
		return 1
	}
	data, err := shell.ReadFile(s, hc, args[0])
	if err != nil {
		fmt.Fprintf(hc.Stderr, "download: %v\n", err)
		return 1
	}
	u8 := js.Global().Get("Uint8Array").New(len(data))
	js.CopyBytesToJS(u8, data)
	parts := js.Global().Get("Array").New()
	parts.Call("push", u8)
	blob := js.Global().Get("Blob").New(parts)
	url := js.Global().Get("URL").Call("createObjectURL", blob)
	a := js.Global().Get("document").Call("createElement", "a")
	a.Set("href", url)
	a.Set("download", shell.Base(args[0]))
	a.Call("click")
	js.Global().Get("URL").Call("revokeObjectURL", url)
	fmt.Fprintf(hc.Stdout, "downloading %s (%d bytes)\n", shell.Base(args[0]), len(data))
	return 0
}

func runUpload(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	doc := js.Global().Get("document")
	input := doc.Call("createElement", "input")
	input.Set("type", "file")

	picked := make(chan js.Value, 1)
	onChange := js.FuncOf(func(js.Value, []js.Value) any {
		files := input.Get("files")
		if files.Get("length").Int() > 0 {
			picked <- files.Index(0)
		} else {
			picked <- js.Null()
		}
		return nil
	})
	defer onChange.Release()
	input.Set("onchange", onChange)
	input.Call("click")
	fmt.Fprintln(hc.Stdout, "waiting for file selection... (Ctrl+C to cancel)")

	var file js.Value
	select {
	case file = <-picked:
	case <-ctx.Done():
		fmt.Fprintln(hc.Stderr, "upload: cancelled")
		return 130
	}
	if file.IsNull() {
		fmt.Fprintln(hc.Stderr, "upload: no file selected")
		return 1
	}
	buf, err := await(file.Call("arrayBuffer"))
	if err != nil {
		fmt.Fprintf(hc.Stderr, "upload: %v\n", err)
		return 1
	}
	u8 := js.Global().Get("Uint8Array").New(buf)
	data := make([]byte, u8.Get("length").Int())
	js.CopyBytesToGo(data, u8)

	dest := file.Get("name").String()
	if len(args) > 0 {
		dest = args[0]
	}
	written, err := shell.WriteFile(s, hc, dest, data)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "upload: %v\n", err)
		return 1
	}
	fmt.Fprintf(hc.Stdout, "%s (%d bytes)\n", written, len(data))
	return 0
}

func runCurl(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	outFile := ""
	var urlArg string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-o" && i+1 < len(args):
			i++
			outFile = args[i]
		case args[i] == "-s" || args[i] == "-L":
			// accepted for muscle memory; fetch always follows redirects
		default:
			urlArg = args[i]
		}
	}
	if urlArg == "" {
		fmt.Fprintln(hc.Stderr, "usage: curl [-o file] <url>   (subject to CORS)")
		return 1
	}
	if !strings.Contains(urlArg, "://") {
		urlArg = "https://" + urlArg
	}
	resp, err := await(js.Global().Call("fetch", urlArg))
	if err != nil {
		fmt.Fprintf(hc.Stderr, "curl: %v (cross-origin requests need CORS headers)\n", err)
		return 1
	}
	if !resp.Get("ok").Bool() {
		fmt.Fprintf(hc.Stderr, "curl: HTTP %d\n", resp.Get("status").Int())
		return 22
	}
	buf, err := await(resp.Call("arrayBuffer"))
	if err != nil {
		fmt.Fprintf(hc.Stderr, "curl: %v\n", err)
		return 1
	}
	u8 := js.Global().Get("Uint8Array").New(buf)
	data := make([]byte, u8.Get("length").Int())
	js.CopyBytesToGo(data, u8)
	if outFile != "" {
		written, err := shell.WriteFile(s, hc, outFile, data)
		if err != nil {
			fmt.Fprintf(hc.Stderr, "curl: %v\n", err)
			return 1
		}
		fmt.Fprintf(hc.Stdout, "saved %d bytes to %s\n", len(data), written)
		return 0
	}
	hc.Stdout.Write(data)
	return 0
}

func runNc(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(hc.Stderr, "usage: nc <ws://host/path | wss://host/path>")
		return 1
	}
	url := args[0]
	if !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
		url = "wss://" + url
	}
	ws := js.Global().Get("WebSocket").New(url)
	ws.Set("binaryType", "arraybuffer")

	closed := make(chan int, 1)
	onOpen := js.FuncOf(func(js.Value, []js.Value) any {
		fmt.Fprintf(hc.Stderr, "nc: connected to %s\n", url)
		return nil
	})
	onMessage := js.FuncOf(func(_ js.Value, cbArgs []js.Value) any {
		data := cbArgs[0].Get("data")
		if data.Type() == js.TypeString {
			fmt.Fprintln(hc.Stdout, data.String())
		} else {
			u8 := js.Global().Get("Uint8Array").New(data)
			buf := make([]byte, u8.Get("length").Int())
			js.CopyBytesToGo(buf, u8)
			hc.Stdout.Write(buf)
		}
		return nil
	})
	onClose := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case closed <- 0:
		default:
		}
		return nil
	})
	onError := js.FuncOf(func(js.Value, []js.Value) any {
		fmt.Fprintln(hc.Stderr, "nc: connection error")
		select {
		case closed <- 1:
		default:
		}
		return nil
	})
	defer onOpen.Release()
	defer onMessage.Release()
	defer onClose.Release()
	defer onError.Release()
	ws.Set("onopen", onOpen)
	ws.Set("onmessage", onMessage)
	ws.Set("onclose", onClose)
	ws.Set("onerror", onError)

	go shell.CopyLines(hc.Stdin, func(line string) {
		if ws.Get("readyState").Int() == 1 {
			ws.Call("send", line)
		}
	})

	select {
	case code := <-closed:
		return code
	case <-ctx.Done():
		ws.Call("close")
		return 130
	}
}

func runPbcopy(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	data, err := shell.ReadAll(hc.Stdin)
	if err != nil {
		fmt.Fprintf(hc.Stderr, "pbcopy: %v\n", err)
		return 1
	}
	clip := js.Global().Get("navigator").Get("clipboard")
	if clip.IsUndefined() {
		fmt.Fprintln(hc.Stderr, "pbcopy: clipboard API unavailable")
		return 1
	}
	if _, err := await(clip.Call("writeText", string(data))); err != nil {
		fmt.Fprintf(hc.Stderr, "pbcopy: %v\n", err)
		return 1
	}
	return 0
}

func runPbpaste(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	clip := js.Global().Get("navigator").Get("clipboard")
	if clip.IsUndefined() {
		fmt.Fprintln(hc.Stderr, "pbpaste: clipboard API unavailable")
		return 1
	}
	text, err := await(clip.Call("readText"))
	if err != nil {
		fmt.Fprintf(hc.Stderr, "pbpaste: %v\n", err)
		return 1
	}
	fmt.Fprint(hc.Stdout, text.String())
	return 0
}
