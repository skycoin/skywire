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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

var (
	outDir   string
	dryRun   bool
	maxLines int
)

func init() {
	RootCmd.Flags().StringVarP(&outDir, "out", "o", "docs/skywire",
		"output directory for generated markdown")
	RootCmd.Flags().BoolVar(&dryRun, "dry-run", false,
		"print the list of files that would be written, don't write")
	RootCmd.Flags().IntVar(&maxLines, "max-lines", 30,
		"per-page line cap for captured command output (currently unused; reserved for state-capture mode)")
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
  skywire doc --dry-run               # list files, don't write`,
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
			if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
				fmt.Fprintf(os.Stderr, "doc: mkdir %s: %v\n", filepath.Dir(full), err)
				os.Exit(1)
			}
			f, err := os.Create(full)
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
	*out = append(*out, p)

	for _, c := range subs {
		collect(c, append(segs, c.Name()), out)
	}
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

// skycoinGenesisAddr is the well-known address of the first Skycoin
// block. We substitute it for any real xpub the generator encounters so
// example output stays valid (the reward command needs SOME address-
// shaped string in its output) without leaking the build host's key.
const skycoinGenesisAddr = "2jBbGxZRGoQG1mqhPBnXnLTxK6oxsTf8os6"

// sanitize is applied to every piece of captured/embedded command
// content before it's written to disk. Keep this monotonically additive
// — anything we strip here was unsafe to publish at least once.
func sanitize(s string) string {
	return xpubRE.ReplaceAllString(s, skycoinGenesisAddr)
}
