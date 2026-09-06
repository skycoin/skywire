# bitree

Renders a **bilateral** (two-sided) tree as monospace plain text using
box-drawing glyphs.

Most tree renderers grow in one direction. A bilateral tree has a single root
whose children extend *rightward* — the usual topology — while any node may
additionally carry a mirror subtree extending *leftward*, hanging at the same
rows as the right-side branches. The renderer auto-justifies with whitespace so
the central spine aligns in one column: left content is right-justified up to
the spine, right content flows out from it. Optional trailing columns are
aligned in a block regardless of tree depth.

```
                     this visor
                     │
R[0] ● 1.6K↑ 1.5K↓ ──┼── EXITPK      [stcpr]  9a3a17e0…  143ms
R[1] ● 0.5K↑ 0.4K↓ ──┴── HOP1PK      [sudph]  72512b4b…  47ms
                         └── EXITPK  [stcpr]  1fafe896…  88ms
```

## Install

```
go get github.com/0magnet/bitree
```

## Use

```go
root := &bitree.Node{Label: "this visor", Right: []*bitree.Node{
    {Label: "EXITPK", Cols: []string{"[stcpr]", "9a3a17e0…", "143ms"},
        Left: []*bitree.Node{{Label: "R[0] ● 1.6K↑ 1.5K↓"}}},
    {Label: "HOP1PK", Cols: []string{"[sudph]", "72512b4b…", "47ms"},
        Left:  []*bitree.Node{{Label: "R[1] ● 0.5K↑ 0.4K↓"}},
        Right: []*bitree.Node{{Label: "EXITPK", Cols: []string{"[stcpr]", "1fafe896…", "88ms"}}}},
}}
fmt.Println(bitree.Render(root, bitree.Options{}))
```

That program prints the block above.

- `Node.Right` is the topology; each right child hangs below its parent and
  nests arbitrarily.
- `Node.Left` is a full nestable subtree too, mirroring the right connector
  logic, and hangs at the same rows as the right branches.
- `Node.Cols` holds trailing columns for that node's row, padded into an aligned
  block across every right-side line.

## Options

`Render(root *Node, opts Options) string`. Zero-value `Options` gives the
defaults shown above.

| Field | Effect |
|---|---|
| `Glyphs` | Box-drawing set; `DefaultGlyphs()` returns the Unicode one, swap for ASCII |
| `LeftGutter` | Extra padding left of the leftmost content |
| `ColSep` | Separator between trailing columns |
| `AlignColumns` | Column block alignment |
| `StyleCell` | `func(text string, kind CellKind) string`, called per cell after layout |

`StyleCell` is the reason layout and styling are separable: geometry is computed
from the plain text first, so returning ANSI-wrapped or HTML-wrapped text does
not shift alignment. `CellKind` distinguishes the root, spine, labels and
columns.

## Notes

Standard library only. Widths are measured in runes, so non-ASCII labels align
correctly; combining marks and double-width characters are not specially
handled.

Extracted from [skywire](https://github.com/skycoin/skywire), where it draws
`skywire cli tp tree`.
