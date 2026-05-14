// Package doc cmd/skywire/commands/doc/doc.go: hidden `skywire doc`
// subcommand that walks the cobra tree rooted at the umbrella `skywire`
// binary and emits one markdown page per command under docs/skywire/
// mirroring the cobra command hierarchy.
//
// Why this exists: the cmd/{skywire,skywire-cli,skycoin-skywire,svc,
// dmsg}/README.md files used to be hand-maintained. They drifted, they
// leaked the build-host's reward address, and they triplicated each
// other (skywire and skycoin-skywire each re-embedded the full cli
// subtree). This generator replaces all of that with a single source-
// of-truth: the live cobra tree.
//
// Output shape:
//
//	docs/skywire/README.md                  — `skywire` help
//	docs/skywire/cli/README.md              — `skywire cli` help + links to children
//	docs/skywire/cli/dmsg/README.md         — `skywire cli dmsg` help + links
//	docs/skywire/cli/dmsg/cat/README.md     — `skywire cli dmsg cat` help
//	docs/skywire/cli/dmsg/cat/listen/README.md
//
// Each page links upward to its parent and downward to its children, so
// readers can navigate the tree without an external index.
//
// Sanitization: see sanitize() below — replaces any xpub-prefixed
// extended key with the Skycoin genesis address. The all-zeros
// cipher.SecKey sentinel is left untouched (it's the literal default
// rendered for SecKey flags and conveys "no key supplied" semantics).
package doc

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	outDir          string
	dryRun          bool
	maxLines        int
	captureMode     bool
	captureTimeout  time.Duration
	captureSkipNote bool
)

func init() {
	RootCmd.Flags().StringVarP(&outDir, "out", "o", "docs/skywire",
		"output directory for generated markdown")
	RootCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the list of files that would be written, don't write")
	RootCmd.Flags().IntVar(&maxLines, "max-lines", 30,
		"per-page line cap for captured command output (--capture only)")
	RootCmd.Flags().BoolVar(&captureMode, "capture", false,
		"run allowlisted state-printing commands against the local visor and embed their (truncated) output as a 'Sample output' section. Requires a running visor on --rpc; commands that fail are silently omitted")
	RootCmd.Flags().DurationVar(&captureTimeout, "capture-timeout", 5*time.Second,
		"per-command timeout when --capture is set")
	RootCmd.Flags().BoolVar(&captureSkipNote, "capture-skip-note", false,
		"when --capture fails for a command, omit the explanatory note instead of including 'Sample output unavailable: ...'")
}

// RootCmd is mounted by cmd/skywire/commands/root.go. Hidden — users
// who run `make generate` (or who know the command exists) will see it
// in --help with `--all` flag, but it's not part of the published CLI.
var RootCmd = &cobra.Command{
	Use:    "doc",
	Short:  "Regenerate docs/skywire/ from the live cobra tree",
	Hidden: true,
	Long: `Walk the cobra command tree rooted at the skywire binary and emit
one markdown page per command into the output directory, mirroring
the command hierarchy as nested directories. Replaces the hand-
maintained cmd/{skywire,skywire-cli,...}/README.md files which had
drifted and leaked private data.

Run from the repo root so the default --out path resolves:

  skywire doc                         # writes docs/skywire/...
  skywire doc --out /tmp/docs         # alternate output root
  skywire doc --dry-run               # list files, don't write
  skywire doc --capture               # also embed live state output
                                      # for allowlisted commands`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, _ []string) {
		// The doc subcommand walks the *root* — skip itself plus the
		// `doc` command (writing its own page would be circular noise).
		root := cmd.Root()
		var pages []page
		collect(root, []string{}, &pages)

		if dryRun {
			for _, p := range pages {
				fmt.Println(p.path())
			}
			return
		}

		for _, p := range pages {
			full := filepath.Join(outDir, p.path())
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil { //nolint:gosec // docs are world-readable
				fmt.Fprintf(os.Stderr, "doc: mkdir %s: %v\n", filepath.Dir(full), err)
				os.Exit(1)
			}
			f, err := os.Create(full) //nolint:gosec // path derived deterministically from outDir + cobra command segments
			if err != nil {
				fmt.Fprintf(os.Stderr, "doc: create %s: %v\n", full, err)
				os.Exit(1)
			}
			if err := render(f, p); err != nil {
				_ = f.Close() //nolint:errcheck
				fmt.Fprintf(os.Stderr, "doc: render %s: %v\n", full, err)
				os.Exit(1)
			}
			_ = f.Close() //nolint:errcheck
		}
		fmt.Printf("doc: wrote %d markdown files under %s\n", len(pages), outDir)
	},
}

