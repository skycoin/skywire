//go:build js && wasm

// Package widget is how a program offers websh a widget to place over its
// cells (OSC 7337 place with "widget": name) when it is not compiled into the
// page: a program run from the filesystem, which is a wasm instance of its
// own. The page's own widgets go through web.RegisterWidget; this is the same
// offer from the other side of a process boundary.
//
// The registry is a plain object on the page, globalThis.webshWidgets, name ->
// {mount, owner}. Offering a widget is only a property written into it, never
// a call into websh, and websh calls mount (el) => unmount on a microtask of
// its own: two Go programs never run on one stack, which would corrupt both.
// What a program offered is withdrawn when it exits.
package widget

import (
	"encoding/json"
	"syscall/js"

	"github.com/0magnet/bottle/proc"

	"github.com/0magnet/websh/progressive"
)

// Global is the name of the registry on the page.
const Global = "webshWidgets"

func registry() js.Value {
	g := js.Global()
	r := g.Get(Global)
	if !r.Truthy() {
		r = g.Get("Object").New()
		g.Set(Global, r)
	}
	return r
}

// Register offers w as name. websh calls it with the element the placement
// fills, once that element is placed, and calls what it returns (if not nil)
// when the placement is taken away. w runs as a JS callback: it must not
// block.
func Register(name string, w func(el js.Value) (unmount func())) {
	RegisterConn(name, func(el js.Value, _ *Conn) func() { return w(el) })
}

// RegisterConn offers w as name, as Register does, and gives it a line to
// the program that places it: what the widget Sends reaches the program as
// an event on its input (progressive.Filter reads them), when it placed the
// widget with Events; what the program Posts (progressive.Post) reaches the
// widget's OnMessage.
//
// The line runs through the host even when the widget is in the program's
// own process, so a widget behaves the same offered from the tab or shipped
// from another machine.
func RegisterConn(name string, w func(el js.Value, c *Conn) (unmount func())) {
	mount := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) == 0 {
			return nil
		}
		c := &Conn{}
		if len(args) > 1 && args[1].Truthy() {
			c.port = args[1]
		}
		un := w(args[0], c)
		if un == nil {
			return nil
		}
		var f js.Func
		f = js.FuncOf(func(js.Value, []js.Value) any {
			un()
			c.close()
			f.Release()
			return nil
		})
		return f
	})
	e := js.Global().Get("Object").New()
	e.Set("mount", mount)
	e.Set("owner", owner())
	registry().Set(name, e)
}

// owner is this program's process id under bottle's proc, or "" for the page.
func owner() string {
	return proc.Getenv("BOTTLE_PID")
}

// Find returns the mount function offered as name.
func Find(name string) (js.Value, bool) {
	e := registry().Get(name)
	if !e.Truthy() || e.Get("mount").Type() != js.TypeFunction {
		return js.Value{}, false
	}
	return e.Get("mount"), true
}

// Drop withdraws every widget a process offered; websh calls it when the
// process exits.
func Drop(owner string) {
	if owner == "" {
		return
	}
	r := registry()
	keys := js.Global().Get("Object").Call("keys", r)
	for i := 0; i < keys.Length(); i++ {
		k := keys.Index(i).String()
		if e := r.Get(k); e.Truthy() && e.Get("owner").String() == owner {
			r.Delete(k)
		}
	}
}

// Mount calls a mount function found by Find with el and port — the
// widget's end of its line to the program, a MessagePort — on a microtask,
// and hands done the unmount it returns: a function to call, through
// Unmount, or undefined.
func Mount(mount, el, port js.Value, done func(unmount js.Value)) {
	var cb js.Func
	cb = js.FuncOf(func(_ js.Value, args []js.Value) any {
		cb.Release()
		v := js.Undefined()
		if len(args) > 0 {
			v = args[0]
		}
		done(v)
		return nil
	})
	js.Global().Get("Promise").Call("resolve").
		Call("then", mount.Call("bind", js.Null(), el, port)).
		Call("then", cb, cb)
}

// Unmount calls an unmount function Mount handed over, on a microtask.
func Unmount(unmount js.Value) {
	if unmount.Type() == js.TypeFunction {
		js.Global().Get("Promise").Call("resolve").Call("then", unmount)
	}
}

// Shown reports whether this program's output goes to a terminal that shows
// placements: the host said so when asked (progressive.Probe, which childtty
// runs), or, from a websh that predates asking, by WEBSH_PLACEMENTS=1.
// Elsewhere the sequences are ignored and the cells are the picture.
func Shown() bool {
	return progressive.Current().Has("place") || proc.Getenv("WEBSH_PLACEMENTS") == "1"
}

// Offered reports whether this program may offer widgets of its own: it runs
// in the host's tab and the host trusts it to.
func Offered() bool {
	return progressive.Current().Has("widget.offer") || proc.Getenv("WEBSH_PLACEMENTS") == "1"
}

// Conn is a widget's line to the program that placed it. Messages are JSON,
// in both directions, carried by a MessagePort the host made for the
// placement: delivered later, never inside the sender's call, so the widget
// and the host (two Go programs, perhaps) never run on one stack.
type Conn struct {
	port js.Value
	fn   js.Func
}

// Send gives v, as JSON, to the program, as an event on its input. It is
// dropped where there is no line: a host that gives none, or a program that
// did not ask for events.
func (c *Conn) Send(v any) error {
	if c == nil || !c.port.Truthy() {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	c.port.Call("postMessage", string(b))
	return nil
}

// OnMessage calls f with each message the program posts to this widget, as
// JSON. f runs as a JS callback: it must not block.
func (c *Conn) OnMessage(f func(data []byte)) {
	if c == nil || !c.port.Truthy() {
		return
	}
	c.fn.Release()
	c.fn = js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			if d := args[0].Get("data"); d.Type() == js.TypeString {
				f([]byte(d.String()))
			}
		}
		return nil
	})
	c.port.Set("onmessage", c.fn)
}

func (c *Conn) close() {
	if c.port.Truthy() {
		c.port.Set("onmessage", js.Null())
		c.port.Call("close")
	}
	c.fn.Release()
}
