//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/shell_js.go c3-vis-wasm
// A real shell for the browser desk. The wasm desk has no host to run a
// dmsgpty against, so the "visor cli" window was once a bespoke REPL in JS:
// one command per line, no pipes, no scripting, its own history and alias
// table.
//
// This is websh (github.com/0magnet/websh) instead: a Bash/POSIX interpreter
// over an in-memory filesystem, rendered by the xterm.js Go port
// (github.com/0magnet/xterm-go), with the visor's own API as applets whose
// JSON output pipes into the shell's jq:
//
//	about | jq -r '.public_key, .build.version'
//	tps | jq -r '.[].type' | sort | uniq -c
//	for pk in $(visors -c | jq -r '.[].local_pk'); do echo "$pk"; done
//
// # Where the visor is
//
// The terminal needs a DOM; the visor does not run in this instance. On the
// served desk it is the `skywire autoconfig` instance in the exec worker, and
// its hypervisor UI listens on the virtual loopback (vnet:8001) — so the
// visor applets call that API over vnet, exactly as the dashboard tab renders
// it. A page that still publishes the legacy globalThis.skywireVisor proxy
// (the in-page visor of cmd/wasm-visor) is honored first, so one code path
// serves both.
//
// Exposed to the UI as skywireShell.open(el) → { close, fit, focus, run }.
package deskhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall/js"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"
	"github.com/0magnet/websh/shell/browser"
	"github.com/0magnet/websh/web"
)

// wasmShellFS is the ONE in-tab filesystem shared by every websh terminal and
// the GUI file browser — files created in the shell show up in the browser and
// vice-versa. Seeded once (its /bin, README, etc.). MemMap-backed, so it's
// ephemeral: gone when the tab closes, like the rest of the wasm visor.
var (
	wasmShellFS     afero.Fs
	wasmShellFSOnce sync.Once
)

func sharedShellFS() afero.Fs {
	wasmShellFSOnce.Do(func() {
		// When the page installed jsfs (the in-memory Linux-layout
		// globalThis.fs — see github.com/0magnet/bottle jsfs.js), route the shell through the
		// OS layer so its builtins (cat, ls, jq, redirection) share ONE
		// filesystem with every `skywire` command execution: what
		// `skywire cli config gen -rp` writes under /opt/skywire, cat reads.
		// Without jsfs the historical private MemMapFs stands.
		if js.Global().Get("jsfs").Truthy() {
			wasmShellFS = afero.NewOsFs()
			_ = shell.Seed(wasmShellFS) //nolint:errcheck
			return
		}
		wasmShellFS = afero.NewMemMapFs()
		_ = shell.Seed(wasmShellFS) //nolint:errcheck
	})
	return wasmShellFS
}

// installShell publishes globalThis.skywireShell for browse.js: open(el) mounts
// a terminal; fs.* is the file-browser bridge onto the SAME shared filesystem.
func installShell() {
	js.Global().Set("skywireShell", js.ValueOf(map[string]interface{}{
		"open": js.FuncOf(jsOpenShell),
		"fs": js.ValueOf(map[string]interface{}{
			"readDir":   js.FuncOf(jsFsReadDir),
			"readFile":  js.FuncOf(jsFsReadFile),
			"writeFile": js.FuncOf(jsFsWriteFile),
			"mkdir":     js.FuncOf(jsFsMkdir),
			"remove":    js.FuncOf(jsFsRemove),
		}),
	}))
}

func fsErr(e error) any { return map[string]interface{}{"error": e.Error()} }

func fsArgPath(args []js.Value) string {
	if len(args) == 0 || args[0].Type() != js.TypeString {
		return ""
	}
	p := args[0].String()
	if p == "" {
		p = "/"
	}
	return filepath.Clean(p)
}

// The fs bridge returns PROMISES: these are js.FuncOf handlers, and with the
// page's jsfs every afero call parks on a deferred callback — blocking in a
// FuncOf blocks the whole JS event loop (the same constraint jsOpenShell
// documents). promise() runs the work on a goroutine; each resolves with the
// same value shape as before ({error} for failures, never a rejection), so
// consumers only add an await.