// page captures everything needed to render one command's README. We
// snapshot the parts of cobra.Command we want at collection time so the
// render step is pure-string and trivially testable.
type page struct {
	// segs is the path of command names from root, e.g. ["cli","dmsg","cat"]
	// for `skywire cli dmsg cat`. Empty segs == root command page.
	segs []string

	short    string
	long     string
	useLine  string
	example  string
	local    string // LocalFlags().FlagUsages()
	global   string // InheritedFlags().FlagUsages()
	children []childRef

	// capture is populated only when --capture is set AND segs match an
	// entry in captureAllowlist. captureErr non-empty means we tried and
	// failed (e.g., visor unreachable) — render embeds the explanatory
	// note unless --capture-skip-note suppresses it.
	capture    string
	captureErr string
}

type childRef struct {
	name  string
	short string
}

// path returns the markdown file's path relative to outDir, e.g.
// "cli/dmsg/cat/README.md" or "README.md" for the root.
func (p page) path() string {
	if len(p.segs) == 0 {
		return "README.md"
	}
	return filepath.Join(append(append([]string{}, p.segs...), "README.md")...)
}

// title is the heading for the page: "skywire" or "skywire cli dmsg cat".
func (p page) title() string {
	return strings.Join(append([]string{"skywire"}, p.segs...), " ")
}

// collect walks the cobra tree depth-first, recording one page per
// non-hidden command. Skips the `completion` command (always emitted by
// cobra, never doc-worthy) and the `doc` command itself (would recurse
// the page being generated for it).
func collect(cmd *cobra.Command, segs []string, out *[]page) {
	if !cmd.IsAvailableCommand() && cmd.Name() != cmd.Root().Name() {
		return
	}
	if cmd.Name() == "completion" || cmd.Name() == "doc" {
		return
	}

	p := page{
		segs:    append([]string{}, segs...),
		short:   cmd.Short,
		long:    cmd.Long,
		useLine: cmd.UseLine(),
		example: cmd.Example,
		local:   cmd.LocalFlags().FlagUsages(),
		global:  cmd.InheritedFlags().FlagUsages(),
	}

	subs := visibleChildren(cmd)
	for _, c := range subs {
		p.children = append(p.children, childRef{name: c.Name(), short: c.Short})
	}

	if captureMode {
		if spec, ok := captureAllowlist[strings.Join(p.segs, " ")]; ok {
			p.capture, p.captureErr = runCapture(spec)
		}
	}

	*out = append(*out, p)

	for _, c := range subs {
		collect(c, append(segs, c.Name()), out)
	}
}

// captureSpec describes one allowlisted command we'll run when
// --capture is set. argv is everything AFTER the binary path; the
// generator invokes `os.Args[0]` (whichever skywire it was launched as)
// so the capture matches the running binary, not whatever's on PATH.
type captureSpec struct {
	argv  []string
	extra []string // optional extras (typically --json or limit flags) — folded into argv at exec
}

