//go:build js && wasm

// Package jsapi exposes winbox-go through the JavaScript constructor API of
// the original WinBox.js, so a page whose JavaScript already says
// `new WinBox({...})` can be served by the Go implementation without being
// rewritten.
//
// Install puts a `WinBox` constructor on a JS object (usually globalThis).
// Instances carry the option keys, methods and properties WinBox.js documents,
// including the `g` / `window` / `dom` aliases for the root element that
// callers use to identify a window from a DOM node.
//
// The Go API (winbox.New) is the better one to write new code against; this is
// a compatibility layer for existing JavaScript. Note that it only exists once
// the wasm module has started, which is not true of a plain <script> tag — see
// the package README for the readiness pattern.
package jsapi

import (
	"strings"
	"syscall/js"

	winbox "github.com/0magnet/winbox-go"
)

// Install defines a `WinBox` constructor on target and returns it. Calling it
// more than once replaces the previous constructor; the js.Func is retained
// for the lifetime of the program, as a global constructor is never released.
func Install(target js.Value) js.Value {
	ctor := js.FuncOf(construct)
	target.Set("WinBox", ctor)
	return target.Get("WinBox")
}

// InstallGlobal installs the constructor on globalThis.
func InstallGlobal() js.Value { return Install(js.Global()) }

// construct implements `new WinBox(params)`, `new WinBox(title)` and
// `new WinBox(title, params)`. It returns the instance object, which a JS
// `new` expression uses in place of the object it created.
func construct(_ js.Value, args []js.Value) any {
	title := ""
	params := js.Undefined()
	switch {
	case len(args) >= 2:
		title = args[0].String()
		params = args[1]
	case len(args) == 1 && args[0].Type() == js.TypeString:
		title = args[0].String()
	case len(args) == 1:
		params = args[0]
	}
	return newWindow(title, params).obj
}

// wrapper couples one Go window to the JS object handed back to the caller.
type wrapper struct {
	w   *winbox.WinBox
	obj js.Value
}

func get(params js.Value, key string) js.Value {
	if !params.Truthy() {
		return js.Undefined()
	}
	return params.Get(key)
}

func str(params js.Value, key string) string {
	v := get(params, key)
	if v.Type() != js.TypeString {
		return ""
	}
	return v.String()
}

func boolOf(params js.Value, key string) bool { return get(params, key).Truthy() }

// num reads a number-or-numeric-string option. WinBox.js callers pass either
// (`border: "1"` is as common as `border: 1`), so both are accepted.
func num(params js.Value, key string) float64 {
	v := get(params, key)
	switch v.Type() {
	case js.TypeNumber:
		return v.Float()
	case js.TypeString:
		return parseFloat(v.String())
	default:
		return 0
	}
}

func parseFloat(s string) float64 {
	f := js.Global().Call("parseFloat", s)
	if f.Type() != js.TypeNumber || js.Global().Call("isNaN", f).Bool() {
		return 0
	}
	return f.Float()
}

// unit converts a WinBox.js position/dimension value to a winbox.Unit, with
// the same rules the original parser applies: a number is pixels, "center" /
// "right" / "bottom" are keywords, a trailing "%" is a percentage and any
// other string is parsed as pixels.
func unit(params js.Value, key string) winbox.Unit {
	v := get(params, key)
	switch v.Type() {
	case js.TypeNumber:
		return winbox.Px(v.Float())
	case js.TypeString:
		s := v.String()
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "center":
			return winbox.Center
		case "right":
			return winbox.Right
		case "bottom":
			return winbox.Bottom
		}
		if strings.Contains(s, "%") {
			return winbox.Pct(parseFloat(s))
		}
		return winbox.Px(parseFloat(s))
	default:
		return winbox.Unit{}
	}
}