// jsFsReadDir(path) → Promise<[{name,dir,size,mtime}]> sorted dirs-first.
func jsFsReadDir(_ js.Value, args []js.Value) any {
	path := fsArgPath(args)
	return promise(func() (interface{}, error) {
		infos, err := afero.ReadDir(sharedShellFS(), path)
		if err != nil {
			return fsErr(err), nil
		}
		sort.Slice(infos, func(i, j int) bool {
			if infos[i].IsDir() != infos[j].IsDir() {
				return infos[i].IsDir()
			}
			return infos[i].Name() < infos[j].Name()
		})
		out := make([]interface{}, 0, len(infos))
		for _, fi := range infos {
			out = append(out, map[string]interface{}{
				"name": fi.Name(), "dir": fi.IsDir(),
				"size": float64(fi.Size()), "mtime": fi.ModTime().UnixMilli(),
			})
		}
		return out, nil
	})
}

// jsFsReadFile(path) → Promise<{text}> for a UTF-8 file, or {error}. Caps at
// 2 MiB so a huge file can't wedge the single-threaded runtime rendering it.
func jsFsReadFile(_ js.Value, args []js.Value) any {
	path := fsArgPath(args)
	return promise(func() (interface{}, error) {
		b, err := afero.ReadFile(sharedShellFS(), path)
		if err != nil {
			return fsErr(err), nil
		}
		if len(b) > 2<<20 {
			return map[string]interface{}{"error": "file too large to view (>2 MiB)"}, nil
		}
		return map[string]interface{}{"text": string(b), "size": float64(len(b))}, nil
	})
}

func jsFsWriteFile(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[1].Type() != js.TypeString {
		return promise(func() (interface{}, error) { return fsErr(errors.New("writeFile(path, text)")), nil })
	}
	path, text := fsArgPath(args), args[1].String()
	return promise(func() (interface{}, error) {
		if err := afero.WriteFile(sharedShellFS(), path, []byte(text), 0o644); err != nil {
			return fsErr(err), nil
		}
		return map[string]interface{}{"ok": true}, nil
	})
}

func jsFsMkdir(_ js.Value, args []js.Value) any {
	path := fsArgPath(args)
	return promise(func() (interface{}, error) {
		if err := sharedShellFS().MkdirAll(path, 0o755); err != nil {
			return fsErr(err), nil
		}
		return map[string]interface{}{"ok": true}, nil
	})
}

func jsFsRemove(_ js.Value, args []js.Value) any {
	path := fsArgPath(args)
	return promise(func() (interface{}, error) {
		if err := sharedShellFS().RemoveAll(path); err != nil {
			return fsErr(err), nil
		}
		return map[string]interface{}{"ok": true}, nil
	})
}

// jsAwait resolves a value that may be a promise. The visor API is a direct
// call in this instance but a postMessage round-trip through the proxy, so
// every call site has to tolerate both. Safe to call from a shell goroutine —
// it parks that goroutine, not the JS event loop.
func jsAwait(v js.Value) (js.Value, error) {
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
		msg := "call rejected"
		if len(args) > 0 {
			if m := args[0]; m.Type() == js.TypeObject && m.Get("message").Truthy() {
				msg = m.Get("message").String()
			} else {
				msg = m.String()
			}
		}
		err = errors.New(msg)
		close(done)
		return nil
	})
	defer onOK.Release()
	defer onErr.Release()
	v.Call("then", onOK).Call("catch", onErr)
	<-done
	return res, err
}

// hvVNetPort is the port of the tab's hypervisor UI on the virtual loopback:
// the port desk-boot.js opens the dashboard tab on (its hvPort default) and,
// on a desk a native hypervisor serves, the one its host bridge claims for
// the host visor.
const hvVNetPort = 8001

