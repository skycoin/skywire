//go:build js && wasm

// Package pterm cmd/skywire-cli/cliutil/pterm/pterm_js.go c5-cli-util
//
// js/wasm implementation of the shadow: plain ANSI SGR codes and a simple
// box-drawing tree renderer, no pterm import (pterm's keyboard dependency
// does not build for js). Output is functionally equivalent for the color
// and tree surface the CLI uses.
package pterm

import (
	"fmt"
	"strings"
)

func sgr(code string, a ...interface{}) string {
	return "\x1b[" + code + "m" + fmt.Sprint(a...) + "\x1b[0m"
}

// Sprint-style color functions.
var (
	Red     = func(a ...interface{}) string { return sgr("31", a...) }
	Green   = func(a ...interface{}) string { return sgr("32", a...) }
	Blue    = func(a ...interface{}) string { return sgr("34", a...) }
	Cyan    = func(a ...interface{}) string { return sgr("36", a...) }
	Magenta = func(a ...interface{}) string { return sgr("35", a...) }
	Yellow  = func(a ...interface{}) string { return sgr("33", a...) }
	White   = func(a ...interface{}) string { return sgr("37", a...) }
	Black   = func(a ...interface{}) string { return sgr("30", a...) }
)

// Style is a background style; its Sprint wraps arguments in the SGR code.
type Style struct{ code string }

// Sprint renders the arguments under the style.
func (s Style) Sprint(a ...interface{}) string { return sgr(s.code, a...) }

// Background styles.
var (
	BgRed     = Style{"41"}
	BgBlue    = Style{"44"}
	BgMagenta = Style{"45"}
)

// Println mirrors fmt.Println (pterm.Println adds theming; plain is fine).
func Println(a ...interface{}) { fmt.Println(a...) }

// TreeNode is one node of a renderable tree.
type TreeNode struct {
	Children []TreeNode
	Text     string
}

// LeveledListItem is one indentation-leveled line.
type LeveledListItem struct {
	Level int
	Text  string
}

// LeveledList is a list of leveled items.
type LeveledList []LeveledListItem

// TreePrinter renders a TreeNode with box-drawing branches.
type TreePrinter struct{ root TreeNode }

// WithRoot returns a printer for the given root.
func (t TreePrinter) WithRoot(n TreeNode) *TreePrinter { return &TreePrinter{root: n} }

// Render prints the tree.
func (t *TreePrinter) Render() error {
	var b strings.Builder
	b.WriteString(t.root.Text + "\n")
	renderChildren(&b, t.root.Children, "")
	fmt.Print(b.String())
	return nil
}

func renderChildren(b *strings.Builder, nodes []TreeNode, prefix string) {
	for i, n := range nodes {
		branch, cont := "├─", "│ "
		if i == len(nodes)-1 {
			branch, cont = "└─", "  "
		}
		b.WriteString(prefix + branch + n.Text + "\n")
		renderChildren(b, n.Children, prefix+cont)
	}
}

// DefaultTree is the zero-config tree printer.
var DefaultTree = TreePrinter{}
