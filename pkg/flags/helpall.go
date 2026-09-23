// Package flags pkg/flags/helpall.go c0-com-util
//
// InstallHelp replaces cobra's default (auto-generated) help subcommand
// with a flag-aware version that consolidates three previously-separate
// concerns — printing every subcommand's help in one pass, rendering
// the subcommand tree, and regenerating markdown docs — into the same
// `help` command users already reach for. Three mutually-exclusive
// mode flags (-r / -t / -d) pick between the variants.
//
// Why not a separate `help-all`? `help` already exists at every level
// of a cobra tree, invocation is universally understood, and
// overloading it avoids cluttering the top-level command list with a
// near-duplicate.
package flags

import (
	"fmt"
	"os"
	"strings"

	"github.com/skycoin/skywire/pkg/cliout"
	"github.com/skycoin/skywire/pkg/cliout/clihelp"

	"github.com/spf13/cobra"
)

// InstallHelp installs a flag-aware help command on root. Replaces
// cobra's auto-generated help command for that scope. Safe to call
// on multiple roots — each gets its own closure-captured flag state.
//
// Modes (mutually exclusive):
//
//	(default)     print root's help (or [command]'s if an arg is given)
//	-r/--recursive print root + every descendant's help
//	-t/--tree     print the subcommand tree only (no help text)
//	-d/--doc      print markdown documentation
//
// With an argument (`cmd help <subcmd>`), the flags apply to the named
// subcommand's subtree rather than the root's, matching the intuition
// of `help <subcmd>`.
func InstallHelp(root *cobra.Command) {
	var recursive, tree, doc bool
	helpCmd := &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		Long: `Print help for the given command, or for the root command when no argument is supplied.

Modes (mutually exclusive):
  (default)      print the command's own help
  -r, --recursive print the command's help plus every descendant
  -t, --tree     print the subcommand tree only (no help text)
  -d, --doc      print markdown documentation`,
		Hidden:                true,
		SilenceErrors:         true,
		SilenceUsage:          true,
		DisableSuggestions:    true,
		DisableFlagsInUseLine: true,
		Run: func(_ *cobra.Command, args []string) {
			target := root
			if len(args) > 0 {
				if found, _, err := root.Find(args); err == nil && found != nil {
					target = found
				}
			}
			switch {
			case tree:
				PrintCommandTree(target)
			case doc:
				PrintCommandDocs(target)
			case recursive:
				PrintHelpAll(target, true)
			default:
				// Plain, like the three modes above it. `--help` is the
				// screen — colored, over the code rain — and `help` is the
				// same text without the backdrop, for reading closely or
				// copying out. One decorated form and one clean one, neither
				// needing an environment variable to reach. See rain.go.
				withPlainHelp(func() {
					_ = target.Help() //nolint:errcheck,gosec
				})
			}
		},
	}
	helpCmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "print help for every descendant as well")
	helpCmd.Flags().BoolVarP(&tree, "tree", "t", false, "print the subcommand tree only")
	helpCmd.Flags().BoolVarP(&doc, "doc", "d", false, "print markdown documentation")
	helpCmd.MarkFlagsMutuallyExclusive("recursive", "tree", "doc")
	// SetHelpCommand alone stores helpCmd in root.helpCommand but
	// cobra's InitDefaultHelpCmd (which also adds the helpCommand to
	// root's commands slice so it resolves as `root help …`) runs
	// only on the top-level root at Execute time. Subcommand roots
	// (e.g. `skywire cli`) need the helpCommand wired up manually —
	// without the AddCommand, `cli help` would still print help, but
	// via a fallback path that never parses our -r/-t/-d flags.
	// Remove a previously-added "help" child first so repeated
	// InstallHelp calls on the same root are idempotent.
	if prev := findChild(root, "help"); prev != nil {
		root.RemoveCommand(prev)
	}
	root.SetHelpCommand(helpCmd)
	root.AddCommand(helpCmd)

	// `<root> tree` prints the command hierarchy and nothing else — the same
	// rendering as `help -t`, reachable as a verb because that is how people
	// look for it. Hidden, since it duplicates a flag that is already
	// documented; discoverability is not the point, muscle memory is.
	//
	// Installed here rather than on one root so every command group gets it
	// at its own level: `skywire tree` shows everything, `skywire cli tree`
	// shows the CLI subtree.
	if prev := findChild(root, "tree"); prev != nil {
		root.RemoveCommand(prev)
	}
	root.AddCommand(&cobra.Command{
		Use:    "tree",
		Short:  "print the subcommand tree",
		Hidden: true,
		Args:   cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			// One value, two renderings: the ASCII tree is Node.Human, so the
			// text and JSON forms cannot describe different trees.
			if err := cliout.Print(cmd, clihelp.TreeOf(cmd.Parent())); err != nil {
				fmt.Fprintln(os.Stderr, err) //nolint:errcheck
			}
		},
	})
}

