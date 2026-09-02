//go:build js && wasm

// Package main cmd/wasm-visor/skywirecmd_js.go c3-vis-wasm
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
package main

import (
	"context"
	"fmt"
	"syscall/js"

	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/websh/shell"
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
			if m := cbArgs[0].Get("message"); m.Type() == js.TypeString {
				msg = m.String()
			} else {
				msg = cbArgs[0].String()
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
	// Ctrl+C parity: the shell cancels ctx on ^C; forward that to the command
	// instance's registered interrupt so a foreground visor shuts down like it
	// would on SIGINT. hooks.instance is invoked SYNCHRONOUSLY by skywireExec
	// with { interrupt }, before the command starts.
	interruptCh := make(chan js.Value, 1)
	instF := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		if len(a) > 0 && a[0].Truthy() {
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
	if s != nil && s.Size != nil {
		if cols, rows := s.Size(); cols > 0 {
			hooks.Set("env", js.ValueOf(map[string]interface{}{
				"COLUMNS": fmt.Sprintf("%d", cols),
				"LINES":   fmt.Sprintf("%d", rows),
			}))
		}
	}

	exec.Invoke(js.ValueOf(jsArgs), hooks).Call("then", thenF).Call("catch", catchF)
	return <-done
}