// unitArg converts one argument of move()/resize(). An omitted or non-value
// argument yields the zero Unit, which re-applies the stored geometry — the
// no-argument path of the original move()/resize().
func unitArg(args []js.Value, i int) winbox.Unit {
	if i >= len(args) {
		return winbox.Unit{}
	}
	v := args[i]
	switch v.Type() {
	case js.TypeNumber:
		return winbox.Px(v.Float())
	case js.TypeString:
		s := v.String()
		switch strings.ToLower(strings.TrimSpace(s)) {
		case "center":
			return winbox.Center
		case "right":
			return winbox.Right
		case "bottom":
			return winbox.Bottom
		}
		if strings.Contains(s, "%") {
			return winbox.Pct(parseFloat(s))
		}
		return winbox.Px(parseFloat(s))
	default:
		return winbox.Unit{}
	}
}

// classList reads the `class` option, which WinBox.js accepts as either a
// string of space-separated names or an array of names.
func classList(params js.Value) []string {
	v := get(params, "class")
	switch v.Type() {
	case js.TypeString:
		return strings.Fields(v.String())
	case js.TypeObject:
		if !v.Truthy() {
			return nil
		}
		n := v.Length()
		out := make([]string, 0, n)
		for i := 0; i < n; i++ {
			out = append(out, v.Index(i).String())
		}
		return out
	default:
		return nil
	}
}

// falseArg reports whether the first argument is literally `false`, the
// toggle-off convention shared by focus/blur/hide/show/minimize/maximize/
// fullscreen in WinBox.js.
func falseArg(args []js.Value) bool {
	return len(args) > 0 && args[0].Type() == js.TypeBoolean && !args[0].Bool()
}

func newWindow(title string, params js.Value) *wrapper {
	wr := &wrapper{obj: js.Global().Get("Object").New()}

	opts := &winbox.Options{
		ID:         str(params, "id"),
		Title:      title,
		Icon:       str(params, "icon"),
		HTML:       str(params, "html"),
		URL:        str(params, "url"),
		Background: str(params, "background"),
		Border:     num(params, "border"),
		Header:     num(params, "header"),
		Index:      int(num(params, "index")),
		Class:      classList(params),

		Width:     unit(params, "width"),
		Height:    unit(params, "height"),
		MinWidth:  unit(params, "minwidth"),
		MinHeight: unit(params, "minheight"),
		MaxWidth:  unit(params, "maxwidth"),
		MaxHeight: unit(params, "maxheight"),
		X:         unit(params, "x"),
		Y:         unit(params, "y"),
		Top:       unit(params, "top"),
		Left:      unit(params, "left"),
		Bottom:    unit(params, "bottom"),
		Right:     unit(params, "right"),

		Autosize: boolOf(params, "autosize"),
		Overflow: boolOf(params, "overflow"),
		Min:      boolOf(params, "min"),
		Max:      boolOf(params, "max"),
		Hidden:   boolOf(params, "hidden"),
		Modal:    boolOf(params, "modal"),
	}
	if t := str(params, "title"); t != "" {
		opts.Title = t
	}
	if r := get(params, "root"); r.Truthy() {
		opts.Root = r
	}
	if t := get(params, "template"); t.Truthy() {
		opts.Template = t
	}
	if m := get(params, "mount"); m.Truthy() {
		opts.Mount = m
	}

	// Callbacks live on the instance, exactly as in WinBox.js: they are copied
	// from the options, invoked with `this` bound to the instance, and may be
	// reassigned by the caller at any time afterwards.
	for _, name := range callbackNames {
		if fn := get(params, name); fn.Type() == js.TypeFunction {
			wr.obj.Set(name, fn)
		}
	}

	wr.bindCallbacks(opts)
	wr.w = winbox.New(opts)
	wr.install()
	wr.sync()
	wr.callBack("oncreate")
	return wr
}

var callbackNames = []string{
	"oncreate", "onclose", "onfocus", "onblur", "onmove", "onresize",
	"onfullscreen", "onmaximize", "onminimize", "onrestore", "onhide",
	"onshow", "onload",
}

