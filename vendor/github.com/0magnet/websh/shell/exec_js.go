//go:build js && wasm

package shell

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"syscall/js"

	"github.com/0magnet/sh/v3/expand"
	"github.com/0magnet/sh/v3/interp"
)

// execExternal runs a program from the filesystem as a child wasm process via
// bottle's proc layer (globalThis.proc). It is how a shell in the tab execs a
// compiled binary — a Go toolchain, say — parked in jsfs on the PATH.
//
// Its stdio crosses through jsfs pipes, never a Go callback: the child writes
// its stdout into a pipe (a plain JS sink), and this shell reads the other end
// with an ordinary os.File. Nothing re-enters this runtime from inside the
// child's execution slice.
func (s *Shell) execExternal(ctx context.Context, args []string) (int, bool) {
	proc := js.Global().Get("proc")
	fsjs := js.Global().Get("fs")
	if !proc.Truthy() || !fsjs.Truthy() {
		return 0, false // no process/filesystem layer on this page
	}
	hc := interp.HandlerCtx(ctx)
	bin := s.lookPath(args[0], hc.Dir, envGet(hc.Env, "PATH"))
	if bin == "" {
		return 0, false // not on the PATH: let the caller say "not found"
	}

	// A jsfs pipe per output stream; the child writes the write end via a plain
	// JS sink (no callback into this runtime), and its bytes queue in jsfs.
	makePipe := func() (r, w int) {
		p := fsjs.Call("pipe")
		return p.Index(0).Int(), p.Index(1).Int()
	}
	ro, wo := makePipe()
	re, we := makePipe()

	opts := js.Global().Get("Object").New()
	argv := js.Global().Get("Array").New()
	for _, a := range args {
		argv.Call("push", a)
	}
	opts.Set("argv", argv)
	opts.Set("cwd", hc.Dir)
	env := js.Global().Get("Object").New()
	hc.Env.Each(func(name string, vr expand.Variable) bool {
		env.Set(name, vr.String())
		return true
	})
	opts.Set("env", env)
	opts.Set("stdout", proc.Call("pipeSink", wo))
	opts.Set("stderr", proc.Call("pipeSink", we))

	res := proc.Call("spawn", opts)
	done := make(chan int, 1)
	then := js.FuncOf(func(_ js.Value, a []js.Value) any {
		code := 0
		if len(a) > 0 {
			code = a[0].Int()
		}
		done <- code
		return nil
	})
	res.Get("exited").Call("then", then)
	code := <-done
	then.Release()

	// The child is gone. Close the write ends (EOF for the read ends) and drain
	// the buffered output into the shell's stdio. Reading now — with the child's
	// runtime finished — keeps every jsfs read callback inside this runtime, so
	// nothing re-enters mid-slice. Stock syscall/fs_js.go can't read these
	// JS-created pipe fds (they aren't in its fd table), so read jsfs directly.
	fsjs.Call("pipeRelease", wo)
	fsjs.Call("pipeRelease", we)
	hc.Stdout.Write(readPipe(fsjs, ro))
	hc.Stderr.Write(readPipe(fsjs, re))
	fsjs.Call("pipeRelease", ro)
	fsjs.Call("pipeRelease", re)
	return code, true
}

// readPipe drains a jsfs pipe read fd to completion via globalThis.fs.read.
func readPipe(fsjs js.Value, fd int) []byte {
	const chunk = 1 << 16
	jsBuf := js.Global().Get("Uint8Array").New(chunk)
	var out []byte
	for {
		done := make(chan int, 1)
		cb := js.FuncOf(func(_ js.Value, a []js.Value) any {
			if len(a) > 0 && a[0].Truthy() { // read error
				done <- -1
				return nil
			}
			n := 0
			if len(a) > 1 {
				n = a[1].Int()
			}
			done <- n
			return nil
		})
		fsjs.Call("read", fd, jsBuf, 0, chunk, js.Null(), cb)
		n := <-done
		cb.Release()
		if n <= 0 { // EOF or error
			return out
		}
		b := make([]byte, n)
		js.CopyBytesToGo(b, jsBuf)
		out = append(out, b...)
	}
}

// lookPath resolves name against the shell's filesystem: a path with a slash
// as given (relative to cwd), a bare name walked down PATH. It returns the
// first match that exists and is not a directory.
func (s *Shell) lookPath(name, cwd, pathEnv string) string {
	try := func(p string) string {
		if fi, err := s.FS.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		return ""
	}
	if strings.Contains(name, "/") {
		if !path.IsAbs(name) {
			name = path.Join(cwd, name)
		}
		return try(name)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		if hit := try(path.Join(dir, name)); hit != "" {
			return hit
		}
	}
	return ""
}

func envGet(env expand.Environ, name string) string {
	if v := env.Get(name); v.IsSet() {
		return v.String()
	}
	return ""
}
