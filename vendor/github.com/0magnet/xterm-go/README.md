# xterm-go

A Go port of [xterm.js](https://github.com/xtermjs/xterm.js) 6.0.0 — a terminal emulator library, compiled to WebAssembly for the browser. The VT core is pure Go and also works headless on any platform.

**[Live demo](https://0magnet.github.io/xterm-go/)** — a wasm terminal with an in-browser echo shell.

![xterm-go in the browser](docs/xterm-go-demo.png "the ported terminal drawing a truecolor box through the WebGL renderer")

xterm.js is the front-end terminal component used by VS Code, Hyper and Theia — and by [skywire](https://github.com/skycoin/skywire)'s web pty. This port reimplements it as a Go library so Go/wasm applications can embed a full terminal without JavaScript dependencies.

## Features

- **Terminal apps just work**: the full escape-sequence machinery of xterm.js is ported — cursor addressing, scroll regions, the alternate screen buffer, insert/delete, SGR text styling (16/256/truecolor, underline styles), charsets, device reports (DA/DSR/DECRQM/DECRQSS) and mouse tracking (X10/VT200/DRAG/ANY with SGR encoding) for curses apps.
- **Headless-capable core**: the `vt` subpackage (parser, buffers, input handler) is pure Go with zero dependencies — it builds natively, so terminal semantics are tested with plain `go test`, no browser needed. Use it standalone to interpret pty output server-side.
- **Rich Unicode support**: CJK wide characters, combining characters, wcwidth tables ported from the UnicodeV6 provider.
- **Scrollback**: ring-buffer scrollback with reflow on resize, native scrollbar viewport in the browser layer.
- **Self-contained**: no JS dependencies; styles are injected automatically.
- **GPU-accelerated**: an optional WebGL2 renderer (the `addon-webgl` equivalent) draws the grid as instanced quads sampling a glyph texture atlas, with pixel-perfect procedural box drawing, block, shade and powerline glyphs. Enable with `term.EnableWebGL()`; it falls back to the DOM renderer when WebGL2 is unavailable.
- **IME support**: composition events (CJK input methods, dead keys) are handled with an in-place composition view like xterm.js.
- **Small**: ~850 KB wasm with TinyGo (≈4 MB with standard Go).

## What xterm-go is not

- xterm-go is not a terminal application you can download and use — it is a library for embedding terminals into (wasm) applications.
- xterm-go is not `bash`. Connect it to a real process over a WebSocket (see `Attach`) or feed it output yourself.

## Getting Started

```bash
go get github.com/0magnet/xterm-go
```

Create a `<div id="terminal"></div>` in your page, then in your wasm program:

```go
//go:build js && wasm

package main

import (
	"syscall/js"

	xterm "github.com/0magnet/xterm-go"
	"github.com/0magnet/xterm-go/vt"
)

func main() {
	term := xterm.New(nil) // nil = default options
	term.Open(js.Global().Get("document").Call("getElementById", "terminal"))
	term.Fit() // size to the parent element

	term.WriteString("Hello from \x1b[1;3;31mxterm-go\x1b[0m $ ")
	term.Core.OnData = func(data string) {
		term.WriteString(data) // echo
	}

	select {}
}
```

Build with standard Go or TinyGo:

```bash
GOOS=js GOARCH=wasm go build -o main.wasm .
# or ~6x smaller:
tinygo build -target wasm -no-debug -o main.wasm .
```

### Options

`xterm.New` takes `*vt.Options` (pass `nil` for defaults): dimensions, scrollback length, fonts, cursor style/blink, a `Theme` with the standard 16 colors, and more — mirroring the xterm.js options relevant to the port.

```go
opts := vt.NewOptions()
opts.Scrollback = 5000
opts.FontSize = 14
opts.Theme.Background = "#1e1e2e"
term := xterm.New(opts)
```

### Connecting to a pty (attach)

The equivalent of `@xterm/addon-attach` is built in — hand it a WebSocket carrying pty data:

```go
ws := js.Global().Get("WebSocket").New("wss://example.com/pty")
term.Attach(ws, true) // bidirectional: keystrokes go back over the socket
```

### Fitting (fit addon)

`term.Fit()` resizes the terminal to fill its parent element; call it from a window `resize` listener.

### Headless use

The `vt` package runs anywhere Go runs:

```go
term := vt.NewTerminal(nil)
term.WriteString("ls\r\n\x1b[1;34mdocs\x1b[0m  main.go\r\n")
line := term.Buffer().Lines.Get(1).TranslateToString(true, 0, -1)
// "docs  main.go"
```

## Real-world uses

- Building the web terminal for [skywire](https://github.com/skycoin/skywire)'s wasm hypervisor UI, replacing the bundled xterm.js + fit/attach/webgl addons.

### WebGL renderer

```go
if err := term.EnableWebGL(); err != nil {
	// WebGL2 unavailable — the DOM renderer stays active
}
// term.DisableWebGL() switches back
```

## Differences from xterm.js

- Parser handlers are synchronous (Go has no reason for the async parse-stack machinery).
- The WebGL renderer uses a single 2048² atlas page (cleared and lazily rebuilt on overflow) instead of the multi-page grow/merge machinery, and does not port the selection/decoration/ligature-joiner model overrides or the minimum-contrast-ratio option.
- The deprecated canvas renderer is not ported (DOM and WebGL are).
- Accessibility tree, link decorations and the selection service are not (yet) ported; text selection uses the browser's native selection (DOM renderer).
- Windows conpty wrapping heuristics are not ported.

## Contributing

Issues and PRs welcome at [github.com/0magnet/xterm-go](https://github.com/0magnet/xterm-go).

## License

Copyright (c) 2017-2022, The xterm.js authors (MIT License)<br>
Copyright (c) 2014-2016, SourceLair Private Company (MIT License)<br>
Copyright (c) 2012-2013, Christopher Jeffrey (MIT License)<br>
Go port copyright (c) 2026 (MIT License)
## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
# GOOS=js: the import edges of a wasm program live in js/wasm-tagged
# files and are invisible to a host-context run
GOOS=js GOARCH=wasm go run github.com/loov/goda@latest graph github.com/0magnet/xterm-go/... | dot -Tsvg -o docs/xterm-go-goda-graph.svg
```

![Dependency Graph](docs/xterm-go-goda-graph.svg "github.com/0magnet/xterm-go Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                              36           1201           1535          11108
JavaScript                       1             56             46            457
Markdown                         1             40              0             93
YAML                             1              0              9             69
HTML                             1              0              0             30
JSON                             2              0              0             28
-------------------------------------------------------------------------------
TOTAL                           42           1297           1590          11785
-------------------------------------------------------------------------------
```