// callBack invokes an instance callback if the caller set one, with `this`
// bound to the instance the way a JS method call would.
func (wr *wrapper) callBack(name string, args ...any) js.Value {
	fn := wr.obj.Get(name)
	if fn.Type() != js.TypeFunction {
		return js.Undefined()
	}
	return wr.obj.Call(name, args...)
}

func (wr *wrapper) bindCallbacks(o *winbox.Options) {
	o.OnClose = func(_ *winbox.WinBox, force bool) bool {
		// A truthy return from onclose cancels the close, as in WinBox.js.
		return wr.callBack("onclose", force).Truthy()
	}
	o.OnFocus = func(*winbox.WinBox) { wr.sync(); wr.callBack("onfocus") }
	o.OnBlur = func(*winbox.WinBox) { wr.sync(); wr.callBack("onblur") }
	o.OnMove = func(_ *winbox.WinBox, x, y float64) { wr.sync(); wr.callBack("onmove", x, y) }
	o.OnResize = func(_ *winbox.WinBox, w, h float64) { wr.sync(); wr.callBack("onresize", w, h) }
	o.OnFullscreen = func(*winbox.WinBox) { wr.sync(); wr.callBack("onfullscreen") }
	o.OnMaximize = func(*winbox.WinBox) { wr.sync(); wr.callBack("onmaximize") }
	o.OnMinimize = func(*winbox.WinBox) { wr.sync(); wr.callBack("onminimize") }
	o.OnRestore = func(*winbox.WinBox) { wr.sync(); wr.callBack("onrestore") }
	o.OnHide = func(*winbox.WinBox) { wr.sync(); wr.callBack("onhide") }
	o.OnShow = func(*winbox.WinBox) { wr.sync(); wr.callBack("onshow") }
	o.OnLoad = func(*winbox.WinBox) { wr.callBack("onload") }
}

// sync copies the mutable window state onto the instance object. WinBox.js
// callers read these as plain properties, so they have to be refreshed
// whenever the window moves, resizes or changes state.
func (wr *wrapper) sync() {
	w := wr.w
	if w == nil {
		return
	}
	o := wr.obj
	o.Set("id", w.ID)
	o.Set("x", w.X)
	o.Set("y", w.Y)
	o.Set("width", w.Width)
	o.Set("height", w.Height)
	o.Set("index", w.Index)
	o.Set("min", w.Min)
	o.Set("max", w.Max)
	o.Set("full", w.Full)
	o.Set("hidden", w.Hidden)
	o.Set("focused", w.Focused)
	o.Set("title", w.Title)
	o.Set("top", w.Top)
	o.Set("right", w.Right)
	o.Set("bottom", w.Bottom)
	o.Set("left", w.Left)
	// g is what the bundled WinBox.js exposes; window and dom are the names
	// other builds use. All three are set so a caller matching a DOM node
	// against a window finds it whichever alias it reaches for.
	o.Set("g", w.DOM)
	o.Set("window", w.DOM)
	o.Set("dom", w.DOM)
	o.Set("body", w.Body)
}

// method defines a chainable instance method: fn does the work and the
// instance is returned, as every WinBox.js method except close() does.
func (wr *wrapper) method(name string, fn func(args []js.Value)) {
	wr.obj.Set(name, js.FuncOf(func(_ js.Value, args []js.Value) any {
		fn(args)
		wr.sync()
		return wr.obj
	}))
}