// captureAllowlist maps "cli dmsg sessions" (the space-joined cobra
// path under the root) to the args we pass to the binary. Keep this
// conservative: only commands that print state, take no required args,
// and complete quickly. State-changing commands (config gen, route
// add, etc.) are NEVER in this list.
var captureAllowlist = map[string]captureSpec{
	"cli visor info":         {argv: []string{"cli", "visor", "info"}},
	"cli visor pk":           {argv: []string{"cli", "visor", "pk"}},
	"cli visor ver":          {argv: []string{"cli", "visor", "ver"}},
	"cli visor ports":        {argv: []string{"cli", "visor", "ports"}},
	"cli visor ip":           {argv: []string{"cli", "visor", "ip"}},
	"cli visor dmsg-servers": {argv: []string{"cli", "visor", "dmsg-servers"}},
	"cli visor app ls":       {argv: []string{"cli", "visor", "app", "ls"}},
	"cli visor ready":        {argv: []string{"cli", "visor", "ready"}},
	"cli visor user":         {argv: []string{"cli", "visor", "user"}},
	"cli visor proxies":      {argv: []string{"cli", "visor", "proxies"}},
	"cli dmsg sessions":      {argv: []string{"cli", "dmsg", "sessions"}},
	"cli route groups":       {argv: []string{"cli", "route", "groups"}},
	"cli rg ls":              {argv: []string{"cli", "rg", "ls"}},
	"cli mdisc servers":      {argv: []string{"cli", "mdisc", "servers"}},
	"cli reward rules":       {argv: []string{"cli", "reward", "rules"}},
}

// runCapture invokes the spec and returns (stdout-truncated, errMsg).
// errMsg is empty on success; otherwise it's a short reason — used by
// render to embed an explanatory note instead of fake output. We use
// os.Args[0] so the capture matches the running binary; falls back to
// PATH lookup of "skywire" if argv[0] is something else (e.g., during
// `go run`).
func runCapture(spec captureSpec) (string, string) {
	binPath := os.Args[0]
	if !strings.HasSuffix(binPath, "skywire") {
		if p, err := exec.LookPath("skywire"); err == nil {
			binPath = p
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), captureTimeout)
	defer cancel()
	args := append([]string{}, spec.argv...)
	args = append(args, spec.extra...)
	cmd := exec.CommandContext(ctx, binPath, args...) //nolint:gosec // allowlisted args only
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return "", fmt.Sprintf("timed out after %s", captureTimeout)
	}
	if err != nil {
		// Trim trailing newlines in stderr — keep the first line as
		// the reason (visor RPC errors are typically one line).
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = err.Error()
		}
		if i := strings.IndexByte(reason, '\n'); i > 0 {
			reason = reason[:i]
		}
		return "", reason
	}
	return truncateLines(stdout.String(), maxLines), ""
}

