//go:build js && wasm

// Package proc is the Go adapter for bottle's process layer (proc.js): spawn
// another wasm program from the page filesystem as a child that shares this
// tab's fs and vnet, wire its stdio, and wait for it to exit.
//
// It is deliberately small and explicit rather than a drop-in for os/exec:
// os/exec on js/wasm fails in syscall.StartProcess (ENOSYS), and making the
// standard package work needs a patched GOROOT. Cmd here is the honest tab
// primitive that a shell — or, with the GOROOT overlay, cmd/go — builds on.
package proc

import (
	"errors"
	"io"
	"strings"
	"sync"
	"syscall/js"
)

// Cmd is one child process: a program in the page filesystem plus the argv,
// env, cwd and stdio to run it with. Zero value is not useful; use Command.
type Cmd struct {
	Path   string   // argv[0]: resolved against jsfs (abs, cwd-relative, or PATH)
	Args   []string // argv, including Args[0] == the program name
	Env    []string // "KEY=value"; empty inherits nothing (pass explicitly)
	Dir    string   // working directory; empty means the page cwd
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	// TTY, if set, gives the child a terminal; see TTY.
	TTY *TTY

	// Program, if set, is the program itself, for one kept somewhere other
	// than the page filesystem; Path then only names it. With Stamp, it is
	// compiled once and kept for as long as Path's Stamp stays the same, and
	// Cached says when it need not be read at all.
	Program []byte
	Stamp   string

	stdin *stdinPipe

	// OffThread runs the child in a Worker instead of on the page's one JS
	// thread, so a long build does not freeze the tab. The child still sees
	// this tab's filesystem -- it reaches jsfs over a blocking channel -- but
	// not its vnet, and its stdin reads EOF. Suitable for compute like
	// cmd/compile and cmd/link; not for a child that listens or dials.
	//
	// It needs cross-origin isolation, which is a property of how the page was
	// served (COOP/COEP headers). Where that is missing, Run falls back to an
	// on-thread child: same result, same output, just a busy main thread. Use
	// OffThreadAvailable to find out which one you will get.
	OffThread bool
}

// OffThreadAvailable reports whether OffThread children can actually run here:
// proc.js and fsbridge.js are loaded and the page is cross-origin isolated.
func OffThreadAvailable() bool {
	proc := js.Global().Get("proc")
	if !proc.Truthy() || !proc.Get("spawnWorker").Truthy() {
		return false
	}
	if !js.Global().Get("fsbridge").Truthy() {
		return false
	}
	return js.Global().Get("crossOriginIsolated").Truthy()
}

// Cached reports whether the program at path, as of stamp, is compiled and
// kept, so a Cmd with that Path and Stamp needs no Program.
func Cached(path, stamp string) bool {
	proc := js.Global().Get("proc")
	return proc.Truthy() && proc.Get("cached").Truthy() && proc.Call("cached", path, stamp).Truthy()
}

// Command builds a Cmd, mirroring os/exec.Command's shape.
func Command(name string, arg ...string) *Cmd {
	return &Cmd{Path: name, Args: append([]string{name}, arg...)}
}

// TTY makes a child a terminal program: it starts at Cols×Rows, finds its
// size and sets raw mode through Terminal, and hears of Process.Resize.
type TTY struct {
	Cols, Rows int
	// OnRaw, if set, is told each time the child turns raw mode on or off,
	// so the parent's terminal can stop (or resume) cooking its input.
	OnRaw func(raw bool)
}

// Process is a started child.
type Process struct {
	ID  string // the id the child was handed in BOTTLE_PID
	Pid int

	v     js.Value
	funcs []js.Func
	out   *outQueue
	done  chan struct{}
	code  int
	err   error
}

// Run spawns the child and blocks until it exits, returning its exit code and
// any spawn error. Stdout/Stderr receive the child's output; Stdin, if set,
// feeds its input.
func (c *Cmd) Run() (int, error) {
	p, err := c.Start()
	if err != nil {
		return -1, err
	}
	return p.Wait()
}