// visorAPI calls the hypervisor API of the visor this tab is looking at: over
// the globalThis.skywireVisor proxy where a page publishes one (the legacy
// in-page visor), else over the virtual loopback to the hypervisor UI the
// tab's visor listens on (vnet.httpFetch, bottle vnet.js) — the same port the
// dashboard tab renders.
func visorAPI(method, path string, body []byte) (status int, out []byte, err error) {
	if v := js.Global().Get("skywireVisor"); v.Truthy() && v.Get("hvApi").Type() == js.TypeFunction {
		var arg any
		if body != nil {
			arg = string(body)
		}
		res, err := jsAwait(v.Call("hvApi", method, path, arg))
		if err != nil {
			return 0, nil, err
		}
		return res.Get("status").Int(), jsBytes(res.Get("body")), nil
	}
	vn := js.Global().Get("vnet")
	if !vn.Truthy() || vn.Get("httpFetch").Type() != js.TypeFunction {
		return 0, nil, errors.New("no visor in this tab")
	}
	if l := vn.Get("listening"); l.Type() == js.TypeFunction && !vn.Call("listening", hvVNetPort).Truthy() {
		return 0, nil, fmt.Errorf("no visor in this tab yet — nothing listens on vnet:%d", hvVNetPort)
	}
	var arg any
	hdrs := js.Global().Get("Object").New()
	if body != nil {
		buf := js.Global().Get("Uint8Array").New(len(body))
		js.CopyBytesToJS(buf, body)
		arg = buf
		hdrs.Set("Content-Type", "application/json")
	}
	res, err := jsAwait(vn.Call("httpFetch", hvVNetPort, method, path, arg, hdrs))
	if err != nil {
		return 0, nil, err
	}
	return res.Get("status").Int(), jsBytes(res.Get("body")), nil
}

// jsBytes copies a response body — a Uint8Array, or a string — into Go.
func jsBytes(b js.Value) []byte {
	switch b.Type() {
	case js.TypeString:
		return []byte(b.String())
	case js.TypeObject:
		if n := b.Get("length"); n.Type() == js.TypeNumber {
			out := make([]byte, n.Int())
			js.CopyBytesToGo(out, b)
			return out
		}
	}
	return nil
}

// visorPK returns the visor's public key for the prompt, WITHOUT waiting for
// it. The first call that misses starts a background fetch and returns empty;
// the prompt says "visor" until the answer lands, and every prompt after that
// is served from memory.
//
// It must not wait, and this is not a matter of taste. Every way of asking
// the visor settles on the JS event loop — a postMessage proxy's promise, a
// vnet exchange's callbacks. This is called from prompt(), which is called
// from the line editor's redraw, which is called from the terminal's data
// handler — a js.Func — and from openShell, itself a js.Func. The standard-Go
// wasm runtime hands control back to the browser only when every goroutine is
// blocked, and a js.Func callback must return before that can happen. So
// waiting here waits for a reply that cannot arrive until we stop waiting:
// the renderer spins inside wasm at full CPU, the page stops answering
// anything, the terminal window will not drag, and a stack sample shows a
// dozen frames of wasm under the Chromium frames.
//
// A goroutine may block on the reply safely: it is not a callback, so the
// runtime can park and let the event loop deliver it.
var (
	visorPKCache    atomic.Value // string
	visorPKFetching atomic.Bool
)

func visorPK() string {
	if pk, _ := visorPKCache.Load().(string); pk != "" {
		return pk
	}
	// Not known yet. Fetch it off the callback stack, one attempt at a time —
	// a failed attempt is retried by the next prompt rather than cached as "".
	if visorPKFetching.CompareAndSwap(false, true) {
		go func() {
			defer visorPKFetching.Store(false)
			if pk := fetchVisorPK(); pk != "" {
				visorPKCache.Store(pk)
			}
		}()
	}
	return ""
}

// visorPKWait is visorPK for callers OFF the callback stack — the applets,
// which run on the shell's goroutine — where waiting for the answer is safe
// and a first `pk` should not have to be asked twice.
func visorPKWait() string {
	if pk := visorPK(); pk != "" {
		return pk
	}
	if pk := fetchVisorPK(); pk != "" {
		visorPKCache.Store(pk)
		return pk
	}
	return ""
}

// fetchVisorPK asks the visor for its key: skywireVisor.status() where a page
// publishes one, else GET /api/about on its hypervisor over the loopback.
func fetchVisorPK() string {
	if v := js.Global().Get("skywireVisor"); v.Truthy() && v.Get("status").Type() == js.TypeFunction {
		st, err := jsAwait(v.Call("status"))
		if err != nil || !st.Truthy() {
			return ""
		}
		if pk := st.Get("pk"); pk.Type() == js.TypeString {
			return pk.String()
		}
		return ""
	}
	status, out, err := visorAPI("GET", "/api/about", nil)
	if err != nil || status != 200 {
		return ""
	}
	var about struct {
		PubKey string `json:"public_key"`
	}
	if json.Unmarshal(out, &about) != nil {
		return ""
	}
	return about.PubKey
}

