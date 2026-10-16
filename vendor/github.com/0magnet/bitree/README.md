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
| `HeaderLeft`, `HeaderLabel`, `HeaderCols` | When any is set, a header row of column labels above the root, aligned with the columns beneath it |

`StyleCell` is the reason layout and styling are separable: geometry is computed
from the plain text first, so returning ANSI-wrapped or HTML-wrapped text does
not shift alignment. `CellKind` distinguishes the root (`CellRoot`), right labels
(`CellLabel`), trailing columns (`CellColumn`), left blocks (`CellLeft`) and the
header row's three fields (`CellHeaderLeft`, `CellHeaderLabel`, `CellHeaderColumn`).

## Notes

Standard library only. Widths are measured in runes, so non-ASCII labels align
correctly; combining marks and double-width characters are not specially
handled.

Extracted from [skywire](https://github.com/skycoin/skywire), where it draws
`skywire cli tp tree`.

## Dependency Graph

Made with [goda](https://github.com/loov/goda):

```
go run github.com/loov/goda@latest graph github.com/0magnet/bitree/... | dot -Tsvg -o docs/bitree-goda-graph.svg
```

![Dependency Graph](docs/bitree-goda-graph.svg "github.com/0magnet/bitree Dependency Graph")

## Lines of Code

Made with [gocloc](https://github.com/hhatto/gocloc) (excludes `vendor/`, `node_modules/`, `.git/`):

```
gocloc --not-match-d='(vendor|node_modules|\.git)' .
```

```
-------------------------------------------------------------------------------
Language                     files          blank        comment           code
-------------------------------------------------------------------------------
Go                               2             40            138            520
Makefile                         1             22             52            111
YAML                             1              0              7             98
Markdown                         1             23              0             85
Bourne Shell                     1              8             17             31
-------------------------------------------------------------------------------
TOTAL                            6             93            214            845
-------------------------------------------------------------------------------
```
