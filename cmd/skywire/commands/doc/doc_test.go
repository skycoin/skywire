// Package doc doc_test.go: unit tests for the markdown doc generator —
// the pure page/render/sanitize helpers, the cobra-tree walker (collect /
// visibleChildren / flagUsagesIncludingHidden), the runCapture exec wrapper
// (success / failure / timeout against a stub script), and the RootCmd Run
// callback in both dry-run and write modes.
package doc

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

// saveGlobals snapshots the mutable package-level flag vars + os.Args and
// restores them after the test, so tests that poke the generator's globals
// don't bleed into one another.
func saveGlobals(t *testing.T) {
	t.Helper()
	o, d, ml := outDir, dryRun, maxLines
	cm, ct, csn := captureMode, captureTimeout, captureSkipNote
	repl := buildInfoReplacements
	args := os.Args
	t.Cleanup(func() {
		outDir, dryRun, maxLines = o, d, ml
		captureMode, captureTimeout, captureSkipNote = cm, ct, csn
		buildInfoReplacements = repl
		os.Args = args
	})
}

// runnable returns a cobra command with a no-op Run so it counts as an
// "available command" (collect/visibleChildren skip non-available ones).
func runnable(use, short string) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Run: func(*cobra.Command, []string) {}}
}

// ---- page.path / page.title ------------------------------------------------

func TestPagePath(t *testing.T) {
	require.Equal(t, "README.md", page{}.path())
	require.Equal(t,
		filepath.Join("cli", "dmsg", "cat", "README.md"),
		page{segs: []string{"cli", "dmsg", "cat"}}.path())
}

func TestPageTitle(t *testing.T) {
	require.Equal(t, "skywire", page{}.title())
	require.Equal(t, "skywire cli dmsg", page{segs: []string{"cli", "dmsg"}}.title())
}

// ---- truncateLines ---------------------------------------------------------

func TestTruncateLines(t *testing.T) {
	require.Equal(t, "whatever", truncateLines("whatever", 0)) // n<=0 returns input

	in := "a\nb\nc"
	require.Equal(t, in, truncateLines(in, 5)) // under cap, unchanged

	out := truncateLines("a\nb\nc\nd", 2)
	require.True(t, strings.HasPrefix(out, "a\nb"))
	require.Contains(t, out, "... (2 more lines)")
}

// ---- needsCodeFence --------------------------------------------------------

func TestNeedsCodeFence(t *testing.T) {
	require.False(t, needsCodeFence("plain prose with no special chars"))
	require.True(t, needsCodeFence("box ─ drawing"))      // U+2500
	require.True(t, needsCodeFence("ansi \x1b[1m color")) // ESC
}

// ---- sanitize / initBuildInfoReplacements ----------------------------------

func TestSanitize_StripsANSI(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	require.Equal(t, "hello", sanitize("\x1b[1mhello\x1b[0m"))
}

func TestSanitize_ReplacesXpub(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	xpub := "xpub6" + strings.Repeat("A", 110)
	require.Equal(t, skycoinGenesisAddr, sanitize(xpub))
}

func TestSanitize_AppliesBuildInfoReplacements(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = [][2]string{{"v1.2.3-deadbeef", "<version>"}}
	require.Equal(t, "ver=<version>", sanitize("ver=v1.2.3-deadbeef"))
}

func TestInitBuildInfoReplacements(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	require.NotPanics(t, initBuildInfoReplacements)
	// "unknown"/empty buildinfo values are never added as scrub rules.
	for _, r := range buildInfoReplacements {
		require.NotEqual(t, "", r[0])
		require.NotEqual(t, "unknown", r[0])
	}
}

// ---- flagUsagesIncludingHidden ---------------------------------------------

func TestFlagUsagesIncludingHidden(t *testing.T) {
	require.Equal(t, "", flagUsagesIncludingHidden(nil))

	fs := pflag.NewFlagSet("x", pflag.ContinueOnError)
	fs.String("visible", "", "a visible flag")
	fs.String("secret", "", "a hidden flag")
	require.NoError(t, fs.MarkHidden("secret"))

	out := flagUsagesIncludingHidden(fs)
	require.Contains(t, out, "visible")
	require.Contains(t, out, "secret") // hidden flag IS shown

	// The Hidden bit is restored afterwards.
	require.True(t, fs.Lookup("secret").Hidden)
}

// ---- visibleChildren -------------------------------------------------------

func TestVisibleChildren(t *testing.T) {
	root := runnable("root", "")
	root.AddCommand(runnable("zebra", "z"))
	root.AddCommand(runnable("apple", "a"))
	root.AddCommand(runnable("completion", "skip")) // skipped
	root.AddCommand(runnable("doc", "skip"))        // skipped
	hidden := runnable("hush", "h")
	hidden.Hidden = true
	root.AddCommand(hidden) // not an available command -> skipped

	got := visibleChildren(root)
	require.Len(t, got, 2)
	require.Equal(t, "apple", got[0].Name()) // sorted
	require.Equal(t, "zebra", got[1].Name())
}

// ---- collect ---------------------------------------------------------------

func buildTree() *cobra.Command {
	root := runnable("skywire", "root short")
	child := runnable("cli", "cli short")
	grand := runnable("dmsg", "dmsg short")
	child.AddCommand(grand)
	root.AddCommand(child)
	root.AddCommand(runnable("completion", "")) // skipped by collect
	return root
}