// registerVisorApplets adds the visor commands to the shell's applet set. The
// registry is process-wide, so this runs once however many windows are open.
var registerVisorApplets = sync.OnceFunc(func() {
	call := func(hc *interp.HandlerContext, method, path string, body []byte) ([]byte, int) {
		status, out, err := visorAPI(method, path, body)
		if err != nil {
			_, _ = fmt.Fprintf(hc.Stderr, "%v\n", err) //nolint:errcheck
			return nil, 1
		}
		if status < 200 || status >= 300 {
			_, _ = fmt.Fprintf(hc.Stderr, "HTTP %d %s\n", status, path) //nolint:errcheck
			if len(out) > 0 {
				_, _ = fmt.Fprintln(hc.Stderr, strings.TrimSpace(string(out))) //nolint:errcheck
			}
			return nil, 1
		}
		return out, 0
	}

	// emit writes a JSON body: indented for reading, compact with -c (jq and
	// friends accept either).
	emit := func(hc *interp.HandlerContext, body []byte, compact bool) int {
		raw := strings.TrimSpace(string(body))
		if compact || raw == "" {
			_, _ = fmt.Fprintln(hc.Stdout, raw) //nolint:errcheck
			return 0
		}
		var v any
		if err := json.Unmarshal(body, &v); err != nil {
			_, _ = fmt.Fprintln(hc.Stdout, raw) //nolint:errcheck
			return 0
		}
		out, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			_, _ = fmt.Fprintln(hc.Stdout, raw) //nolint:errcheck
			return 0
		}
		_, _ = fmt.Fprintln(hc.Stdout, string(out)) //nolint:errcheck
		return 0
	}

	compactFlag := func(args []string) (bool, []string) {
		compact := false
		var rest []string
		for _, a := range args {
			if a == "-c" {
				compact = true
				continue
			}
			rest = append(rest, a)
		}
		return compact, rest
	}

	// get registers a GET applet against a path computed at call time (the
	// visor's key is not known until it has booted).
	get := func(name, help string, path func(hc *interp.HandlerContext) (string, bool)) {
		shell.RegisterApplet(name, help, func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			compact, _ := compactFlag(args)
			p, ok := path(hc)
			if !ok {
				return 1
			}
			body, code := call(hc, "GET", p, nil)
			if code != 0 {
				return code
			}
			return emit(hc, body, compact)
		})
	}

	fixed := func(p string) func(*interp.HandlerContext) (string, bool) {
		return func(*interp.HandlerContext) (string, bool) { return p, true }
	}
	self := func(suffix string) func(*interp.HandlerContext) (string, bool) {
		return func(hc *interp.HandlerContext) (string, bool) {
			pk := visorPKWait()
			if pk == "" {
				_, _ = fmt.Fprintln(hc.Stderr, "no visor identity yet — boot the visor first") //nolint:errcheck
				return "", false
			}
			return "/api/visors/" + pk + suffix, true
		}
	}

	get("about", "visor build and identity (GET /api/about)", fixed("/api/about"))
	get("visors", "every visor this hypervisor sees (GET /api/visors)", fixed("/api/visors"))
	get("net", "network view (GET /api/network-view)", fixed("/api/network-view"))
	get("health", "this visor's service health", self("/health"))
	get("apps", "this visor's apps", self("/apps"))
	get("tps", "this visor's transports", self("/transports"))
	get("routes", "this visor's routes", self("/routes"))

	shell.RegisterApplet("pk", "print this visor's public key", func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, _ []string) int {
		pk := visorPKWait()
		if pk == "" {
			_, _ = fmt.Fprintln(hc.Stderr, "no visor identity yet — boot the visor first") //nolint:errcheck
			return 1
		}
		_, _ = fmt.Fprintln(hc.Stdout, pk) //nolint:errcheck
		return 0
	})

	shell.RegisterApplet("hvapi", "call the visor API directly: hvapi [-c] METHOD PATH [body]",
		func(_ context.Context, _ *shell.Shell, hc *interp.HandlerContext, args []string) int {
			compact, rest := compactFlag(args)
			if len(rest) < 2 {
				_, _ = fmt.Fprintln(hc.Stderr, "usage: hvapi [-c] METHOD PATH [body]") //nolint:errcheck
				_, _ = fmt.Fprintln(hc.Stderr, "   eg: hvapi GET /api/visors")         //nolint:errcheck
				return 2
			}
			var body []byte
			if len(rest) > 2 {
				body = []byte(strings.Join(rest[2:], " "))
			}
			out, code := call(hc, strings.ToUpper(rest[0]), rest[1], body)
			if code != 0 {
				return code
			}
			return emit(hc, out, compact)
		})

	// The real skywire CLI, executed per invocation from /skywire.wasm
	// against the page-shared jsfs (skywirecmd_js.go). Registered only when
	// the page serves the module.
	registerSkywireCmd()
})

