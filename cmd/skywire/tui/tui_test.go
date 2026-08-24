// Package tui cmd/skywire/tui/tui_test.go: the console's pure helpers — the
// quote-aware argument splitter and the tree walk install builds on — exercised
// directly. The bubbletea Update/View loop is left to run against a real
// terminal; there is nothing to assert about it without one.
package tui

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSplitArgs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"plain", "cli visor ls", []string{"cli", "visor", "ls"}},
		{"extra spaces", "  cli   visor  ", []string{"cli", "visor"}},
		{"double quoted", `cli config gen -o "my path/out.json"`,
			[]string{"cli", "config", "gen", "-o", "my path/out.json"}},
		{"single quoted", `echo 'a b c'`, []string{"echo", "a b c"}},
		{"quote joins to word", `cli --name=foo" bar"`, []string{"cli", "--name=foo bar"}},
		{"empty quotes are an arg", `cli ""`, []string{"cli", ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := splitArgs(c.in)
			require.NoError(t, err)
			require.Equal(t, c.want, got)
		})
	}
}

func TestSplitArgsUnterminatedQuote(t *testing.T) {
	for _, in := range []string{`cli "oops`, `cli 'oops`, `"`} {
		_, err := splitArgs(in)
		require.Error(t, err, "unterminated quote in %q was not rejected", in)
	}
}

// eachDeepestFirst (install.go) must visit children before parents: install
// wraps each command's help function, and one with none of its own reports its
// parent's, so parent-first would wrap the parent's new wrapper a second time.
func TestEachDeepestFirstVisitsChildrenFirst(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	cli := &cobra.Command{Use: "cli"}
	config := &cobra.Command{Use: "config"}
	cli.AddCommand(config)
	root.AddCommand(cli)

	var order []string
	eachDeepestFirst(root, func(c *cobra.Command) { order = append(order, c.CommandPath()) })

	pos := map[string]int{}
	for i, p := range order {
		pos[p] = i
	}
	require.Less(t, pos["root cli config"], pos["root cli"], "a child was visited after its parent")
	require.Less(t, pos["root cli"], pos["root"], "a child was visited after the root")
	require.Len(t, order, 3, "not every command was visited")
}
