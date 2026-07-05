// Package flags pkg/flags/flags_test.go: unit tests for the cobra help/
// styling helpers (InitFlags/InitStyle) and the flag-aware help command
// installed by InstallHelp (tree / doc / recursive / default modes).
package flags

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// newTree builds a small command hierarchy:
//
//	root
//	├── child1
//	│   └── grandchild
//	├── child2
//	└── hidden (not available — must be skipped everywhere)
func newTree() *cobra.Command {
	root := &cobra.Command{Use: "root", Short: "root cmd", Run: func(*cobra.Command, []string) {}}
	child1 := &cobra.Command{Use: "child1", Short: "first", Run: func(*cobra.Command, []string) {}}
	grandchild := &cobra.Command{Use: "grandchild", Short: "deep", Run: func(*cobra.Command, []string) {}}
	child2 := &cobra.Command{Use: "child2", Short: "second", Run: func(*cobra.Command, []string) {}}
	hidden := &cobra.Command{Use: "hidden", Hidden: true, Run: func(*cobra.Command, []string) {}}

	child1.AddCommand(grandchild)
	root.AddCommand(child1, child2, hidden)
	return root
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
//
// The pipe is drained in a background goroutine while fn runs. Reading only
// after fn returns (as a single-threaded read would) deadlocks whenever fn
// writes more than the OS pipe buffer holds — the buffer fills, fn's write
// blocks forever, and nothing ever reads. Windows pipe buffers are small, so
// even modest help output triggers this; concurrent draining avoids it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(t, err)
	os.Stdout = w

	outCh := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r) //nolint
		outCh <- buf.String()
	}()

	fn()
	require.NoError(t, w.Close())
	os.Stdout = orig
	return <-outCh
}

func TestInitFlags_NoUsage(t *testing.T) {
	cmd := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}
	require.NotPanics(t, func() { InitFlags(cmd, false) })
	require.NotNil(t, findChild(cmd, "help"))
}

func TestInitFlags_Usage(t *testing.T) {
	cmd := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}
	require.NotPanics(t, func() { InitFlags(cmd, true) })
}

func TestInitStyle(t *testing.T) {
	cmd := &cobra.Command{Use: "root", Run: func(*cobra.Command, []string) {}}
	require.NotPanics(t, func() { InitStyle(cmd) })
}

func TestFindChild(t *testing.T) {
	root := newTree()
	require.NotNil(t, findChild(root, "child1"))
	require.Nil(t, findChild(root, "nope"))
}

func TestInstallHelp_Idempotent(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	first := findChild(root, "help")
	require.NotNil(t, first)

	// A second install must remove the previous help child, not add a
	// duplicate.
	InstallHelp(root)
	var count int
	for _, c := range root.Commands() {
		if c.Name() == "help" {
			count++
		}
	}
	require.Equal(t, 1, count)
}

func TestHelpCommand_DefaultMode(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	root.SetArgs([]string{"help"})
	out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
	require.Contains(t, out, "child1")
}

func TestHelpCommand_WithArg(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	root.SetArgs([]string{"help", "child1"})
	out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
	require.Contains(t, out, "grandchild")
}

func TestHelpCommand_TreeMode(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	root.SetArgs([]string{"help", "-t"})
	out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
	require.Contains(t, out, "child1")
	require.Contains(t, out, "grandchild")
	require.Contains(t, out, "└──")
	require.NotContains(t, out, "hidden") // hidden command excluded
}

func TestHelpCommand_DocMode(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	root.SetArgs([]string{"help", "-d"})
	out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
	require.Contains(t, out, "# root")
	require.Contains(t, out, "## root child1")
	require.Contains(t, out, "```")
}

func TestHelpCommand_RecursiveMode(t *testing.T) {
	root := newTree()
	InstallHelp(root)
	root.SetArgs([]string{"help", "-r"})
	out := captureStdout(t, func() { require.NoError(t, root.Execute()) })
	require.Contains(t, out, "root child1 grandchild")
	require.Equal(t, strings.Count(out, strings.Repeat("=", 72))/2 > 0, true)
}

func TestPrintHelpAll_NonRecursive(t *testing.T) {
	root := newTree()
	out := captureStdout(t, func() { PrintHelpAll(root, false) })
	require.Contains(t, out, "root child1")
	require.NotContains(t, out, "root child1 grandchild") // not recursive
}

func TestPrintCommandTree(t *testing.T) {
	root := newTree()
	out := captureStdout(t, func() { PrintCommandTree(root) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	require.Equal(t, "root", lines[0])
	require.Contains(t, out, "├── ")
	require.Contains(t, out, "└── ")
}

func TestPrintCommandDocs_DepthCap(t *testing.T) {
	// Build a chain deeper than 6 to exercise the H6 cap in printCmdDoc.
	root := &cobra.Command{Use: "l0", Run: func(*cobra.Command, []string) {}}
	cur := root
	for i := 1; i <= 8; i++ {
		next := &cobra.Command{Use: "l" + string(rune('0'+i)), Run: func(*cobra.Command, []string) {}}
		cur.AddCommand(next)
		cur = next
	}
	out := captureStdout(t, func() { PrintCommandDocs(root) })
	require.Contains(t, out, "# l0")
	// Never exceed six '#'.
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "#") {
			hashes := len(line) - len(strings.TrimLeft(line, "#"))
			require.LessOrEqual(t, hashes, 6)
		}
	}
}

// sanity: captureStdout actually round-trips bytes.
func TestCaptureStdout(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("x")
	out := captureStdout(t, func() { os.Stdout.WriteString("hello") }) //nolint
	require.Equal(t, "hello", out)
}