// openShell mounts a websh session on el: the terminal, the shell, its
// line editor and the progressive host, over the desk's shared filesystem,
// with the visor's applets registered.
func openShell(el js.Value) (*web.Session, error) {
	registerVisorApplets()
	browser.Register()
	registerMeshApplets()
	registerCurl()
	return web.NewSession(el, web.Options{
		FS:   sharedShellFS(),
		Host: "visor",
		Env:  deskShellEnv(),
	})
}

// jsOpenShell implements skywireShell.open(el): mount a terminal running websh
// in the given element (or element id). Returns { close, fit, focus, run }.
//
// The session is built on a GOROUTINE and the handle returned immediately.
// This is load-bearing, not a nicety: open() is a js.FuncOf handler, and a
// FuncOf that blocks blocks the whole JS event loop (syscall/js contract).
// openShell touches the filesystem (Seed, PopulateBin), and with the page's
// jsfs those syscalls complete on a MICROTASK — which can never run while the
// event loop is held. Synchronously opening the shell from JS therefore
// wedges the tab (or, with same-tick callbacks, nests _resume until g0
// overflows). Handle calls that arrive before the session is up are queued
// (run) or dropped (fit/focus — the next resize refits anyway).
func jsOpenShell(_ js.Value, args []js.Value) interface{} {
	if len(args) == 0 || !args[0].Truthy() {
		return js.ValueOf(map[string]interface{}{"error": "open(el): missing mount element"})
	}
	el := args[0]
	if el.Type() == js.TypeString {
		el = js.Global().Get("document").Call("getElementById", el.String())
		if !el.Truthy() {
			return js.ValueOf(map[string]interface{}{"error": "open(id): no such element"})
		}
	}

	var (
		mu      sync.Mutex
		s       *web.Session
		closed  bool
		pending []string // run() lines queued before the session is up
	)
	go func() {
		sess, err := openShell(el)
		if err != nil {
			js.Global().Get("console").Call("error", "desk shell: "+err.Error())
			return
		}
		mu.Lock()
		if closed {
			mu.Unlock()
			sess.Close()
			return
		}
		s = sess
		queued := pending
		pending = nil
		mu.Unlock()
		for _, cmd := range queued {
			sess.Submit(cmd)
		}
	}()

	get := func() *web.Session { mu.Lock(); defer mu.Unlock(); return s }
	handle := js.Global().Get("Object").New()
	closeFn := js.FuncOf(func(js.Value, []js.Value) any {
		mu.Lock()
		closed = true
		sess := s
		mu.Unlock()
		if sess != nil {
			sess.Close()
		}
		return nil
	})
	fitFn := js.FuncOf(func(js.Value, []js.Value) any {
		if sess := get(); sess != nil {
			sess.Term.Fit()
		}
		return nil
	})
	focusFn := js.FuncOf(func(js.Value, []js.Value) any {
		if sess := get(); sess != nil {
			sess.Term.Focus()
		}
		return nil
	})
	// run(cmd): execute one line as if the operator typed it — echoed to the
	// terminal, then submitted. The desk's default layout uses it to open a
	// terminal already showing `skywire --help` (or a foreground visor).
	runFn := js.FuncOf(func(_ js.Value, a []js.Value) any {
		if len(a) == 0 || a[0].String() == "" {
			return nil
		}
		cmd := a[0].String()
		mu.Lock()
		sess := s
		if sess == nil && !closed {
			pending = append(pending, cmd)
			mu.Unlock()
			return nil
		}
		mu.Unlock()
		if sess != nil {
			sess.Submit(cmd)
		}
		return nil
	})
	handle.Set("close", closeFn)
	handle.Set("fit", fitFn)
	handle.Set("focus", focusFn)
	handle.Set("run", runFn)
	return handle
}
