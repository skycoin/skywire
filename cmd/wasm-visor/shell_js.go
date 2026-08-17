//go:build js && wasm

// Package main cmd/wasm-visor/shell_js.go c3-vis-wasm
// A real shell for the browser visor. The wasm visor has no host to run a
// dmsgpty against — browse.js says as much where it opens terminal windows —
// so the "visor cli" window has been a bespoke REPL in JS: one command per
// line, no pipes, no scripting, its own history and alias table.
//
// This replaces it with websh (github.com/0magnet/websh): a Bash/POSIX
// interpreter over an in-memory filesystem, rendered by the xterm.js Go port
// (github.com/0magnet/xterm-go), with the visor's own API as applets whose
// JSON output pipes into the shell's jq:
//
//	about | jq -r '.public_key, .build.version'
//	tps | jq -r '.[].type' | sort | uniq -c
//	for pk in $(visors -c | jq -r '.[].local_pk'); do echo "$pk"; done
//
// # One binary, two roles
//
// The terminal needs a DOM; the visor normally runs in a SharedWorker, which
// has none (hv-boot.js: one visor per origin, tabs talk to it over a
// postMessage proxy). Rather than ship a second wasm binary, THIS binary
// carries both roles and picks at startup:
//
//   - role "visor" (default): the existing visor. If it happens to have a DOM
//     — the in-page host fallback — it ALSO installs the shell, so no second
//     instance is needed there.
//   - role "shell" (globalThis.__SKYWIRE_WASM_ROLE__ = "shell" before
//     instantiating): install only the terminal. browse.js loads this in the
//     tab when the visor lives in a worker.
//
// The visor applets follow the same principle: when this instance IS the visor
// they call wasmhv.Core.ServeHTTP directly, and otherwise they go through the
// public globalThis.skywireVisor proxy — so one code path serves both roles.
//
// Exposed to the UI as skywireShell.open(el) → { close, fit, focus }.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	xterm "github.com/0magnet/xterm-go"
)

// wasmRole reports how this instance was invoked: "shell" for the DOM-side
// terminal instance, "visor" (default) otherwise.
func wasmRole() string {
	if r := js.Global().Get("__SKYWIRE_WASM_ROLE__"); r.Type() == js.TypeString {
		return r.String()
	}
	return "visor"
}

// hasDOM reports whether this instance can touch the document — false in a
// (Shared)Worker.
func hasDOM() bool {
	d := js.Global().Get("document")
	return d.Truthy() && d.Get("createElement").Type() == js.TypeFunction
}

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

