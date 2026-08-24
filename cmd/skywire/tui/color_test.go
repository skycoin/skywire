package tui

import (
	"strings"
	"testing"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/flags"
)

// renderHelp must keep coloredcobra's color even when writing to an off-screen
// buffer (not a TTY). Before the fatih-vs-gookit fix, subcommand help rendered
// white because the TUI forced the wrong color library.
func TestRenderHelpKeepsColor(t *testing.T) {
	color.NoColor = true // simulate a non-tty process (what broke it)
	root := &cobra.Command{Use: "skywire", Long: "root"}
	cli := &cobra.Command{Use: "cli", Long: "cli"}
	cli.AddCommand(&cobra.Command{Use: "visor", Short: "v", Run: func(*cobra.Command, []string) {}})
	root.AddCommand(cli)
	flags.InitStyle(cli)
	flags.InitFlags(root, true)

	for _, c := range []*cobra.Command{root, cli} {
		if n := strings.Count(renderHelp(c), "\x1b["); n == 0 {
			t.Errorf("%s help rendered with no color", c.Name())
		}
	}
}
