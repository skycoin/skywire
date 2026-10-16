# progkit

A small widget kit for terminal programs on [tcell v3](https://github.com/gdamore/tcell)
that are progressive terminal programs, as
[websh's PROTOCOL.md](https://github.com/0magnet/websh/blob/main/PROTOCOL.md)
describes them.

Every widget draws itself in cells, so a program built on it runs in any
terminal. Where the host offers placements and shipped widgets, as websh does,
an input, a button, a list or a text view is also laid over its cells as the
page's own element. The person types into a real text field, clicks a real
button and selects and copies real text, and what they do reaches the program
as it would from the keyboard. Keys typed in an element reach the program too:
Esc and Tab from a text field, and every plain key from the others.

```go
app, err := progkit.Open() // the terminal, asked first what its host offers
if err != nil {
	log.Fatal(err)
}
defer app.Close()

in := &progkit.Input{ID: "q", Placeholder: "search"}
app.Run(func(f *progkit.Frame) {
	in.Draw(f, progkit.Rect{X: 0, Y: 0, W: f.W, H: 1}, true)
}, func(ev tcell.Event) bool {
	if k, ok := ev.(*tcell.EventKey); ok {
		if k.Key() == tcell.KeyEscape {
			return false
		}
		in.Key(k)
	}
	return true
})
```

The widgets are `Input`, `Area` (a multi-line editor, a textarea in websh),
`Button`, `List`, `Text` (a scrolling view that takes ANSI colored output) and
`Spinner`, with `Box` and `DrawLine` for the rest. Each
placed element is the one shipped document, `widget.html`, told by a post what
to be. Only changed placements and changed elements are sent after a frame.

`cmd/progkitdemo` uses all of them. Run it natively, or build it for js/wasm
and run it from websh's filesystem:

```sh
GOOS=js GOARCH=wasm go build -o progkitdemo.wasm ./cmd/progkitdemo
```

`make check` runs the linters and the tests.

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/0magnet/progkit/... | dot -Tsvg -o docs/progkit-goda-graph.svg
```

![Dependency Graph](docs/progkit-goda-graph.svg "github.com/0magnet/progkit Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                              16            148            135           1393
HTML                             1              0              5            115
Makefile                         1             21             52            111
YAML                             1              0              7             98
Markdown                         1             15              0             67
Bourne Shell                     1              8             17             31
-------------------------------------------------------------------------------
TOTAL                           21            192            216           1815
-------------------------------------------------------------------------------
```
