<h1>
    <img src="https://cdn.jsdelivr.net/gh/nextapps-de/winbox@master/demo/winbox-gradient.svg" alt="WinBox: A modern HTML5 window manager for the web." width="100%">
</h1>
<h3>winbox-go: a full Go/WebAssembly port of WinBox.js — modern window manager for the web: lightweight, no dependencies, fully customizable, open source!</h3>

<a href="https://0magnet.github.io/winbox-go/">Demo</a> &ensp;&bull;&ensp; <a href="#started">Getting Started</a> &ensp;&bull;&ensp; <a href="#options">Options</a> &ensp;&bull;&ensp; <a href="#api">API</a> &ensp;&bull;&ensp; <a href="#themes">Themes</a> &ensp;&bull;&ensp; <a href="#customize">Customize</a> &ensp;&bull;&ensp; <a href="#jsapi">From JavaScript</a> &ensp;&bull;&ensp; <a href="#differences">Differences from WinBox.js</a>

This is a complete port of [WinBox.js](https://github.com/nextapps-de/winbox) by Thomas Wilkerling to Go, targeting WebAssembly via `syscall/js`. It compiles with both the **standard Go toolchain** (`GOOS=js GOARCH=wasm`) and **TinyGo** (`-target wasm`), and ports the entire feature set: drag, 8-direction resize, minimize with split-screen taskbar stacking, maximize, browser fullscreen, modals, DOM mount/unmount, iframes, custom controls and templates, viewport limits, percentage/centered positioning, and all lifecycle callbacks.

The library is fully self-contained: the stylesheet is embedded in the Go binary with all control icons inlined as data URIs, and is injected automatically when the first window is created — no additional assets to serve.

If you find the underlying window manager useful, consider [supporting the original WinBox.js project](https://github.com/nextapps-de/winbox#support-this-project).

<a name="demo"></a>
### Live Demo

<a href="https://0magnet.github.io/winbox-go/">https://0magnet.github.io/winbox-go/</a> (compiled with TinyGo)

<a name="started"></a>
## Getting Started

```cmd
go get github.com/0magnet/winbox-go
```

```go
package main

import winbox "github.com/0magnet/winbox-go"

func main() {
    winbox.New(&winbox.Options{
        Title: "Window Title",
        HTML:  "<h1>Hello from Go</h1>",
    })

    select {} // keep the Go runtime alive for callbacks
}
```

Build with the standard Go toolchain:

```cmd
GOOS=js GOARCH=wasm go build -o app.wasm .
cp "$(go env GOROOT)/lib/wasm/wasm_exec.js" .
```

Or build with TinyGo for a much smaller binary (~340 kB before compression):

```cmd
tinygo build -o app.wasm -target wasm -no-debug .
cp "$(tinygo env TINYGOROOT)/targets/wasm_exec.js" .
```

Load it from a page (no stylesheet needed — it's embedded):

```html
<html>
<head><meta charset="utf-8"></head>
<body>
    <script src="wasm_exec.js"></script>
    <script>
        const go = new Go();
        WebAssembly.instantiateStreaming(fetch("app.wasm"), go.importObject)
            .then(result => go.run(result.instance));
    </script>
</body>
</html>
```

See <a href="examples/demo">examples/demo</a> for a runnable example with a build script for both toolchains.

<a name="units"></a>
## Units

Positions and dimensions are expressed with the `winbox.Unit` type, replacing the `number | string` parameters of WinBox.js:

| Go | WinBox.js equivalent |
|---|---|
| `winbox.Px(300)` | `300` |
| `winbox.Pct(50)` | `"50%"` |
| `winbox.Center` | `"center"` |
| `winbox.Right` | `"right"` (x-axis) |
| `winbox.Bottom` | `"bottom"` (y-axis) |
| zero value (unset) | option omitted |

<a name="api"></a>
## Overview

Constructor:

- winbox.**New**(*winbox.Options) : *WinBox

Global functions:

- winbox.**Stack**() : []*WinBox — all open windows ordered by focus history
- winbox.**CSS**() : string — the embedded stylesheet
- winbox.**InjectCSS**() — inject the stylesheet manually (otherwise automatic on first `New`)

Instance methods (chainable, mirroring WinBox.js 1:1):

- w.**Mount**(src js.Value)
- w.**Unmount**(dest ...js.Value)
- w.**SetURL**(url string)
- w.**SetTitle**(title string)
- w.**SetIcon**(url string)
- w.**SetBackground**(background string)
- w.**Move**(x, y winbox.Unit)
- w.**Resize**(width, height winbox.Unit)
- w.**Close**(force bool) : bool
- w.**Focus**() / w.**Blur**()
- w.**Hide**() / w.**Show**()
- w.**Minimize**() / w.**Maximize**() / w.**Fullscreen**() / w.**Restore**()
- w.**AddClass**(name string) / w.**RemoveClass**(name string) / w.**HasClass**(name string) : bool / w.**ToggleClass**(name string)
- w.**AddControl**(winbox.Control) / w.**RemoveControl**(name string)

Instance fields (read/write like the JS properties):

- w.**ID** string, w.**Index** int — identity and z-index
- w.**DOM**, w.**Body** js.Value — the outer window element and the content body element
- w.**X**, w.**Y**, w.**Width**, w.**Height** float64 — geometry (call `Move`/`Resize` with zero Units to re-apply after direct writes)
- w.**Top**, w.**Right**, w.**Bottom**, w.**Left** float64 — viewport limits
- w.**MinWidth**, w.**MinHeight**, w.**MaxWidth**, w.**MaxHeight** float64
- w.**Min**, w.**Max**, w.**Full**, w.**Hidden**, w.**Focused** bool — window state (read-only; use the methods to change state)

Callbacks (assignable via `Options` or swapped on the instance at any time):

- w.**OnClose**, w.**OnFocus**, w.**OnBlur**, w.**OnMove**, w.**OnResize**, w.**OnFullscreen**, w.**OnMaximize**, w.**OnMinimize**, w.**OnRestore**, w.**OnHide**, w.**OnShow**, w.**OnLoad** (plus **OnCreate** in `Options`)

<a name="options" id="options"></a>
## Options

<table>
    <tr>
        <td>Option</td>
        <td>Type</td>
        <td>Description</td>
    </tr>
    <tr>
        <td>ID</td>
        <td>string</td>
        <td>Set a unique id to the window. Used to define custom styles in css, query elements by context or just to identify the corresponding window instance. If no ID was set it will automatically create one for you.</td>
    </tr>
    <tr>
        <td>Index</td>
        <td>int</td>
        <td>Set the initial <code>z-index</code> of the window to this value (will be increased automatically when unfocused/focused). Zero means automatic.</td>
    </tr>
    <tr>
        <td>Title</td>
        <td>string</td>
        <td>The window title.</td>
    </tr>
    <tr>
        <td>Root</td>
        <td>js.Value</td>
        <td>The element the window will append to. Defaults to <code>document.body</code>.</td>
    </tr>
    <tr>
        <td>Template</td>
        <td>js.Value</td>
        <td>A custom window layout template element (see <a href="#template">Custom Template</a>).</td>
    </tr>
    <tr>
        <td>Mount</td>
        <td>js.Value</td>
        <td>Mount an element (widget, template, etc.) to the window body.</td>
    </tr>
    <tr>
        <td>HTML</td>
        <td>string</td>
        <td>Set the <code>innerHTML</code> of the window body.</td>
    </tr>
    <tr>
        <td>URL</td>
        <td>string</td>
        <td>Open URL inside the window (loaded via iframe).</td>
    </tr>
    <tr>
        <td>Width<br>Height</td>
        <td>winbox.Unit</td>
        <td>Set the initial width/height of the window (<code>Px</code> and <code>Pct</code>).</td>
    </tr>
    <tr>
        <td>MinWidth<br>MinHeight</td>
        <td>winbox.Unit</td>
        <td>Set the minimal width/height of the window. Should be at least the height of the window header title bar.</td>
    </tr>
    <tr>
        <td>MaxWidth<br>MaxHeight</td>
        <td>winbox.Unit</td>
        <td>Set the maximum width/height of the window.</td>
    </tr>
    <tr>
        <td>Autosize</td>
        <td>bool</td>
        <td>Automatically size the window to fit the window contents.</td>
    </tr>
    <tr>
        <td>Overflow</td>
        <td>bool</td>
        <td>Allow the window to move outside the viewport.</td>
    </tr>
    <tr>
        <td>X<br>Y</td>
        <td>winbox.Unit</td>
        <td>Set the initial position of the window (supports <code>winbox.Right</code> for x-axis, <code>winbox.Bottom</code> for y-axis, <code>winbox.Center</code> for both, <code>Px</code> and <code>Pct</code> for both).</td>
    </tr>
    <tr>
        <td>Max</td>
        <td>bool</td>
        <td>Automatically toggles the window into maximized state when created.</td>
    </tr>
    <tr>
        <td>Min</td>
        <td>bool</td>
        <td>Automatically toggles the window into minimized state when created.</td>
    </tr>
    <tr>
        <td>Hidden</td>
        <td>bool</td>
        <td>Automatically toggles the window into hidden state when created.</td>
    </tr>
    <tr>
        <td>Modal</td>
        <td>bool</td>
        <td>Shows the window as modal.</td>
    </tr>
    <tr>
        <td>Dock</td>
        <td>Edge</td>
        <td>Pins the window to an edge of the viewport: <code>EdgeTop</code>, <code>EdgeRight</code>, <code>EdgeBottom</code>, <code>EdgeLeft</code>. The zero value <code>EdgeNone</code> leaves it floating. Not in WinBox.js — see <a href="#differences">Docking</a>.</td>
    </tr>
    <tr>
        <td>DockSize</td>
        <td>Unit</td>
        <td>Thickness of the dock across its edge. Defaults to the window's extent along that axis.</td>
    </tr>
    <tr>
        <td>DockMode</td>
        <td>DockMode</td>
        <td><code>DockReserve</code> (default) takes the dock's strip out of the viewport other windows see, so maximize fills what is left. <code>DockOverlay</code> leaves the viewport alone.</td>
    </tr>
    <tr>
        <td>Top<br>Right<br>Bottom<br>Left</td>
        <td>winbox.Unit</td>
        <td>Set or limit the viewport of the window's available area. Also used for custom splitscreen configurations.</td>
    </tr>
    <tr>
        <td>Background</td>
        <td>string</td>
        <td>Set the background of the window (supports all CSS styles which are also supported by the style-attribute "background", e.g. colors, transparent colors, hsl, gradients, background images).</td>
    </tr>
    <tr>
        <td>Border</td>
        <td>float64</td>
        <td>Set the border width of the window in pixels.</td>
    </tr>
    <tr>
        <td>Header</td>
        <td>float64</td>
        <td>Set the height of the window title bar in pixels (default 35).</td>
    </tr>
    <tr>
        <td>Icon</td>
        <td>string</td>
        <td>Make the titlebar icon visible and set the image source to this url.</td>
    </tr>
    <tr>
        <td>Class</td>
        <td>[]string</td>
        <td>Add one or more classnames to the window. Used to define custom styles in css, query elements by context or just to tag the corresponding window instance. WinBox provides some useful <a href="#control-classes">Built-in Control Classes</a> to easily setup a custom configuration.</td>
    </tr>
    <tr>
        <td>OnCreate</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered after the window was created.</td>
    </tr>
    <tr>
        <td>OnMove</td>
        <td>func(w *WinBox, x, y float64)</td>
        <td>Callback triggered when the window moves.</td>
    </tr>
    <tr>
        <td>OnResize</td>
        <td>func(w *WinBox, width, height float64)</td>
        <td>Callback triggered when the window resizes.</td>
    </tr>
    <tr>
        <td>OnFullscreen<br>OnMinimize<br>OnMaximize</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when the window enters fullscreen / minimized / maximized mode.</td>
    </tr>
    <tr>
        <td>OnRestore</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when the window returns to a windowed state from a fullscreen, minimized or maximized state.</td>
    </tr>
    <tr>
        <td>OnDock</td>
        <td>func(w *WinBox, edge Edge)</td>
        <td>Callback triggered when the window is docked to an edge. Not in WinBox.js.</td>
    </tr>
    <tr>
        <td>OnUndock</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when the window leaves its edge and returns to floating. Not in WinBox.js.</td>
    </tr>
    <tr>
        <td>OnHide<br>OnShow</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when the window is hidden / shown.</td>
    </tr>
    <tr>
        <td>OnClose</td>
        <td>func(w *WinBox, force bool) bool</td>
        <td>Triggered right before closing; return <code>true</code> to stop the window from closing.</td>
    </tr>
    <tr>
        <td>OnFocus<br>OnBlur</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when a window gains / loses the focused state.</td>
    </tr>
    <tr>
        <td>OnLoad</td>
        <td>func(w *WinBox)</td>
        <td>Callback triggered when an iframe created via <code>URL</code>/<code>SetURL</code> finished loading.</td>
    </tr>
</table>

## Create and Setup Window

#### Basic Window

> When no `Root` was specified the window will append to the `document.body`.

```go
winbox.New(&winbox.Options{Title: "Window Title"})
```

Alternatively:
```go
w := winbox.New(nil)
w.SetTitle("Window Title")
```

#### Custom Root

```go
winbox.New(&winbox.Options{
    Title: "Window Title",
    Root:  js.Global().Get("document").Get("body"),
})
```

#### Custom Color

> Supports all CSS styles which are also supported by the style-attribute "background", e.g. colors, rgba, hsl, gradients, background images.

```go
winbox.New(&winbox.Options{
    Title:      "Custom Color",
    Background: "#ff005d",
})
```

Alternatively:
```go
w := winbox.New(&winbox.Options{Title: "Custom Color"})
w.SetBackground("#ff005d")
```

#### Custom Border

```go
winbox.New(&winbox.Options{
    Title:  "Custom Border",
    Border: 4,
})
```

#### Custom Titlebar Icon

> Supports all datatypes which are also supported by the style-attribute "background-image", e.g. URL or base64 encoded data. The default icon size is 20 x 20 pixels.

```go
winbox.New(&winbox.Options{
    Title: "Custom Icon",
    Icon:  "img/icon.svg",
})
```

Alternatively:
```go
w := winbox.New(&winbox.Options{Title: "Custom Icon"})
w.SetIcon("img/icon.svg")
```

#### Custom Viewport

> Define the available area (relative to the document) in which the window can move or could be resized.

```go
winbox.New(&winbox.Options{
    Title:  "Custom Viewport",
    Top:    winbox.Px(50),
    Right:  winbox.Pct(5),
    Bottom: winbox.Px(50),
    Left:   winbox.Pct(5),
})
```

Alternatively (pixel values only when directly assigned):
```go
w := winbox.New(&winbox.Options{Title: "Custom Viewport"})
w.Top = 50
w.Right = 200
w.Bottom = 0
w.Left = 200
```

#### Custom Position / Size

```go
winbox.New(&winbox.Options{
    Title:  "Centered",
    X:      winbox.Center,
    Y:      winbox.Center,
    Width:  winbox.Pct(50),
    Height: winbox.Pct(50),
})
```

```go
winbox.New(&winbox.Options{
    Title:  "Bottom Right",
    X:      winbox.Right,
    Y:      winbox.Bottom,
    Width:  winbox.Px(200),
    Height: winbox.Px(200),
})
```

Alternatively (also supports the same units and keywords as above):
```go
w := winbox.New(&winbox.Options{Title: "Custom Position"})
w.Resize(winbox.Pct(50), winbox.Pct(50)).
    Move(winbox.Center, winbox.Center)
```

Alternatively (pixel values when directly assigned; re-apply with zero-value Units):
```go
w := winbox.New(&winbox.Options{Title: "Custom Position"})

w.Width = 200
w.Height = 200
w.Resize(winbox.Unit{}, winbox.Unit{})

w.X = 100
w.Y = 100
w.Move(winbox.Unit{}, winbox.Unit{})
```

> In some cases you need to execute `Resize()` before `Move()` to properly apply relative positions which take the window's size into account.

#### Overflow Window

Allow the window to move outside the viewport borders on left, right and bottom (default is false).

```go
winbox.New(&winbox.Options{
    Title:    "Overflow Window",
    Overflow: true,
})
```

#### Modal Window

```go
winbox.New(&winbox.Options{
    Title: "Modal Window",
    Modal: true,
})
```

#### Window Stack

The window stack gets you an ordered list of every created window which wasn't already closed. The last focused window is placed as last entry in the slice.

```go
a := winbox.New(&winbox.Options{Title: "A"}) // window A gets focused
b := winbox.New(&winbox.Options{Title: "B"}) // window B gets focused
c := winbox.New(&winbox.Options{Title: "C"}) // window C gets focused
a.Focus()                                    // window A gets focused

// get window stack ordered by focus history
stack := winbox.Stack() // [B, C, A]
```

<a name="themes"></a>
#### Themes

The upstream themes work unchanged. Load the corresponding css file from the [WinBox.js themes folder](https://github.com/nextapps-de/winbox/tree/master/dist/css/themes):

```html
<head>
    <link rel="stylesheet" href="themes/modern.min.css">
</head>
```

Just add the name of the theme via `Class`:

```go
winbox.New(&winbox.Options{
    Title: "Theme: Modern",
    Class: []string{"modern"},
})
```

Alternatively:
```go
w := winbox.New(&winbox.Options{Title: "Theme: Modern"})
w.AddClass("modern")
```

You can change themes during the lifetime of the window.

## Manage Window Content

#### Set innerHTML

> Do not forget to sanitize any user inputs which are part of the __HTML__ as this can lead to unintended XSS!

```go
winbox.New(&winbox.Options{
    Title: "Set innerHTML",
    HTML:  "<h1>Lorem Ipsum</h1>",
})
```

Alternatively:
```go
w := winbox.New(&winbox.Options{Title: "Set innerHTML"})
w.Body.Set("innerHTML", "<h1>Lorem Ipsum</h1>")
```

#### Mount DOM (Cloned)

> When cloning you can easily create multiple window instances of the same content in parallel.

```html
<div id="content">
    <h1>Lorem Ipsum</h1>
    <p>Lorem ipsum [...]</p>
</div>
```

```go
node := js.Global().Get("document").Call("getElementById", "content")

winbox.New(&winbox.Options{
    Title: "Mount DOM",
    Mount: node.Call("cloneNode", true),
})
```

#### Mount DOM (Singleton) + Auto-Unmount

> A singleton is a unique fragment which can move inside the document. When creating multiple windows and mounting the same fragment to them, the fragment will leave the old window. When the window closes, the fragment automatically moves back to its origin (auto-unmount).

```html
<div id="backstore" style="display: none">
    <div id="content">
        <h1>Lorem Ipsum</h1>
        <p>Lorem ipsum [...]</p>
    </div>
</div>
```

```go
node := js.Global().Get("document").Call("getElementById", "content")

winbox.New(&winbox.Options{
    Title: "Mount DOM",
    Mount: node,
})
```

#### Explicit Unmount

Move the fragment back to its hidden backstore source:

```go
w.Unmount()
```

Or move the fragment to another destination:

```go
w.Unmount(js.Global().Get("document").Call("getElementById", "backstore-2"))
```

Override the default auto-unmount behavior when closing the window:

```go
winbox.New(&winbox.Options{
    Title: "Mount DOM",
    Mount: node,
    OnClose: func(w *winbox.WinBox, force bool) bool {
        w.Unmount(js.Global().Get("document").Call("getElementById", "backstore-2"))
        return false
    },
})
```

#### Manual Mount Contents

Feel free to use `w.Body` directly:
```go
w.Body.Call("appendChild", node)
```

#### Open URI / URL

> Do not forget to sanitize any user inputs when they are part of the __URL__ as this can lead to unintended XSS!

```go
winbox.New(&winbox.Options{
    Title: "Open URL",
    URL:   "https://wikipedia.com",
    OnLoad: func(w *winbox.WinBox) { /* extern page loaded */ },
})
```

> You can use every URI scheme which is supported by the `src` attribute, e.g. URL, image or video, base64 encoded data.

Alternatively:
```go
w := winbox.New(&winbox.Options{Title: "Open URL"})
w.OnLoad = func(w *winbox.WinBox) { /* extern page loaded */ }
w.SetURL("https://wikipedia.com")
```

## The WinBox Window Instance

Window states / information:
```go
w := winbox.New(nil)

fmt.Println("Window ID:", w.ID)
fmt.Println("Window Index:", w.Index)
fmt.Println("Current Maximize State:", w.Max)
fmt.Println("Current Minimize State:", w.Min)
fmt.Println("Current Fullscreen State:", w.Full)
fmt.Println("Current Hidden State:", w.Hidden)
fmt.Println("Current Focused State:", w.Focused)
```

The window body acts like the `document.body` and has a scroll pane:
```go
w.Body.Set("innerHTML", "<h1>Lorem Ipsum</h1>")
```

Get the DOM element of the window's outer frame:
```go
root := w.DOM
```

You can also get the window element by DOM id:
```go
root := js.Global().Get("document").Call("getElementById", w.ID)
```

You can access and modify the window DOM element directly, or use the built-in methods:

```go
hidden := w.HasClass("hide")
focused := w.HasClass("focus")
w.RemoveClass("modal")
w.AddClass("my-theme")
w.ToggleClass("my-toggle")
```

#### Controls

```go
w := winbox.New(nil)

w.Focus()      // bring up to front
w.Blur()       // release focus
w.Minimize()   // set minimized state
w.Maximize()   // set maximized state
w.Fullscreen() // set fullscreen state
w.Restore()    // restore windowed state
w.Hide()       // hide window
w.Show()       // show hidden window
w.Close(false) // close and destroy the window
```

#### Chaining Methods

```go
w := winbox.New(nil)

w.SetTitle("Title").
    SetBackground("#fff").
    Resize(winbox.Pct(50), winbox.Pct(50)).
    Move(winbox.Center, winbox.Center).
    Mount(js.Global().Get("document").Call("getElementById", "content"))
```

> When using `winbox.Center` as position you need to call `Resize()` before `Move()`.

#### The "OnClose" callback

> The `OnClose` callback is triggered right before closing and __stops__ the close when it returns a __true__.

```go
winbox.New(&winbox.Options{
    OnClose: func(w *winbox.WinBox, force bool) bool {
        // return true to stop the closing
        // return false to allow closing
        return !force && !js.Global().Call("confirm", "Close window?").Bool()
    },
})
```

Close the window and execute the callback (shows the prompt from the example above):
```go
w.Close(false)
```

Force-close the window (skips the prompt from the example above):
```go
w.Close(true)
```

<a name="control-classes" id="control-classes"></a>
#### Use Control Classes

WinBox provides built-in control classes you can pass when creating a window instance.

> All control classes from this list can be added or removed during the lifetime of the window. State classes like `max`, `min`, `full`, `hide` and `focus` must not be changed manually — use the member methods `Maximize()`, `Minimize()`, `Hide()` etc. instead.

<table>
    <tr>
        <th>Classname&nbsp;&nbsp;&nbsp;&nbsp;</th>
        <th align="left">Description</th>
    </tr>
    <tr>
        <td>no-animation</td>
        <td>Disables the window's transition animation</td>
    </tr>
    <tr>
        <td>no-shadow</td>
        <td>Disables the window's drop shadow</td>
    </tr>
    <tr>
        <td>no-header</td>
        <td>Hide the window header incl. title and toolbar</td>
    </tr>
    <tr>
        <td>no-min</td>
        <td>Hide the minimize icon</td>
    </tr>
    <tr>
        <td>no-max</td>
        <td>Hide the maximize icon</td>
    </tr>
    <tr>
        <td>no-full</td>
        <td>Hide the fullscreen icon</td>
    </tr>
    <tr>
        <td>no-close</td>
        <td>Hide the close icon</td>
    </tr>
    <tr>
        <td>no-resize</td>
        <td>Disables the window resizing capability</td>
    </tr>
    <tr>
        <td>no-move</td>
        <td>Disables the window moving capability</td>
    </tr>
</table>

> Without the header the user isn't able to move the window frame. It may be useful for creating fixed popups.

Pass in classnames when creating the window to apply behaviour:
```go
winbox.New(&winbox.Options{
    Class: []string{"no-min", "no-max", "no-full", "no-resize", "no-move"},
})
```

> The example above is a good start to create classical popups.

You can add or remove all control classes from above during the window's lifetime:

```go
w.AddClass("no-resize").AddClass("no-move")
w.RemoveClass("no-resize").RemoveClass("no-move")
w.ToggleClass("no-resize").ToggleClass("no-move")
state := w.HasClass("no-resize") && w.HasClass("no-move")
```

## Custom Splitscreen

Use the viewport limit to define your own splitscreen areas, e.g. for a simple vertical split:

```go
winbox.New(&winbox.Options{Title: "Split Left", Right: winbox.Pct(50)})
winbox.New(&winbox.Options{Title: "Split Right", Left: winbox.Pct(50)})
```

The same way you can also define custom sizes and positions for each split as well as complex grids, e.g.:

```go
winbox.New(&winbox.Options{Title: "Split Top-Left", Right: winbox.Pct(66), Bottom: winbox.Pct(50), Max: true})
winbox.New(&winbox.Options{Title: "Split Bottom-Left", Right: winbox.Pct(66), Top: winbox.Pct(50), Max: true})

winbox.New(&winbox.Options{Title: "Split Middle", Left: winbox.Pct(34), Right: winbox.Pct(34), Max: true})

winbox.New(&winbox.Options{Title: "Split Top-Right", Left: winbox.Pct(66), Bottom: winbox.Pct(50), Max: true})
winbox.New(&winbox.Options{Title: "Split Bottom-Right", Left: winbox.Pct(66), Top: winbox.Pct(50), Max: true})
```

The splitscreen from above will look like this grid:

```
---------------------------------------------
|             |              |              |
|             |              |              |
|  Top Left   |              |  Top Right   |
|             |              |              |
|             |              |              |
---------------    Middle    ----------------
|             |              |              |
|             |              |              |
| Bottom Left |              | Bottom Right |
|             |              |              |
|             |              |              |
---------------------------------------------
```

You can set the values for the viewport dynamically, doing this makes it possible to size the grid dynamically and also change the grid schema.

## Custom Controls

This example will add a custom control button `.wb-like` to the window heading toolbar along some CSS for icon styling:
```css
.wb-like {
    background-size: 20px auto;
}
.wb-like.active {
    background-image: url(heart-filled.svg) !important;
}
```

Attach a control to the window toolbar:
```go
w.AddControl(winbox.Control{
    // the position index
    Index: 1,
    // classname to apply styling
    Class: "wb-like",
    // icon url when not specified via classname
    Image: "heart.svg",
    // click listener; the button element is passed via event.target
    Click: func(event js.Value, w *winbox.WinBox) {
        fmt.Println(w.ID)
        event.Get("target").Get("classList").Call("toggle", "active")
    },
})
```

Remove a control from the window toolbar:
```go
w.RemoveControl("wb-like").RemoveControl("wb-min")
```

<a name="template"></a>
## Custom Template (Layout)

You can fully customize the WinBox window layout by providing a custom `Template` element during creation. This way you can add new elements to the window or re-arrange them.

```go
document := js.Global().Get("document")
template := document.Call("createElement", "div")
template.Set("innerHTML", `
    <div class=wb-header>
        <div class=wb-control>
            <span class=wb-custom></span>
            <span class=wb-close></span>
        </div>
        <div class=wb-drag></div>
    </div>
    <div class=wb-body></div>
`)

winbox.New(&winbox.Options{Title: "Custom Template", Template: template})
```

> The `.wb-drag` element needs to exist for the user to be able to move the window by dragging the heading toolbar.

<a name="customize"></a>
## Customize Window

> Additionally, take a look into the <a href="https://github.com/nextapps-de/winbox/tree/master/src/css/themes">themes folder</a> of the original project to get some ideas how to customize the window.

The window boilerplate:

<img src="https://cdn.jsdelivr.net/gh/nextapps-de/winbox@master/demo/boilerplate.svg?v=4" width="100%" alt="WinBox Boilerplate">

To extend or replace the embedded styles, add your own stylesheet — or provide a full replacement with `<style id="winbox-style">` before creating the first window (the automatic injection then skips itself). All CSS recipes from the original project apply unchanged:

Hide or disable specific icons:
```css
.wb-min   { display: none }
.wb-full  { display: none }
.wb-max   { display: none }
.wb-close { display: none }
```

Modify a specific icon:
```css
.wb-max {
    background-image: url(max.png);
    background-position: center;
    background-size: 15px auto;
}
```

Use black standard icons (useful for bright backgrounds):
```css
.wb-control { filter: invert(1) }
```

Modify or disable resizing areas on the window borders (`.wb-n`, `.wb-e`, `.wb-s`, `.wb-w`, `.wb-nw`, `.wb-ne`, `.wb-sw`, `.wb-se`):
```css
.wb-n { display: none }
```

Style the window frame and body:
```css
.winbox {
    background: linear-gradient(90deg, #ff00f0, #0050ff);
    border-radius: 12px 12px 0 0;
    box-shadow: none;
}
.wb-title { font-size: 12px }
.wb-body {
    /* the width of window frame border: */
    margin: 4px;
    color: #fff;
    background: #131820;
}
```

> The margin of `.wb-body` corresponds to the width of the window border.

Apply styles depending on window state (`min`, `max`, `focus`, `modal` classes on the `.winbox` root; `.wb-body:fullscreen` for fullscreen):
```css
.winbox.min      { border-radius: 0 }
.winbox.max      { border-radius: 0 }
.winbox:not(.focus) .wb-control { display: none }
.winbox.modal .wb-close { display: none }
.winbox.modal:after {
    background: #0d1117;
    opacity: 0.5;
    animation: none;
}
```

## Useful Hints

Often you need to hide specific content parts when they are mounted to a window, or when they are NOT inside a window. Add the two classes `wb-hide` and `wb-show` to any element to control visibility between the two states "inside" and "outside" a window:

```html
<body>
    <main id="content">
        <header class="wb-hide">Hide this header when in windowed mode</header>
        <section>
            <!-- page contents -->
        </section>
        <footer class="wb-show">Hide this footer when NOT in windowed mode</footer>
    </main>
</body>
```

```go
winbox.New(&winbox.Options{
    Mount: js.Global().Get("document").Call("getElementById", "content"),
})
```

#### Best Practices

- Use a non-scrolling body element to get the best user experience on mobile devices.
- Or provide an alternative view strategy for mobile devices, e.g. when the device is a touch device then open a classical app view. If a mouse pointer is available, then mount this view to the WinBox window. Also, you can place a switch button in your application where the user can toggle between these two modes.
- Keep the Go runtime alive (e.g. `select {}` at the end of `main`) — the window manager is driven entirely by Go callbacks.

<a name="jsapi"></a>
## Using it from JavaScript

Existing pages already say `new WinBox({...})`. The `jsapi` subpackage puts
that constructor back on `globalThis`, backed by this implementation, so such a
page keeps working without being rewritten:

```go
//go:build js && wasm

package main

import "github.com/0magnet/winbox-go/jsapi"

func main() {
    jsapi.InstallGlobal()
    select {} // keep the Go runtime alive for callbacks
}
```

`cmd/winbox-js` is exactly that program, if a module whose only job is to
install the constructor is what you want:

```cmd
tinygo build -o winbox.wasm -target wasm -no-debug -opt=z ./cmd/winbox-js
```

The option keys, methods, instance properties and callbacks are the ones
WinBox.js documents, with the loose argument forms it accepts: `width: 250`,
`"250"`, `"250px"` and `"40%"` all work, `class` takes a string or an array,
`minimize(false)` restores, `close()` returns `true` only when an `onclose`
handler cancelled it, and callbacks are invoked with `this` bound to the
instance and may be reassigned at any time. The root element is exposed as
`g`, `window` **and** `dom`, so code that identifies a window by matching a DOM
node against any one of those aliases finds it.

The Go API (`winbox.New`) is the better one to write new code against. This is
a compatibility layer.

### It exists only once the module has started

This is the one thing a `<script src="winbox.js">` gave you that a wasm module
cannot: the constructor is defined when Go's `main` runs, not when the tag is
parsed. Page code that constructs a window before then throws
`WinBox is not defined`.

`cmd/winbox-js` resolves `globalThis.__winboxReady` when the constructor is in
place, so a caller can wait for it instead of racing:

```html
<script src="wasm_exec.js"></script>
<script>
  const go = new Go();
  WebAssembly.instantiateStreaming(fetch("winbox.wasm"), go.importObject)
      .then(r => go.run(r.instance));
</script>
<script>
  // Gate whatever opens windows on this, rather than opening one at load.
  (globalThis.__winboxReady || Promise.resolve()).then(() => {
      new WinBox({ title: "Ready" });
  });
</script>
```

A host that already polls for readiness before mounting its UI — many do — has
the cheaper option of adding `typeof WinBox === "function"` to the condition it
already waits on, which gates every window it will ever open in one place.

<a name="differences"></a>
## Differences from WinBox.js

- Positions and dimensions use the `winbox.Unit` type (`Px`, `Pct`, `Center`, `Right`, `Bottom`) instead of `number | string`
- Callbacks are Go funcs receiving the `*WinBox` as first argument (JS used `this`); they can be reassigned on the instance at any time
- `Options.Border` and `Options.Header` are pixel values (`float64`), not arbitrary CSS lengths
- `Options.Index: 0` means "auto" (JS distinguished `0` from undefined)
- The DOM element's `winbox` property holds the window `ID` string rather than the instance; keep the `*WinBox` returned by `New` (or use `winbox.Stack()`) to control windows
- The iframe `onload` callback is the window's `OnLoad` field instead of a `setUrl` parameter
- The stylesheet (with icons inlined) is embedded and injected automatically; there are no separate js/css assets to load

### Docking (an addition, not a port)

Everything above is a difference in *how* WinBox.js is expressed in Go. Docking
is the one thing here that WinBox.js does not have at all.

It is opt-in, and the zero value of every type involved means "not docked", so
a program that never mentions docking behaves exactly as it did before docking
existed — maximize still fills the raw viewport, and minimized windows still
stack along the bottom of it.

```go
panel := winbox.New(&winbox.Options{
    Title:    "Controls",
    Dock:     winbox.EdgeLeft,   // EdgeTop, EdgeRight, EdgeBottom, EdgeLeft
    DockSize: winbox.Px(260),    // thickness across that edge
})

panel.Dock(winbox.EdgeBottom, winbox.Px(180)) // move it to another edge
panel.Undock()                                // back to its pre-dock geometry
```

A docked window pins to its edge and stretches along it, keeping a fixed
thickness. Drag the one handle facing the content to resize it; drag its
titlebar to pull it off the edge, the way dragging a maximized window restores
it. The other seven resize handles are inert while docked, in CSS as well as in
code, so no cursor promises something that will not happen.

By default a dock **reserves** its strip: `Maximize` fills what is left rather
than covering the dock, and minimized windows stack inside that area instead of
underneath it. `DockMode: winbox.DockOverlay` opts out, leaving the viewport
alone so the dock sits over what is behind it.

Dock several windows and each claims space inside what the earlier ones left, so
a left dock and a bottom dock meet at a corner rather than overlapping it.
Hiding, minimizing or closing a dock returns its strip to everyone else, and
docks follow the viewport when the page is resized. (A *maximized* window does
not follow a resize — that is WinBox.js's behaviour and it is kept — except
where docks are involved, since a stale maximized window and a dock would
otherwise overlap.)

Per-window `Top`/`Right`/`Bottom`/`Left` offsets are ignored while docked: they
exist to inset a floating window from the viewport, and a dock's whole job is to
be against the edge.

`OnDock`/`OnUndock` callbacks and the `Docked()`, `DockThickness()` and
`SetDockThickness()` accessors round it out. See `dock.go`.

---

winbox-go is a derivative work of <a href="https://github.com/nextapps-de/winbox">WinBox.js</a>, Copyright 2021-2023 Thomas Wilkerling, hosted by Nextapps GmbH.<br>
Released under the <a href="http://www.apache.org/licenses/LICENSE-2.0.html" target="_blank">Apache 2.0 License</a><br>
## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/winbox-go/... | dot -Tsvg -o docs/winbox-go-goda-graph.svg
```

![Dependency Graph](docs/winbox-go-goda-graph.svg "github.com/0magnet/winbox-go Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               7            268            248           1572
JavaScript                       2            117             82            935
Markdown                         1            189              1            875
CSS                              1              0             39            294
HTML                             2              4              0            137
YAML                             1              0              9             69
JSON                             2              0              0             28
Bourne Shell                     1              2              3             12
-------------------------------------------------------------------------------
TOTAL                           17            580            382           3922
-------------------------------------------------------------------------------
```
