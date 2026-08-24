// Package flags pkg/flags/rain.go
//
// InitRain prints the help screen over a still frame of the Matrix code rain,
// seeded from the clock so it is different every time. It is decoration and
// nothing else, and it is confined to the one screen where decoration costs
// nothing: help is printed once, read, and scrolled past. Nothing parses it.
//
// It is off in every case where the output is not a person reading a terminal
// — see rainOptions.
package flags

import (
	"os"
	"sync"

	"github.com/spf13/cobra"

	"github.com/0magnet/termanim/matrix/backdrop"
	"github.com/0magnet/termanim/matrix/backdrop/cobrarain"

	"github.com/skycoin/skywire/pkg/cliout"
)

// NoRainEnv turns the backdrop off while leaving the help colored. NO_COLOR
// turns off both, which is what NO_COLOR is for; this is for someone who wants
// the colors and not the rain.
const NoRainEnv = "SKYWIRE_NO_HELP_RAIN"

var (
	// plainHelp suppresses the backdrop for the duration of a call that is
	// producing help text rather than showing it. `help -d` writes markdown
	// meant to be committed to a file and `help -r` dumps every command in the
	// tree; neither wants a frame of rain per command, and the markdown would
	// carry the escapes into the document.
	plainHelp   bool
	plainHelpMu sync.Mutex

	// rained records the roots already wired, so calling InitRain twice on one
	// command does not put the rain behind a screen that already has rain
	// behind it.
	rained   = map[*cobra.Command]bool{}
	rainedMu sync.Mutex
)

// InitRain makes cmd, and every command under it, print its help over the code
// rain.
//
// Call it on the root, and call it once the tree is assembled: it walks the
// commands rather than leaving cobra's inheritance to carry the help function
// down, so a subcommand added afterwards gets the backdrop only by inheritance
// and only if nothing in its own subtree installs a help function. See
// eachDeepestFirst.
//
// Deliberately not part of InitFlags. That runs for every binary in this
// repository, including the services, and this is meant for the one people
// read help in.
func InitRain(cmd *cobra.Command) {
	rainedMu.Lock()
	defer rainedMu.Unlock()
	eachDeepestFirst(cmd, func(c *cobra.Command) {
		if rained[c] {
			return
		}
		rained[c] = true
		cobrarain.OnFunc(c, rainOptions)
	})
}

// eachDeepestFirst calls fn for every command in the tree, children before
// parents.
//
// Every command rather than just the root, because cobra's inheritance is not
// enough on its own. It looks the help function up on the parent only when a
// command has none of its own, and skywire-cli's root installs one for --json
// — which shadowed the inherited one for the whole of `skywire cli`, so that
// subtree was the one part of the binary printing its help without a backdrop.
// Anything else that wraps help in its own init would break the chain the same
// way, so the tree is covered command by command instead of being relied upon
// to pass it down.
//
// Children before parents is the part that matters. Wrapping a command
// captures whatever help function it has at that moment, and a command with
// none of its own reports its parent's — so wrapping a parent first and then
// its child would wrap the parent's new wrapper a second time and paint the
// rain twice, over itself.
func eachDeepestFirst(c *cobra.Command, fn func(*cobra.Command)) {
	for _, sub := range c.Commands() {
		eachDeepestFirst(sub, fn)
	}
	fn(c)
}

// rainOptions decides, per help screen, whether to draw the rain.
//
// Off means "hand back exactly the bytes the help function produced". The
// cases are every one where the output is not a person reading a terminal:
//
//   - machine mode (--json / --jq / --shape), where Help prints a schema for
//     something else to consume rather than help for someone to read;
//   - `help -r` and `help -d`, which produce text to keep rather than to look
//     at, the second of it markdown;
//   - SKYWIRE_NO_HELP_RAIN, for someone who wants the colors without this.
//
// Not a terminal at all — a pipe, a redirect, `--help | less`, a --help pasted
// into a bug report — is handled inside backdrop, along with NO_COLOR.
//
// Machine mode is checked here rather than relied upon by ordering. cliout's
// own help wrapper and this one are installed in different init functions, and
// which of them ends up outermost is not something to build on.
func rainOptions(cmd *cobra.Command) backdrop.Options {
	plainHelpMu.Lock()
	plain := plainHelp
	plainHelpMu.Unlock()

	return backdrop.Options{
		Off: plain || cliout.MachineMode(cmd) || os.Getenv(NoRainEnv) != "",
	}
}

// withPlainHelp runs fn with the backdrop suppressed.
func withPlainHelp(fn func()) {
	plainHelpMu.Lock()
	plainHelp = true
	plainHelpMu.Unlock()
	defer func() {
		plainHelpMu.Lock()
		plainHelp = false
		plainHelpMu.Unlock()
	}()
	fn()
}

// WithPlainHelp runs fn with the backdrop suppressed, and is what anything
// that captures help text rather than showing it should use.
//
// The docs and recursive modes above are the obvious callers. The other is a
// program that renders help into a pane of its own — an interactive browser
// over the command tree — which wants cobra's help exactly as it is, colors
// and all, and will decide for itself what goes behind it.
func WithPlainHelp(fn func()) { withPlainHelp(fn) }
