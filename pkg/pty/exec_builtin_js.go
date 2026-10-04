//go:build js && wasm

// Package pty pkg/pty/exec_builtin_js.go c3-vis-pty
package pty

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"
	"syscall/js"
)

// execBuiltin runs a command a browser visor can run: the skywire CLI, as a
// process on the page's process layer (globalThis.skywireExec). A browser
// cannot start an OS process, so anything else is refused with a reason.
func execBuiltin(ctx context.Context, req *CommandExecReq, stdout, stderr io.Writer) (code int, handled bool) {
	if path.Base(req.Name) != "skywire" {
		fmt.Fprintf(stderr, "dmsgpty: exec %s: a browser visor runs only the skywire command\n", req.Name) //nolint:errcheck
		return 127, true
	}
	run := js.Global().Get("skywireExec")
	if run.Type() != js.TypeFunction {
		fmt.Fprintln(stderr, "dmsgpty: exec skywire: no process layer on this page") //nolint:errcheck
		return 127, true
	}

	sink := func(w io.Writer) js.Func {
		return js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
			b := make([]byte, a[0].Get("length").Int())
			js.CopyBytesToGo(b, a[0])
			_, _ = w.Write(b) //nolint:errcheck
			return nil
		})
	}
	outF, errF := sink(stdout), sink(stderr)
	defer outF.Release()
	defer errF.Release()

	stdin := req.Stdin
	inF := js.FuncOf(func(_ js.Value, _ []js.Value) interface{} {
		if len(stdin) == 0 {
			return js.Null()
		}
		u := js.Global().Get("Uint8Array").New(len(stdin))
		js.CopyBytesToJS(u, stdin)
		stdin = nil
		return u
	})
	defer inF.Release()

	interrupt := make(chan js.Value, 1)
	instF := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		if len(a) > 0 && a[0].Truthy() {
			if f := a[0].Get("interrupt"); f.Type() == js.TypeFunction {
				select {
				case interrupt <- f:
				default:
				}
			}
		}
		return nil
	})
	defer instF.Release()

	done := make(chan int, 1)
	thenF := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		c := 0
		if len(a) > 0 && a[0].Type() == js.TypeNumber {
			c = a[0].Int()
		}
		done <- c
		return nil
	})
	catchF := js.FuncOf(func(_ js.Value, a []js.Value) interface{} {
		msg := "unknown error"
		if len(a) > 0 {
			msg = a[0].String()
			if m := a[0].Get("message"); m.Type() == js.TypeString {
				msg = m.String()
			}
		}
		fmt.Fprintln(stderr, "dmsgpty: exec skywire:", msg) //nolint:errcheck
		done <- 126
		return nil
	})
	defer thenF.Release()
	defer catchF.Release()

	env := map[string]interface{}{}
	for _, kv := range req.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	args := make([]interface{}, len(req.Arg))
	for i, a := range req.Arg {
		args[i] = a
	}
	hooks := js.ValueOf(map[string]interface{}{"env": env})
	hooks.Set("stdout", outF)
	hooks.Set("stderr", errF)
	hooks.Set("stdin", inF)
	hooks.Set("instance", instF)
	run.Invoke(js.ValueOf(args), hooks).Call("then", thenF).Call("catch", catchF)

	select {
	case c := <-done:
		return c, true
	case <-ctx.Done():
		select {
		case f := <-interrupt:
			f.Invoke()
		default:
		}
		<-done
		return -1, true
	}
}