func TestCollect(t *testing.T) {
	saveGlobals(t)
	captureMode = false
	root := buildTree()

	var pages []page
	collect(root, []string{}, &pages)

	// root + cli + cli/dmsg = 3 (completion skipped)
	require.Len(t, pages, 3)
	require.Empty(t, pages[0].segs)
	require.Equal(t, []string{"cli"}, pages[1].segs)
	require.Equal(t, []string{"cli", "dmsg"}, pages[2].segs)
	// The root page lists its visible child.
	require.Len(t, pages[0].children, 1)
	require.Equal(t, "cli", pages[0].children[0].name)
}

// ---- render ----------------------------------------------------------------

func TestRender_Root(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{
		long:    "the long description",
		useLine: "skywire [flags]",
		children: []childRef{
			{name: "cli", short: "cli stuff"},
		},
		example: "skywire cli visor info",
		local:   "  -o, --out string   output dir\n",
		global:  "  --json   json output\n",
	}))
	out := buf.String()
	require.Contains(t, out, "# skywire\n")
	require.NotContains(t, out, "[←") // root has no breadcrumb
	require.Contains(t, out, "the long description")
	require.Contains(t, out, "## Usage")
	require.Contains(t, out, "## Subcommands")
	require.Contains(t, out, "[cli](cli/README.md) — cli stuff")
	require.Contains(t, out, "## Examples")
	require.Contains(t, out, "## Flags")
	require.Contains(t, out, "## Global Flags")
	require.Contains(t, out, "Generated by `skywire doc`")
}

func TestRender_NestedWithBreadcrumbAndShortFallback(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{
		segs:  []string{"cli", "dmsg"},
		short: "short used when long empty",
	}))
	out := buf.String()
	require.Contains(t, out, "# skywire cli dmsg")
	require.Contains(t, out, "[← skywire cli](../README.md)")
	require.Contains(t, out, "short used when long empty")
}

func TestRender_CodeFencedLong(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{long: "banner ─── art"}))
	out := buf.String()
	require.Contains(t, out, "```\nbanner")
}

func TestRender_CaptureSuccess(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{segs: []string{"cli", "tp"}, capture: "TRANSPORT TABLE\nrow1"}))
	out := buf.String()
	require.Contains(t, out, "## Sample output")
	require.Contains(t, out, "TRANSPORT TABLE")
}

func TestRender_CaptureErrorWithNote(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	captureSkipNote = false
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{segs: []string{"cli", "tp"}, captureErr: "visor unreachable"}))
	out := buf.String()
	require.Contains(t, out, "Capture unavailable: visor unreachable")
}

func TestRender_CaptureErrorNoteSuppressed(t *testing.T) {
	saveGlobals(t)
	buildInfoReplacements = nil
	captureSkipNote = true
	var buf bytes.Buffer
	require.NoError(t, render(&buf, page{segs: []string{"cli", "tp"}, captureErr: "visor unreachable"}))
	require.NotContains(t, buf.String(), "Capture unavailable")
}

// ---- runCapture ------------------------------------------------------------

// stubSkywire writes an executable shell script named "skywire" so runCapture
// (which uses os.Args[0] when it ends in "skywire") invokes it instead of a
// real binary. body is the script after the shebang.
func stubSkywire(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "skywire")
	require.NoError(t, os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755)) //nolint:gosec
	return p
}

func TestRunCapture_Success(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 5 * time.Second
	os.Args = []string{stubSkywire(t, "echo line1\necho line2\necho line3\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}, maxLines: 2})
	require.Equal(t, "", errMsg)
	require.Contains(t, out, "line1")
	require.Contains(t, out, "... (1 more lines)")
}

func TestRunCapture_Failure(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 5 * time.Second
	os.Args = []string{stubSkywire(t, "echo problem >&2\nexit 1\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}})
	require.Equal(t, "", out)
	require.Equal(t, "problem", errMsg) // first stderr line is the reason
}

func TestRunCapture_Timeout(t *testing.T) {
	saveGlobals(t)
	captureTimeout = 50 * time.Millisecond
	os.Args = []string{stubSkywire(t, "sleep 5\n")}

	out, errMsg := runCapture(captureSpec{argv: []string{"x"}})
	require.Equal(t, "", out)
	require.Contains(t, errMsg, "timed out")
}

// ---- RootCmd.Run -----------------------------------------------------------

// mountRoot builds a small umbrella tree with RootCmd (the `doc` command)
// and a couple of documentable subcommands mounted under it, returning the
// umbrella root.
func mountRoot() *cobra.Command {
	root := runnable("skywire", "umbrella")
	root.AddCommand(RootCmd)
	cli := runnable("cli", "cli short")
	cli.AddCommand(runnable("visor", "visor short"))
	root.AddCommand(cli)
	return root
}

func TestRootCmd_DryRun(t *testing.T) {
	saveGlobals(t)
	dryRun = true
	captureMode = false
	_ = mountRoot()
	// Run uses cmd.Root(); invoking via RootCmd resolves to the umbrella.
	require.NotPanics(t, func() { RootCmd.Run(RootCmd, nil) })
}

func TestRootCmd_Writes(t *testing.T) {
	saveGlobals(t)
	dryRun = false
	captureMode = false
	dir := t.TempDir()
	outDir = dir
	_ = mountRoot()

	require.NotPanics(t, func() { RootCmd.Run(RootCmd, nil) })

	// Root page plus the cli + cli/visor pages should exist on disk.
	require.FileExists(t, filepath.Join(dir, "README.md"))
	require.FileExists(t, filepath.Join(dir, "cli", "README.md"))
	require.FileExists(t, filepath.Join(dir, "cli", "visor", "README.md"))

	body, err := os.ReadFile(filepath.Join(dir, "README.md"))
	require.NoError(t, err)
	require.Contains(t, string(body), "# skywire")
}
