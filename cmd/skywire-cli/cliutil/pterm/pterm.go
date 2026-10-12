// Package pterm cmd/skywire-cli/cliutil/pterm/pterm.go c5-cli-util
//
// The ANSI colors and box-drawing tree the CLI used from github.com/pterm/pterm,
// with the same output. pterm's keyboard dependency does not build for js.
package pterm

import (
	"fmt"
	"strings"
)

const reset = "\x1b[0m"

// sgr colors each line of the text and restores the color after a nested
// reset, as pterm does, so a colored span inside another keeps the outer one.
func sgr(code string, a ...interface{}) string {
	start := "\x1b[" + code + "m"
	lines := strings.Split(fmt.Sprint(a...), "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = start + strings.ReplaceAll(line, reset, reset+start) + reset
	}
	return strings.Join(lines, "\n")
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
	gray    = func(a ...interface{}) string { return sgr("90", a...) }
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

// Println prints the arguments and a newline.
func Println(a ...interface{}) { fmt.Print(fmt.Sprint(a...) + "\n") }

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

// TreePrinter renders a TreeNode with gray box-drawing branches.
type TreePrinter struct{ root TreeNode }

// DefaultTree is the zero-config tree printer.
var DefaultTree = TreePrinter{}

// WithRoot returns a printer for the given root.
func (t TreePrinter) WithRoot(n TreeNode) *TreePrinter { return &TreePrinter{root: n} }

// Srender returns the rendered tree.
func (t *TreePrinter) Srender() string {
	var b strings.Builder
	if t.root.Text != "" {
		b.WriteString(t.root.Text + "\n")
	}
	renderChildren(&b, t.root.Children, "")
	return b.String()
}

// Render prints the tree and a blank line.
func (t *TreePrinter) Render() error {
	fmt.Print(t.Srender() + "\n")
	return nil
}

func renderChildren(b *strings.Builder, nodes []TreeNode, prefix string) {
	for i, n := range nodes {
		branch, cont := "├", gray("│")+" "
		if i == len(nodes)-1 {
			branch, cont = "└", "  "
		}
		b.WriteString(prefix + gray(branch) + gray("─"))
		if len(n.Children) == 0 {
			b.WriteString(gray("─") + n.Text + "\n")
			continue
		}
		b.WriteString(gray("┬") + n.Text + "\n")
		renderChildren(b, n.Children, prefix+cont)
	}
}
