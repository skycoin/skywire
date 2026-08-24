// Package flags pkg/flags/rain_test.go: unit tests for the help backdrop
// wiring (InitRain) and, more to the point, for every case in which it must
// keep its hands off the output.
package flags

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cliout"
)

// The backdrop is decoration on one screen. Everything that is not a person
// reading a terminal must come back exactly as the help function produced it,
// and that is what Off means.
func TestRainOptionsOff(t *testing.T) {
	newCmd := func() *cobra.Command {
		c := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}
		cliout.RegisterOutputFlags(c)
		return c
	}

	t.Run("plain terminal help is not off", func(t *testing.T) {
		t.Setenv(NoRainEnv, "")
		require.False(t, rainOptions(newCmd()).Off)
	})

	// --json/--jq/--shape make Help print a schema for something else to
	// consume. Rain in the middle of that is not decoration, it is corruption.
	for _, flag := range []string{"json", "shape"} {
		t.Run("machine mode: --"+flag, func(t *testing.T) {
			t.Setenv(NoRainEnv, "")
			c := newCmd()
			require.NoError(t, c.ParseFlags([]string{"--" + flag}))
			require.True(t, rainOptions(c).Off)
		})
	}
	t.Run("machine mode: --jq", func(t *testing.T) {
		t.Setenv(NoRainEnv, "")
		c := newCmd()
		require.NoError(t, c.ParseFlags([]string{"--jq", ".name"}))
		require.True(t, rainOptions(c).Off)
	})

	// `help -d` writes markdown for a file and `help -r` dumps the whole tree.
	t.Run("while printing help text to keep", func(t *testing.T) {
		t.Setenv(NoRainEnv, "")
		c := newCmd()
		withPlainHelp(func() { require.True(t, rainOptions(c).Off) })
		// ...and back on afterwards, or one `help -d` would disable it for
		// the rest of the process.
		require.False(t, rainOptions(c).Off)
	})

	t.Run("env override", func(t *testing.T) {
		t.Setenv(NoRainEnv, "1")
		require.True(t, rainOptions(newCmd()).Off)
	})
}

// Wiring the same root twice would put the rain behind a screen that already
// has rain behind it.
func TestInitRainIsIdempotent(t *testing.T) {
	root := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}

	InitRain(root)
	first := len(rained)
	InitRain(root)

	require.Equal(t, first, len(rained), "a second InitRain on the same root wrapped it again")

	// Not left in the map for the next test to trip over.
	rainedMu.Lock()
	delete(rained, root)
	rainedMu.Unlock()
}

// The docs and recursive modes go through the suppressing path. This is the
// end-to-end version of the withPlainHelp case above: it drives the real help
// command rather than calling rainOptions directly.
func TestDocAndRecursiveModesLeaveHelpAlone(t *testing.T) {
	t.Setenv(NoRainEnv, "")
	for _, mode := range []string{"-d", "-r"} {
		root := newTree()
		InitFlags(root, true)
		InitRain(root)
		root.SetArgs([]string{"help", mode})

		out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
		require.NotContains(t, out, "\x1b[0;38;2;", "mode %s drew a backdrop", mode)

		rainedMu.Lock()
		delete(rained, root)
		rainedMu.Unlock()
	}
}

// The bug this guards: cobra looks the help function up on the parent only
// when a command has none of its own, and skywire-cli's root installs one for
// --json. Wiring just the top command left the whole of `skywire cli` printing
// its help with no backdrop.
func TestInitRainCoversASubtreeThatShadowsHelp(t *testing.T) {
	root := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}
	shadow := &cobra.Command{Use: "shadow", Run: func(*cobra.Command, []string) {}}
	// Exactly what cliout.SetJSONHelp does: its own help function, which
	// cobra will now use for everything beneath it.
	shadow.SetHelpFunc(func(*cobra.Command, []string) {})
	leaf := &cobra.Command{Use: "leaf", Run: func(*cobra.Command, []string) {}}
	shadow.AddCommand(leaf)
	root.AddCommand(shadow)

	InitRain(root)
	defer func() {
		rainedMu.Lock()
		defer rainedMu.Unlock()
		for _, c := range []*cobra.Command{root, shadow, leaf} {
			delete(rained, c)
		}
	}()

	for _, c := range []*cobra.Command{root, shadow, leaf} {
		require.True(t, rained[c], "%s was left without a backdrop", c.Name())
	}
}

// Children before parents. Wrapping a command captures whatever help function
// it has at that moment, and one with none of its own reports its parent's —
// so parent-first would wrap the parent's new wrapper again and paint the rain
// over itself.
func TestEachDeepestFirstVisitsChildrenFirst(t *testing.T) {
	root := &cobra.Command{Use: "root"}
	mid := &cobra.Command{Use: "mid"}
	leaf := &cobra.Command{Use: "leaf"}
	mid.AddCommand(leaf)
	root.AddCommand(mid)

	var order []string
	eachDeepestFirst(root, func(c *cobra.Command) { order = append(order, c.Name()) })

	require.Equal(t, []string{"leaf", "mid", "root"}, order)
}
