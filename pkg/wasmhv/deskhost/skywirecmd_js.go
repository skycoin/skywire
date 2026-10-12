//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/skywirecmd_js.go c3-vis-wasm
//
// The `skywire` command inside the browser shell: each invocation executes
// the FULL skywire CLI wasm module (the repo-root binary compiled for
// GOOS=js, served at /skywire.wasm) OS-style — one fresh instance per
// command, argv/env per run, output streamed into the terminal, and the
// page-shared jsfs as its filesystem. So `skywire cli config gen -rp` writes
// /opt/skywire/skywire.json and the shell's cat/jq (on the same jsfs via
// afero.NewOsFs) read it back — the real binary, the real help, the real
// behavior, in the page.
//
// The heavy lifting lives in browseui/skywire-exec.js
// (globalThis.skywireExec); this applet bridges websh's stdio to it and
// blocks until the command exits. Registered only when the page provides
// skywireExec — a served deployment without /skywire.wasm simply has no
// `skywire` command in the shell.
package deskhost

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"syscall/js"
	"time"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"

	"github.com/skycoin/skywire/pkg/proxyenv"
)

func registerSkywireCmd() {
	if !js.Global().Get("skywireExec").Truthy() {
		return
	}
	shell.RegisterApplet("skywire",
		"run the real skywire CLI (full command tree; try: skywire --help)",
		func(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
			return runSkywireWasm(ctx, s, hc, args)
		})
}

func runSkywireWasm(ctx context.Context, s *shell.Shell, hc *interp.HandlerContext, args []string) int {
	exec := js.Global().Get("skywireExec")
	if !exec.Truthy() {
		fmt.Fprintln(hc.Stderr, "skywire: skywire-exec.js not loaded") //nolint:errcheck
		return 127
	}

	jsArgs := make([]interface{}, len(args))
	for i, a := range args {
		jsArgs[i] = a
	}

	sink := func(w func(p []byte) (int, error)) js.Func {
		return js.FuncOf(func(_ js.Value, cbArgs []js.Value) interface{} {
			buf := cbArgs[0]
			b := make([]byte, buf.Get("length").Int())
			js.CopyBytesToGo(b, buf)
			_, _ = w(b) //nolint:errcheck
			return nil
		})
	}
	outF := sink(hc.Stdout.Write)
	errF := sink(hc.Stderr.Write)
	defer outF.Release()
	defer errF.Release()

	done := make(chan int, 1)
	var thenF, catchF js.Func
	thenF = js.FuncOf(func(_ js.Value, cbArgs []js.Value) interface{} {
		code := 0
		if len(cbArgs) > 0 && cbArgs[0].Type() == js.TypeNumber {
			code = cbArgs[0].Int()
		}
		done <- code
		return nil
	})
	catchF = js.FuncOf(func(_ js.Value, cbArgs []js.Value) interface{} {
		msg := "unknown error"
		if len(cbArgs) > 0 {
			msg = js.Global().Call("String", cbArgs[0]).String()
			if cbArgs[0].Type() == js.TypeObject {
				if m := cbArgs[0].Get("message"); m.Type() == js.TypeString {
					msg = m.String()
				}
			}
		}
		fmt.Fprintln(hc.Stderr, "skywire:", msg) //nolint:errcheck
		done <- 126
		return nil
	})
	defer thenF.Release()
	defer catchF.Release()

	hooks := js.ValueOf(map[string]interface{}{})
	hooks.Set("stdout", outF)
	hooks.Set("stderr", errF)
	// The command starts where the shell is, on the same tree.
	hooks.Set("cwd", hc.Dir)
	// Ctrl+C parity: the shell cancels ctx on ^C; forward that to the command
	// instance's registered interrupt so a foreground visor shuts down like it
	// would on SIGINT. hooks.instance is invoked SYNCHRONOUSLY by skywireExec
	// with { interrupt }, before the command starts.
	interruptCh := make(chan js.Value, 1)
	var child js.Value // the process handle: stdin and resize, for a terminal
	instF := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		if len(a) > 0 && a[0].Truthy() {
			child = a[0]
			if f := a[0].Get("interrupt"); f.Type() == js.TypeFunction {
				select {
				case interruptCh <- f:
				default:
				}
			}
		}
		return nil
	})
	defer instF.Release()
	hooks.Set("instance", instF)
	watchDone := make(chan struct{})
	defer close(watchDone)
	go func() {
		select {
		case <-ctx.Done():
			// Wait for the instance to register if ^C raced its startup —
			// watchDone ends the wait when the command finishes anyway.
			select {
			case f := <-interruptCh:
				f.Invoke()
			case <-watchDone:
			}
		case <-watchDone:
		}
	}()
	// Terminal identity for the command instance: the help styling (the
	// coloredcobra colors, the Matrix rain backdrop) decides by TERM/COLUMNS
	// under js — isatty can't answer through a pipe, so the host says.
	env := map[string]interface{}{}
	if s != nil && s.Size != nil {
		if cols, rows := s.Size(); cols > 0 {
			env["COLUMNS"] = fmt.Sprintf("%d", cols)
			env["LINES"] = fmt.Sprintf("%d", rows)
		}
	}
	// The shell's exported proxy variables reach the command, as a real shell
	// passes its environment on: `skywire cli got` reads ALL_PROXY the way the
	// shell's curl does. Only these: the rest of this virtual shell's
	// environment (HOME, PATH) describes its own filesystem, not the visor's.
	for _, name := range proxyenv.Names {
		if v := hc.Env.Get(name); v.Exported && v.String() != "" {
			env[name] = v.String()
		}
	}
	if len(env) > 0 {
		hooks.Set("env", js.ValueOf(env))
	}

	if term := onTerminal(s, hc); term != nil {
		hooks.Set("stdin", "pipe")
		hooks.Set("tty", term.opts)
		defer s.WithSource("local")()
		defer term.release()
		exec.Invoke(js.ValueOf(jsArgs), hooks).Call("then", thenF).Call("catch", catchF)
		return term.attach(s, hc, child, done)
	}
	exec.Invoke(js.ValueOf(jsArgs), hooks).Call("then", thenF).Call("catch", catchF)
	return <-done
}