// Start spawns the child and returns without waiting for it.
//
// Its output reaches Stdout and Stderr as it is written, in order, from a
// goroutine of the process's own, so a writer there may block — a pipe to the
// next command — without stalling the page. Stdin is copied into the child's
// stdin pipe by another, and a child reading it waits as one reading a
// terminal does.
func (c *Cmd) Start() (*Process, error) {
	proc := js.Global().Get("proc")
	if !proc.Truthy() {
		return nil, errors.New("proc: proc.js not loaded on this page")
	}
	p := &Process{done: make(chan struct{}), out: newOutQueue()}
	fn := func(f func(_ js.Value, args []js.Value) any) js.Func {
		jf := js.FuncOf(f)
		p.funcs = append(p.funcs, jf)
		return jf
	}

	opts := js.Global().Get("Object").New()
	argv := js.Global().Get("Array").New()
	for _, a := range c.Args {
		argv.Call("push", a)
	}
	opts.Set("argv", argv)
	if c.Dir != "" {
		opts.Set("cwd", c.Dir)
	}
	env := js.Global().Get("Object").New()
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env.Set(k, v)
		}
	}
	opts.Set("env", env)
	if c.Stamp != "" {
		opts.Set("stamp", c.Stamp)
	}
	if c.Program != nil {
		u := js.Global().Get("Uint8Array").New(len(c.Program))
		js.CopyBytesToJS(u, c.Program)
		opts.Set("bytes", u)
	}

	if c.Stdout != nil {
		opts.Set("stdout", fn(p.out.sink(c.Stdout)))
	}
	if c.Stderr != nil {
		opts.Set("stderr", fn(p.out.sink(c.Stderr)))
	}

	method := "spawn"
	if c.OffThread && OffThreadAvailable() {
		method = "spawnWorker" // a worker child's stdin reads EOF, and it has no terminal
	} else {
		if c.Stdin != nil || c.stdin != nil {
			opts.Set("stdin", "pipe")
		}
		if c.TTY != nil {
			t := js.Global().Get("Object").New()
			t.Set("cols", c.TTY.Cols)
			t.Set("rows", c.TTY.Rows)
			if on := c.TTY.OnRaw; on != nil {
				t.Set("onRaw", fn(func(_ js.Value, args []js.Value) any {
					on(len(args) > 0 && args[0].Truthy())
					return nil
				}))
			}
			opts.Set("tty", t)
		}
	}

	p.v = proc.Call(method, opts)
	p.ID = p.v.Get("id").String()
	p.Pid = p.v.Get("pid").Int()
	if method == "spawn" {
		switch {
		case c.stdin != nil:
			c.stdin.set(p.v.Get("stdin"))
		case c.Stdin != nil:
			go feed(c.Stdin, p.v.Get("stdin"))
		}
	} else if c.stdin != nil {
		c.stdin.set(js.Null())
	}
	go p.out.run()
	go func() {
		code, err := await(p.v.Get("exited"))
		if err != nil {
			p.code, p.err = -1, err
		} else {
			p.code = code.Int()
		}
		// Output written before the exit was handed over on microtasks ahead
		// of it, so it is all queued by now.
		p.out.finish()
		close(p.done)
	}()
	return p, nil
}

// Wait blocks until the child exits and returns its exit code.
func (p *Process) Wait() (int, error) {
	<-p.done
	// The sinks have run and the output is written; nothing calls them now.
	for _, f := range p.funcs {
		f.Release()
	}
	p.funcs = nil
	return p.code, p.err
}

// Kill interrupts the child through its own handler when it registered one,
// and otherwise stops it outright, as kill -9 would; it then exits 130.
// Force skips the handler. It reports whether there was anything to kill.
func (p *Process) Kill(force bool) bool {
	return p.v.Call("kill", force).Truthy()
}

// Resize tells a child started with a TTY that its terminal changed size.
func (p *Process) Resize(cols, rows int) {
	p.v.Call("resize", cols, rows)
}

