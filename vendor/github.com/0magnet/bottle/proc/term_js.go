//go:build js && wasm

package proc

import (
	"errors"
	"io"
	"os"
	"syscall/js"
)

// Terminal is a child's own terminal, when its parent started it with a TTY:
// the size to draw at, raw mode, and word of resizes. Reading it is reading
// the keys typed, and writing it is drawing on the screen, as stdin and stdout
// are on any Unix. Use it for both when the program may be built by stock
// TinyGo, whose os.Stdin cannot read.
type Terminal struct {
	v      js.Value
	resize []js.Func
}

// Term returns this program's terminal, or false when it has none: it was
// not spawned by proc, or was spawned without a TTY. Call it from main's
// goroutine before anything else runs, where a program that was handed no
// environment is still found by the process it is.
func Term() (*Terminal, bool) {
	proc := js.Global().Get("proc")
	if !proc.Truthy() || !proc.Get("tty").Truthy() {
		return nil, false
	}
	id := Getenv("BOTTLE_PID")
	if id == "" {
		return nil, false
	}
	v := proc.Call("tty", id)
	if !v.Truthy() {
		return nil, false
	}
	return &Terminal{v: v}, true
}

// Getenv is os.Getenv for a program proc spawned, which works whichever
// toolchain built it: stock TinyGo hands a program no environment, so what
// os.Getenv cannot find is asked of proc, which knows the one it was given.
func Getenv(name string) string {
	if v, ok := os.LookupEnv(name); ok {
		return v
	}
	proc := js.Global().Get("proc")
	if !proc.Truthy() || !proc.Get("environ").Truthy() {
		return ""
	}
	if env := proc.Call("environ"); env.Truthy() {
		if v := env.Get(name); v.Type() == js.TypeString {
			return v.String()
		}
	}
	return ""
}

// Args is os.Args for a program proc spawned, which works whichever
// toolchain built it: TinyGo hands a js program no arguments (its os.Args is
// a placeholder), so they are asked of proc, which knows the ones it was
// given. Elsewhere it is os.Args.
func Args() []string {
	proc := js.Global().Get("proc")
	if !proc.Truthy() || !proc.Get("argv").Truthy() {
		return os.Args
	}
	argv := proc.Call("argv")
	if !argv.Truthy() {
		return os.Args
	}
	args := make([]string, argv.Length())
	for i := range args {
		args[i] = argv.Index(i).String()
	}
	return args
}

// Size is the terminal's size in cells.
func (t *Terminal) Size() (cols, rows int) {
	s := t.v.Call("size")
	return s.Index(0).Int(), s.Index(1).Int()
}

// SetRaw turns raw mode on or off. In raw mode the parent passes every key
// through as typed, Ctrl+C among them; cooked, it echoes, edits a line and
// interrupts on Ctrl+C.
func (t *Terminal) SetRaw(on bool) {
	t.v.Call("setRaw", on)
}

// OnResize calls f, on a goroutine of its own, each time the terminal
// changes size.
func (t *Terminal) OnResize(f func(cols, rows int)) {
	jf := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) >= 2 {
			go f(args[0].Int(), args[1].Int())
		}
		return nil
	})
	t.resize = append(t.resize, jf)
	t.v.Call("onResize", jf)
}

// Read waits for keys and returns them; 0 and io.EOF at the end of the input.
func (t *Terminal) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	buf := js.Global().Get("Uint8Array").New(len(p))
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	cb := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 && args[0].Truthy() {
			ch <- res{err: errors.New(args[0].Get("message").String())}
			return nil
		}
		n := 0
		if len(args) > 1 {
			n = args[1].Int()
		}
		ch <- res{n: n}
		return nil
	})
	defer cb.Release()
	t.v.Call("read", buf, cb)
	r := <-ch
	if r.err != nil {
		return 0, r.err
	}
	if r.n == 0 {
		return 0, io.EOF
	}
	js.CopyBytesToGo(p[:r.n], buf.Call("subarray", 0, r.n))
	return r.n, nil
}

// Write draws p on the terminal.
func (t *Terminal) Write(p []byte) (int, error) {
	buf := js.Global().Get("Uint8Array").New(len(p))
	js.CopyBytesToJS(buf, p)
	t.v.Call("write", buf)
	return len(p), nil
}
