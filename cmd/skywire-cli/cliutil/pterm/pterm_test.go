// Package pterm cmd/skywire-cli/cliutil/pterm/pterm_test.go
package pterm

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// The layout matches what github.com/pterm/pterm drew.
func TestTreeLayout(t *testing.T) {
	root := TreeNode{Children: []TreeNode{
		{Text: "a", Children: []TreeNode{{Text: "a1"}, {Text: "a2"}}},
		{Text: "b", Children: []TreeNode{{Text: "b1"}}},
	}}
	want := "├─┬a\n│ ├──a1\n│ └──a2\n└─┬b\n  └──b1\n"
	require.Equal(t, want, ansi.ReplaceAllString(DefaultTree.WithRoot(root).Srender(), ""))
}

func TestNestedColorKeepsOuter(t *testing.T) {
	require.Equal(t, "\x1b[30m\x1b[41mpk\x1b[0m\x1b[30m\x1b[0m", Black(BgRed.Sprint("pk")))
	require.Equal(t, "\x1b[31ma\x1b[0m\n\x1b[31mb\x1b[0m", Red("a\nb"))
}