// jsFsReadDir(path) → [{name,dir,size,mtime}] sorted dirs-first then name.
func jsFsReadDir(_ js.Value, args []js.Value) any {
	infos, err := afero.ReadDir(sharedShellFS(), fsArgPath(args))
	if err != nil {
		return fsErr(err)
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
	return out
}

// jsFsReadFile(path) → {text} for a UTF-8 file, or {error}. Caps at 2 MiB so a
// huge file can't wedge the single-threaded runtime rendering it.
func jsFsReadFile(_ js.Value, args []js.Value) any {
	b, err := afero.ReadFile(sharedShellFS(), fsArgPath(args))
	if err != nil {
		return fsErr(err)
	}
	if len(b) > 2<<20 {
		return map[string]interface{}{"error": "file too large to view (>2 MiB)"}
	}
	return map[string]interface{}{"text": string(b), "size": float64(len(b))}
}

func jsFsWriteFile(_ js.Value, args []js.Value) any {
	if len(args) < 2 || args[1].Type() != js.TypeString {
		return fsErr(errors.New("writeFile(path, text)"))
	}
	if err := afero.WriteFile(sharedShellFS(), fsArgPath(args), []byte(args[1].String()), 0o644); err != nil {
		return fsErr(err)
	}
	return map[string]interface{}{"ok": true}
}

func jsFsMkdir(_ js.Value, args []js.Value) any {
	if err := sharedShellFS().MkdirAll(fsArgPath(args), 0o755); err != nil {
		return fsErr(err)
	}
	return map[string]interface{}{"ok": true}
}

func jsFsRemove(_ js.Value, args []js.Value) any {
	if err := sharedShellFS().RemoveAll(fsArgPath(args)); err != nil {
		return fsErr(err)
	}
	return map[string]interface{}{"ok": true}
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

// visorAPI calls the hypervisor API: in-process when this instance is the
// visor, else over the skywireVisor proxy the tab already holds.
func visorAPI(method, path string, body []byte) (status int, out []byte, err error) {
	if hvCore != nil {
		status, out = hvCore.ServeHTTP(method, path, body)
		return status, out, nil
	}
	v := js.Global().Get("skywireVisor")
	if !v.Truthy() || v.Get("hvApi").Type() != js.TypeFunction {
		return 0, nil, errors.New("no visor in this tab")
	}
	var arg any
	if body != nil {
		arg = string(body)
	}
	res, err := jsAwait(v.Call("hvApi", method, path, arg))
	if err != nil {
		return 0, nil, err
	}
	status = res.Get("status").Int()
	b := res.Get("body")
	if b.Truthy() {
		out = make([]byte, b.Get("length").Int())
		js.CopyBytesToGo(out, b)
	}
	return status, out, nil
}

// visorPK returns this tab's visor public key, from whichever side holds it.
// visorPK returns the visor's public key for the prompt, WITHOUT waiting for
// it. The first call that misses starts a background fetch and returns empty;
// the prompt says "visor" until the answer lands, and every prompt after that
// is served from memory.
//
// It must not wait, and this is not a matter of taste. Where the visor lives in
// a worker, skywireVisor is a postMessage proxy: every method returns a promise
// that only settles once the JS event loop runs. This is called from prompt(),
// which is called from the line editor's redraw, which is called from the
// terminal's data handler — a js.Func — and from openShell, itself a js.Func.
// The standard-Go wasm runtime hands control back to the browser only when
// every goroutine is blocked, and a js.Func callback must return before that
// can happen. So waiting here waits for a reply that cannot arrive until we
// stop waiting: the renderer spins inside wasm at full CPU, the page stops
// answering anything, the terminal window will not drag, and a stack sample
// shows a dozen frames of wasm under the Chromium frames.
//
// It looked fine everywhere it was tested. With the visor in-page selfPK is
// already set, so the wait never happens; on a bare page skywireVisor is absent
// and it returns immediately; under TinyGo the asyncify scheduler can suspend a
// callback and return to JS, so the same code works. Only the split this build
// uses — visor in a SharedWorker, shell a second instance in the tab — reaches
// it, and only under standard Go.
//
// A goroutine may block on the proxy safely: it is not a callback, so the
// runtime can park and let the event loop deliver the reply.
var (
	visorPKCache    atomic.Value // string
	visorPKFetching atomic.Bool
)

func visorPK() string {
	if !selfPK.Null() {
		return selfPK.Hex()
	}
	if pk, _ := visorPKCache.Load().(string); pk != "" {
		return pk
	}
	// Not known yet. Fetch it off the callback stack, one attempt at a time —
	// a failed attempt is retried by the next prompt rather than cached as "".
	if visorPKFetching.CompareAndSwap(false, true) {
		go func() {
			defer visorPKFetching.Store(false)
			v := js.Global().Get("skywireVisor")
			if !v.Truthy() || v.Get("status").Type() != js.TypeFunction {
				return
			}
			st, err := jsAwait(v.Call("status"))
			if err != nil || !st.Truthy() {
				return
			}
			if pk := st.Get("pk"); pk.Type() == js.TypeString && pk.String() != "" {
				visorPKCache.Store(pk.String())
			}
		}()
	}
	return ""
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
			pk := visorPK()
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
		pk := visorPK()
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
})

// shellSession is one open terminal window: a terminal, a shell, and the
// plumbing between them.
type shellSession struct {
	term   *xterm.Terminal
	sh     *shell.Shell
	editor *shell.LineEditor

	running   bool
	rawInput  bool
	cancelRun context.CancelFunc
	stdinW    *io.PipeWriter
	lines     chan string

	funcs []js.Func // released on close
}

// openShell builds a terminal + shell inside el and starts the read loop.
func openShell(el js.Value) *shellSession {
	registerVisorApplets()
	// the browser's own console, reachable from the shell: `js <expr>` and
	// `logs` (which backfills from browse.js's window.skywireLog, so visor
	// output from before this window opened is there too)
	browser.Register()
	// the mesh as the shell's network: dcurl / dial / aliases, addressing
	// peers by public key over the visor's dmsg session
	registerMeshApplets()

	term := xterm.New(nil)
	term.Open(el)
	term.Fit()
	if err := term.EnableWebGL(); err != nil {
		fmt.Println("wasm-visor: shell using DOM renderer:", err)
	}

	s := &shellSession{term: term, lines: make(chan string, 8)}
	out := shellWriter{term}
	stdinR, stdinW := io.Pipe()
	s.stdinW = stdinW

	vfs := sharedShellFS()
	sh, err := shell.New(vfs, stdinR, out, out)
	if err != nil {
		term.WriteString("failed to start the shell: " + err.Error() + "\r\n")
		return s
	}
	s.sh = sh
	sh.RawMode = func(on bool) { s.rawInput = on }
	sh.Size = func() (int, int) { return term.Core.Cols(), term.Core.Rows() }
	if err := sh.PopulateBin(); err != nil {
		term.WriteString("failed to populate /bin: " + err.Error() + "\r\n")
	}

	s.editor = &shell.LineEditor{
		Echo: func(str string) { term.WriteString(str) },
		Redraw: func(content string, back int) {
			line := "\r\x1b[2K" + s.prompt() + content
			if back > 0 {
				line += fmt.Sprintf("\x1b[%dD", back)
			}
			term.WriteString(line)
		},
		Submit:      func(line string) { s.lines <- line },
		Interrupt:   func() { s.sh.CancelPending(); s.writePrompt() },
		EOF:         func() { term.WriteString("\r\n(close the window to end this session)\r\n"); s.writePrompt() },
		ClearScreen: func() { term.WriteString("\x1b[2J\x1b[H"); s.writePrompt(); term.WriteString(s.editor.Line()) },
		Complete:    s.complete,
	}

	term.Core.OnData = func(data string) {
		switch {
		case s.running && s.rawInput:
			// a full-screen applet (edit, less) owns the terminal
			go func() { _, _ = s.stdinW.Write([]byte(data)) }() //nolint:errcheck
		case s.running:
			if strings.Contains(data, "\x03") {
				if s.cancelRun != nil {
					s.cancelRun()
				}
				return
			}
			term.WriteString(strings.ReplaceAll(data, "\r", "\r\n"))
			go func() { _, _ = s.stdinW.Write([]byte(strings.ReplaceAll(data, "\r", "\n"))) }() //nolint:errcheck
		default:
			s.editor.Input(data)
		}
	}

	// the history builtin reads the line editor's list
	if err := sh.UseHistory(s.editor.History, s.editor.ClearHistory); err != nil {
		term.WriteString("history unavailable: " + err.Error() + "\r\n")
	}

	// keep the grid in step with the WinBox window as it is resized
	if ro := js.Global().Get("ResizeObserver"); ro.Truthy() {
		cb := js.FuncOf(func(js.Value, []js.Value) any {
			term.Fit()
			return nil
		})
		s.funcs = append(s.funcs, cb)
		ro.New(cb).Call("observe", el)
	}

	go s.run()

	term.WriteString("\x1b[1;36mwebsh\x1b[0m — a shell in the visor tab · type \x1b[1mhelp\x1b[0m\r\n")
	term.WriteString("visor commands: \x1b[1mpk about visors net health apps tps routes hvapi\x1b[0m " +
		"(JSON — pipe into \x1b[1mjq\x1b[0m)\r\n")
	term.WriteString("browser: \x1b[1mlogs\x1b[0m (-f follow, -e errors) · \x1b[1mjs\x1b[0m <expr> · " +
		"\x1b[1mcurl download upload pbcopy\x1b[0m\r\n")
	term.WriteString("mesh: \x1b[1mdcurl\x1b[0m dmsg://<pk|alias>[:port][/path] · \x1b[1mdial\x1b[0m <pk> · " +
		"\x1b[1maliases\x1b[0m\r\n\r\n")
	s.writePrompt()
	return s
}

// shellWriter adapts shell output (LF) to the terminal (CRLF).
type shellWriter struct{ term *xterm.Terminal }

func (w shellWriter) Write(p []byte) (int, error) {
	w.term.WriteString(strings.ReplaceAll(string(p), "\n", "\r\n"))
	return len(p), nil
}

func (s *shellSession) prompt() string {
	if s.sh == nil {
		return "$ "
	}
	if s.sh.Pending() {
		return "\x1b[1;33m>\x1b[0m "
	}
	dir := s.sh.Dir()
	if strings.HasPrefix(dir, "/home/user") {
		dir = "~" + dir[len("/home/user"):]
	}
	name := "visor"
	if pk := visorPK(); len(pk) >= 6 {
		name = pk[:6]
	}
	return "\x1b[1;32m" + name + "\x1b[0m:\x1b[1;34m" + dir + "\x1b[0m$ "
}

func (s *shellSession) writePrompt() { s.term.WriteString(s.prompt()) }

// complete resolves tab completion: commands for the first word, virtual
// filesystem paths after it.
func (s *shellSession) complete(word string, isFirstWord bool) []string {
	if isFirstWord && !strings.Contains(word, "/") {
		var out []string
		for _, name := range append(shell.AppletNames(), shellBuiltins...) {
			if strings.HasPrefix(name, word) {
				out = append(out, name)
			}
		}
		sort.Strings(out)
		return out
	}
	dir, base := filepath.Split(word)
	search := dir
	if !filepath.IsAbs(search) {
		search = filepath.Join(s.sh.Dir(), dir)
	}
	infos, err := afero.ReadDir(s.sh.FS, filepath.Clean(search))
	if err != nil {
		return nil
	}
	var out []string
	for _, info := range infos {
		if !strings.HasPrefix(info.Name(), base) {
			continue
		}
		cand := dir + info.Name()
		if info.IsDir() {
			cand += "/"
		}
		out = append(out, cand)
	}
	sort.Strings(out)
	return out
}

// shellBuiltins are the interpreter builtins offered by tab completion.
var shellBuiltins = []string{
	"cd", "pwd", "echo", "printf", "read", "exit", "export", "unset",
	"source", "test", "true", "false", "set", "shift", "local", "declare",
	"eval", "alias", "unalias", "type", "return", "break", "continue",
	"pushd", "popd", "dirs", "let", "getopts", "wait",
	"jobs", "kill", "disown", "fg", "bg", "enable", "compgen", "history",
	"builtin", "umask", "times", "trap", "shopt", "mapfile", "readarray",
}

// run executes submitted lines one at a time, off the JS event loop so the
// terminal stays responsive while a command works.
func (s *shellSession) run() {
	for line := range s.lines {
		if !s.sh.Pending() {
			s.editor.AddHistory(line)
		}
		// Canceling this at the end of the line is safe: the interpreter
		// detaches background jobs from it, so `sleep 30 &` survives to the
		// next prompt as it would in bash. close() stops them instead.
		runCtx, cancel := context.WithCancel(ctx)
		s.cancelRun = cancel
		s.running = true

		_, err := s.sh.Run(runCtx, line)

		s.running = false
		s.cancelRun = nil
		cancel()

		if err != nil && !strings.HasPrefix(err.Error(), "exit status") {
			s.term.WriteString("websh: " + strings.ReplaceAll(err.Error(), "\n", "\r\n") + "\r\n")
		}
		s.writePrompt()
	}
}

func (s *shellSession) close() {
	if s.lines != nil {
		close(s.lines)
		s.lines = nil
	}
	if s.cancelRun != nil {
		s.cancelRun()
	}
	if s.sh != nil {
		// Background jobs outlive the line that started them, so closing the
		// window is what ends them.
		s.sh.Runner.StopJobs()
	}
	if s.stdinW != nil {
		_ = s.stdinW.Close() //nolint:errcheck
	}
	if s.term != nil {
		s.term.Dispose()
	}
	for _, f := range s.funcs {
		f.Release()
	}
	s.funcs = nil
}

// jsOpenShell implements skywireShell.open(el): mount a terminal running websh
// in the given element (or element id). Returns { close, fit, focus }.
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
	s := openShell(el)

	handle := js.Global().Get("Object").New()
	closeFn := js.FuncOf(func(js.Value, []js.Value) any { s.close(); return nil })
	fitFn := js.FuncOf(func(js.Value, []js.Value) any { s.term.Fit(); return nil })
	focusFn := js.FuncOf(func(js.Value, []js.Value) any { s.term.Focus(); return nil })
	s.funcs = append(s.funcs, closeFn, fitFn, focusFn)
	handle.Set("close", closeFn)
	handle.Set("fit", fitFn)
	handle.Set("focus", focusFn)
	return handle
}