// InstallHelpTree installs the flag-aware `help` on root and on every command
// beneath it that has subcommands of its own.
//
// Without it, `help` is a real command only on the few roots InstallHelp was
// called on, and at any greater depth it only appears to work: cobra prints a
// command's help for any argument it cannot resolve, so `visor help` and
// `visor zzzz` are the same code path. The flags are the tell — `visor help -t`
// fails with "unknown shorthand flag: 't'" because nothing is parsing them.
//
// Descendants get it only if they have visible subcommands of their own. A
// leaf takes positional arguments, and giving it a `help` child would capture
// an argument that was meant for it; a parent that already prints help for an
// unrecognized argument loses nothing by having the word resolve properly
// instead. Root is exempt from that rule — a binary's own root gets `help`
// whether or not it has subcommands, which is what InstallHelp has always
// done for it.
//
// That leaves the commands which are both a parent and runnable, where the
// word is genuinely ambiguous, and the answer there is the POSIX one rather
// than a special case: `cmd -- help` passes it through as an argument. Cobra
// stops looking for subcommands at `--` (stripFlags breaks out of its loop),
// so the argument reaches the command and is rejected or used on its own
// terms. Sniffing for args[0] == "help" somewhere instead would claim the
// word just as firmly, only in a place with no flags parsed and no way to
// opt out.
//
// Call it once the tree is assembled, and before anything that wraps help
// functions (InitRain, tui.Install), so the commands it adds are wrapped too.
func InstallHelpTree(root *cobra.Command) {
	installHelpTree(root, true)
}

func installHelpTree(c *cobra.Command, isRoot bool) {
	// Snapshot the children first: InstallHelp adds `help` and `tree` to c,
	// and recursing into those would be pointless at best.
	children := append([]*cobra.Command(nil), c.Commands()...)
	if isRoot || c.HasAvailableSubCommands() {
		InstallHelp(c)
	}
	for _, sub := range children {
		installHelpTree(sub, false)
	}
}

// findChild returns the direct child of c matching name, or nil.
func findChild(c *cobra.Command, name string) *cobra.Command {
	for _, child := range c.Commands() {
		if child.Name() == name {
			return child
		}
	}
	return nil
}

// PrintHelpAll writes root.Help() followed by each subcommand's Help().
// With recursive=true, every descendant is included.
//
// Plain: this produces text to keep rather than to look at, and a frame of
// help-rain per command in the tree is not that. See withPlainHelp.
func PrintHelpAll(root *cobra.Command, recursive bool) {
	withPlainHelp(func() {
		printCmdHelp(root)
		for _, c := range root.Commands() {
			if !c.IsAvailableCommand() {
				continue
			}
			printCmdHelp(c)
			if recursive {
				printDescendantsHelp(c)
			}
		}
	})
}

func printDescendantsHelp(c *cobra.Command) {
	for _, child := range c.Commands() {
		if !child.IsAvailableCommand() {
			continue
		}
		printCmdHelp(child)
		printDescendantsHelp(child)
	}
}

func printCmdHelp(c *cobra.Command) {
	fmt.Println(strings.Repeat("=", 72))
	fmt.Printf("%s\n", c.CommandPath())
	fmt.Println(strings.Repeat("=", 72))
	_ = c.Help() //nolint:errcheck,gosec
	fmt.Println()
}

// PrintCommandTree writes an ASCII tree of the command hierarchy rooted
// at root. Kept dependency-free (no pterm / tablewriter) so the
// utilities package stays lightweight.
func PrintCommandTree(root *cobra.Command) {
	fmt.Println(root.Name())
	printTreeChildren(root, "")
}

func printTreeChildren(c *cobra.Command, prefix string) {
	// Materialize visible children once so the last-child branch char
	// (`└──`) is picked correctly when some children are hidden.
	var visible []*cobra.Command
	for _, child := range c.Commands() {
		if !child.IsAvailableCommand() {
			continue
		}
		visible = append(visible, child)
	}
	for i, child := range visible {
		isLast := i == len(visible)-1
		branch := "├── "
		nextPrefix := prefix + "│   "
		if isLast {
			branch = "└── "
			nextPrefix = prefix + "    "
		}
		fmt.Printf("%s%s%s\n", prefix, branch, child.Name())
		printTreeChildren(child, nextPrefix)
	}
}

// PrintCommandDocs emits a Markdown document describing every command
// in the tree rooted at root. Headings deepen with nesting (root = H1,
// direct children = H2, etc.), capped at H6 to stay within CommonMark.
// The help text is wrapped in fenced code blocks.
func PrintCommandDocs(root *cobra.Command) {
	// Plain: this is markdown for a file. Escape sequences in a fenced code
	// block are not decoration, they are litter. See withPlainHelp.
	withPlainHelp(func() {
		fmt.Printf("# %s\n\n", root.CommandPath())
		printCmdDoc(root, 1)
		walkCmdDocs(root, 2)
	})
}

func walkCmdDocs(c *cobra.Command, depth int) {
	for _, child := range c.Commands() {
		if !child.IsAvailableCommand() {
			continue
		}
		printCmdDoc(child, depth)
		walkCmdDocs(child, depth+1)
	}
}

func printCmdDoc(c *cobra.Command, depth int) {
	if depth > 6 {
		depth = 6
	}
	fmt.Printf("%s %s\n\n", strings.Repeat("#", depth), c.CommandPath())
	fmt.Println("```")
	_ = c.Help() //nolint:errcheck,gosec
	fmt.Println("```")
	fmt.Println()
}