// truncateLines caps captured output at n lines. When truncation
// happens the cut is marked so readers know they're seeing a slice
// rather than the full state.
func truncateLines(s string, n int) string {
	if n <= 0 {
		return s
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + fmt.Sprintf("\n... (%d more lines)\n", len(lines)-n)
}

// visibleChildren returns the subcommands we want to document, sorted
// for determinism. Mirrors collect's skip rules so the rendered
// "Subcommands:" list matches what actually gets generated.
func visibleChildren(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, c := range cmd.Commands() {
		if !c.IsAvailableCommand() {
			continue
		}
		if c.Name() == "completion" || c.Name() == "doc" {
			continue
		}
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// render writes the markdown for one page. Pure string output — no I/O
// other than the writer — so it can be unit-tested by feeding a
// hand-built page through it.
func render(w io.Writer, p page) error {
	var b strings.Builder

	fmt.Fprintf(&b, "# %s\n\n", p.title())

	// Breadcrumb back to the parent. The root page has no breadcrumb;
	// every other page links to ../README.md (since each command lives
	// in its own dir).
	if len(p.segs) > 0 {
		parentTitle := strings.Join(append([]string{"skywire"}, p.segs[:len(p.segs)-1]...), " ")
		fmt.Fprintf(&b, "[← %s](../README.md)\n\n", parentTitle)
	}

	desc := p.long
	if desc == "" {
		desc = p.short
	}
	if desc != "" {
		b.WriteString(sanitize(strings.TrimSpace(desc)))
		b.WriteString("\n\n")
	}

	if p.useLine != "" {
		b.WriteString("## Usage\n\n```\n")
		b.WriteString(sanitize(p.useLine))
		b.WriteString("\n```\n\n")
	}

	if len(p.children) > 0 {
		b.WriteString("## Subcommands\n\n")
		for _, c := range p.children {
			// Link to the child's directory (which contains README.md).
			// `name/` resolves to `name/README.md` on every markdown
			// renderer we care about (GitHub, gitiles, mkdocs, etc.).
			fmt.Fprintf(&b, "- [%s](%s/README.md) — %s\n", c.name, c.name, sanitize(strings.TrimSpace(c.short)))
		}
		b.WriteString("\n")
	}

	if p.example != "" {
		b.WriteString("## Examples\n\n```\n")
		b.WriteString(sanitize(strings.TrimSpace(p.example)))
		b.WriteString("\n```\n\n")
	}

	if strings.TrimSpace(p.local) != "" {
		b.WriteString("## Flags\n\n```\n")
		b.WriteString(sanitize(strings.TrimRight(p.local, "\n")))
		b.WriteString("\n```\n\n")
	}

	if strings.TrimSpace(p.global) != "" {
		b.WriteString("## Global Flags\n\n```\n")
		b.WriteString(sanitize(strings.TrimRight(p.global, "\n")))
		b.WriteString("\n```\n\n")
	}

	if p.capture != "" {
		b.WriteString("## Sample output\n\n_Captured live from a running visor; truncated to ")
		fmt.Fprintf(&b, "%d", maxLines)
		b.WriteString(" lines._\n\n```\n")
		b.WriteString(sanitize(strings.TrimRight(p.capture, "\n")))
		b.WriteString("\n```\n\n")
	} else if p.captureErr != "" && !captureSkipNote {
		b.WriteString("## Sample output\n\n_Capture unavailable: ")
		b.WriteString(sanitize(p.captureErr))
		b.WriteString("._\n\n")
	}

	b.WriteString("---\n_Generated by `skywire doc` — do not edit by hand._\n")

	_, err := io.WriteString(w, b.String())
	return err
}

// xpubRE matches Skycoin/HD-wallet extended public keys. Skywire's
// reward command prints whatever the operator has configured, so a
// generator running on an operator's box would otherwise leak their
// reward address into the docs (which is exactly what happened with
// cmd/skywire-cli/README.md before this generator existed).
var xpubRE = regexp.MustCompile(`xpub6[A-HJ-NP-Za-km-z1-9]{100,}`)

// versionRE matches the build-version string buildinfo embeds into
// user-agent / default-value strings (e.g. "v1.3.54-0.-82e6f3b5ee3d+dirty").
// Without scrubbing this, every commit would change every page in
// docs/skywire/ that references a default value carrying the version —
// making the generated tree perpetually dirty in PRs that don't touch
// any user-facing surface.
var versionRE = regexp.MustCompile(`v\d+\.\d+\.\d+(?:-\d+)?(?:\.[0-9-]*[0-9a-f]{12})?(?:\+\w+)?`)

// buildDateRE matches the ISO-8601 build timestamp buildinfo embeds in
// the root command's Long description. Same rationale as versionRE:
// without normalizing it, every commit dirties docs/skywire/README.md.
var buildDateRE = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z`)

// skycoinGenesisAddr is the well-known address of the first Skycoin
// block. We substitute it for any real xpub the generator encounters so
// example output stays valid (the reward command needs SOME address-
// shaped string in its output) without leaking the build host's key.
const skycoinGenesisAddr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"

// versionPlaceholder is what replaces any embedded build version. The
// "<version>" form is a deliberately non-version-shaped string so a
// future render pass doesn't try to "re-normalize" it.
const versionPlaceholder = "<version>"

// buildDatePlaceholder mirrors versionPlaceholder for ISO timestamps.
const buildDatePlaceholder = "<build-date>"

// sanitize is applied to every piece of captured/embedded command
// content before it's written to disk. Keep this monotonically additive
// — anything we strip here was unsafe or commit-dependent to publish at
// least once.
func sanitize(s string) string {
	s = xpubRE.ReplaceAllString(s, skycoinGenesisAddr)
	s = versionRE.ReplaceAllString(s, versionPlaceholder)
	s = buildDateRE.ReplaceAllString(s, buildDatePlaceholder)
	return s
}