// feed copies r into a child's stdin pipe until r ends or the child goes.
func feed(r io.Reader, stdin js.Value) {
	defer stdin.Call("close")
	buf := make([]byte, 32*1024)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			u := js.Global().Get("Uint8Array").New(n)
			js.CopyBytesToJS(u, buf[:n])
			if !stdin.Call("write", u).Truthy() {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// StdinPipe returns a pipe to the child's stdin, as os/exec's does, for a
// caller that must decide itself when to stop feeding it: a terminal, whose
// reader must not outlive the child and eat the shell's next key. Call it
// before Start, and Close it to give the child the end of its input. A Write
// after the child has gone fails with io.ErrClosedPipe.
func (c *Cmd) StdinPipe() (io.WriteCloser, error) {
	if c.Stdin != nil {
		return nil, errors.New("proc: Stdin already set")
	}
	if c.stdin != nil {
		return nil, errors.New("proc: StdinPipe already called")
	}
	c.stdin = &stdinPipe{ready: make(chan struct{})}
	return c.stdin, nil
}

type stdinPipe struct {
	v     js.Value
	ready chan struct{}
}

func (w *stdinPipe) set(v js.Value) {
	w.v = v
	close(w.ready)
}

func (w *stdinPipe) Write(b []byte) (int, error) {
	<-w.ready
	if w.v.IsNull() {
		return 0, io.ErrClosedPipe
	}
	u := js.Global().Get("Uint8Array").New(len(b))
	js.CopyBytesToJS(u, b)
	if !w.v.Call("write", u).Truthy() {
		return 0, io.ErrClosedPipe
	}
	return len(b), nil
}

func (w *stdinPipe) Close() error {
	<-w.ready
	if !w.v.IsNull() {
		w.v.Call("close")
	}
	return nil
}

// outQueue carries a child's output from proc.js's sinks to the caller's
// writers. The sinks run as JS callbacks, where a blocking Write would stall
// every callback on the page behind it; they only queue, and run writes.
type outQueue struct {
	mu    sync.Mutex
	q     []outChunk
	kick  chan struct{}
	ended bool
	idle  chan struct{}
}

type outChunk struct {
	w io.Writer
	b []byte
}

func newOutQueue() *outQueue {
	return &outQueue{kick: make(chan struct{}, 1), idle: make(chan struct{})}
}

// sink is the callback for one stream; it is called with a Uint8Array chunk
// per write, already a copy.
func (o *outQueue) sink(w io.Writer) func(js.Value, []js.Value) any {
	return func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		b := make([]byte, args[0].Get("length").Int())
		js.CopyBytesToGo(b, args[0])
		o.mu.Lock()
		o.q = append(o.q, outChunk{w, b})
		o.mu.Unlock()
		o.poke()
		return nil
	}
}

func (o *outQueue) poke() {
	select {
	case o.kick <- struct{}{}:
	default:
	}
}

// run writes what is queued until finish, and then the rest.
func (o *outQueue) run() {
	defer close(o.idle)
	for range o.kick {
		o.mu.Lock()
		q, ended := o.q, o.ended
		o.q = nil
		o.mu.Unlock()
		for _, c := range q {
			c.w.Write(c.b) //nolint:errcheck,gosec // a writer that cannot take output is the caller's problem, not the child's
		}
		if ended && len(q) == 0 {
			return
		}
		if len(q) > 0 {
			o.poke() // look again: more may have come in while writing
		}
	}
}

// finish says nothing more will be queued, and waits for the writes.
func (o *outQueue) finish() {
	o.mu.Lock()
	o.ended = true
	o.mu.Unlock()
	o.poke()
	<-o.idle
}

// await blocks a goroutine on a JS promise, returning its resolved value or a
// rejection as an error.
func await(promise js.Value) (js.Value, error) {
	type result struct {
		v   js.Value
		err error
	}
	ch := make(chan result, 1)
	then := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var v js.Value
		if len(args) > 0 {
			v = args[0]
		}
		ch <- result{v: v}
		return nil
	})
	defer then.Release()
	catch := js.FuncOf(func(_ js.Value, args []js.Value) any {
		msg := "promise rejected"
		if len(args) > 0 {
			msg = args[0].Call("toString").String()
		}
		ch <- result{err: errors.New(msg)}
		return nil
	})
	defer catch.Release()
	promise.Call("then", then).Call("catch", catch)
	r := <-ch
	return r.v, r.err
}