// terminal is what a command run on the shell's own terminal is given: the
// terminal's size and raw mode, the keys typed, and resizes, as websh gives a
// program it runs from the filesystem.
type terminal struct {
	opts  js.Value
	onRaw js.Func
	raw   atomic.Bool
}

// onTerminal is a terminal for the command when its output is the shell's
// terminal, and nil when it is a pipe or a file.
func onTerminal(s *shell.Shell, hc *interp.HandlerContext) *terminal {
	if s == nil || s.Size == nil || s.IsTerminal == nil || !s.IsTerminal(hc.Stdout) {
		return nil
	}
	t := &terminal{opts: js.Global().Get("Object").New()}
	cols, rows := s.Size()
	t.opts.Set("cols", cols)
	t.opts.Set("rows", rows)
	t.onRaw = js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		on := len(a) > 0 && a[0].Truthy()
		t.raw.Store(on)
		if s.RawMode != nil {
			s.RawMode(on)
		}
		return nil
	})
	t.opts.Set("onRaw", t.onRaw)
	return t
}

func (t *terminal) release() { t.onRaw.Release() }

// attach feeds the command the keys typed and the terminal's size until it
// exits, and returns its exit code.
func (t *terminal) attach(s *shell.Shell, hc *interp.HandlerContext, child js.Value, done <-chan int) int {
	exited := make(chan struct{})
	if child.Truthy() {
		if in := child.Get("stdin"); in.Truthy() {
			go feedStdin(hc.Stdin, in, exited)
		}
		if resize := child.Get("resize"); resize.Type() == js.TypeFunction {
			go func() {
				tick := time.NewTicker(250 * time.Millisecond)
				defer tick.Stop()
				lc, lr := s.Size()
				for {
					select {
					case <-exited:
						return
					case <-tick.C:
						if c, r := s.Size(); c != lc || r != lr {
							lc, lr = c, r
							resize.Invoke(c, r)
						}
					}
				}
			}()
		}
	}
	code := <-done
	close(exited)
	if t.raw.Load() && s.RawMode != nil {
		s.RawMode(false) // it left the terminal raw: killed, or it forgot
	}
	if s.WakeStdin != nil {
		s.WakeStdin() // a key read for it after it is gone is the shell's again
	}
	return code
}

// feedStdin copies what is typed into the command's stdin until it exits.
func feedStdin(r io.Reader, stdin js.Value, exited <-chan struct{}) {
	if r == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		select {
		case <-exited:
			return
		default:
		}
		if n > 0 {
			u := js.Global().Get("Uint8Array").New(n)
			js.CopyBytesToJS(u, buf[:n])
			if !stdin.Call("write", u).Truthy() {
				return
			}
		}
		if err != nil {
			stdin.Call("close")
			return
		}
	}
}