func (wr *wrapper) install() {
	w := wr.w

	wr.method("mount", func(a []js.Value) {
		if len(a) > 0 {
			w.Mount(a[0])
		}
	})
	wr.method("unmount", func(a []js.Value) {
		if len(a) > 0 && a[0].Truthy() {
			w.Unmount(a[0])
			return
		}
		w.Unmount()
	})
	wr.method("setTitle", func(a []js.Value) {
		if len(a) > 0 {
			w.SetTitle(a[0].String())
		}
	})
	wr.method("setIcon", func(a []js.Value) {
		if len(a) > 0 {
			w.SetIcon(a[0].String())
		}
	})
	wr.method("setBackground", func(a []js.Value) {
		if len(a) > 0 {
			w.SetBackground(a[0].String())
		}
	})
	wr.method("setUrl", func(a []js.Value) {
		if len(a) > 0 {
			// The second argument of the original setUrl was an onload
			// callback; here it is the instance's onload, so a caller passing
			// one still gets it invoked.
			if len(a) > 1 && a[1].Type() == js.TypeFunction {
				wr.obj.Set("onload", a[1])
			}
			w.SetURL(a[0].String())
		}
	})

	wr.method("focus", func(a []js.Value) {
		if falseArg(a) {
			w.Blur()
			return
		}
		w.Focus()
	})
	wr.method("blur", func(a []js.Value) {
		if falseArg(a) {
			w.Focus()
			return
		}
		w.Blur()
	})
	wr.method("hide", func(a []js.Value) {
		if falseArg(a) {
			w.Show()
			return
		}
		w.Hide()
	})
	wr.method("show", func(a []js.Value) {
		if falseArg(a) {
			w.Hide()
			return
		}
		w.Show()
	})
	wr.method("minimize", func(a []js.Value) {
		if falseArg(a) {
			w.Restore()
			return
		}
		w.Minimize()
	})
	wr.method("maximize", func(a []js.Value) {
		if falseArg(a) {
			w.Restore()
			return
		}
		w.Maximize()
	})
	wr.method("restore", func([]js.Value) { w.Restore() })
	wr.method("fullscreen", func(a []js.Value) {
		if falseArg(a) && w.Full {
			w.Restore()
			return
		}
		w.Fullscreen()
	})

	wr.method("move", func(a []js.Value) { w.Move(unitArg(a, 0), unitArg(a, 1)) })
	wr.method("resize", func(a []js.Value) { w.Resize(unitArg(a, 0), unitArg(a, 1)) })

	wr.method("addClass", func(a []js.Value) {
		if len(a) > 0 {
			w.AddClass(a[0].String())
		}
	})
	wr.method("removeClass", func(a []js.Value) {
		if len(a) > 0 {
			w.RemoveClass(a[0].String())
		}
	})
	wr.method("toggleClass", func(a []js.Value) {
		if len(a) > 0 {
			w.ToggleClass(a[0].String())
		}
	})
	wr.obj.Set("hasClass", js.FuncOf(func(_ js.Value, a []js.Value) any {
		return len(a) > 0 && w.HasClass(a[0].String())
	}))

	wr.method("addControl", func(a []js.Value) {
		if len(a) == 0 || !a[0].Truthy() {
			return
		}
		spec := a[0]
		c := winbox.Control{
			Class: str(spec, "class"),
			Image: str(spec, "image"),
			Index: int(num(spec, "index")),
		}
		if click := get(spec, "click"); click.Type() == js.TypeFunction {
			c.Click = func(event js.Value, _ *winbox.WinBox) {
				click.Invoke(event, wr.obj)
			}
		}
		w.AddControl(c)
	})
	wr.method("removeControl", func(a []js.Value) {
		if len(a) > 0 {
			w.RemoveControl(a[0].String())
		}
	})

	// close() is the one method that does not return the instance: it returns
	// true when an onclose handler canceled the close, as WinBox.js does.
	wr.obj.Set("close", js.FuncOf(func(_ js.Value, a []js.Value) any {
		canceled := w.Close(len(a) > 0 && a[0].Truthy())
		if !canceled {
			// The window is gone; leave the aliases null so a stale reference
			// reads as closed rather than pointing at a detached subtree.
			wr.obj.Set("g", js.Null())
			wr.obj.Set("window", js.Null())
			wr.obj.Set("dom", js.Null())
			wr.obj.Set("body", js.Null())
			// WinBox.js returns nothing here; only the canceled path returns
			// true. A Go nil would surface as JS null, which is a different
			// value even though both are falsy.
			return js.Undefined()
		}
		wr.sync()
		return true
	}))
}
