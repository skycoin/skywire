// Package cobrarain puts the code rain behind a cobra command's help.
//
// It is one call, and it is separate from matrix/backdrop so that package
// stays free of a CLI framework:
//
//	cobrarain.On(rootCmd, backdrop.Options{})
//
// Help for every command under the root goes through it, because cobra looks
// up the help function on the parent when a command has none of its own.
package cobrarain

import (
	"bytes"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/0magnet/termanim/matrix/backdrop"
)

// On makes cmd, and everything under it, print its help over a still frame of
// the rain.
//
// The help itself is cobra's — it is rendered into a buffer by whatever help
// function was in effect and then composited, rather than being replaced by a
// template of ours. Every rule cobra has about what goes in a help screen and
// where still applies; the only difference is what is behind it.
//
// Only Help, not Usage. Usage is what a command prints to stderr when it is
// called wrongly, which is a thing scripts capture and people grep, and it
// should stay plain.
func On(cmd *cobra.Command, o backdrop.Options) {
	OnFunc(cmd, func(*cobra.Command) backdrop.Options { return o })
}

// OnFunc is On with the options decided when the help is printed rather than
// when it is wired up, and decided per command.
//
// Two things need this. A command whose own flags tune the backdrop is one:
// cobra parses flags before it prints help, so the values are there by then
// but not at the point On would have been called. The other is a program that
// has more than one thing behind Help — a --json mode that prints a schema, a
// docs mode that prints markdown — where the backdrop belongs on one of them
// and not the others. Those wrappers may have been installed either side of
// this one, so which is outermost is not something to rely on; return
// Options{Off: true} for the ones that should stay plain and it does not
// matter what order anything was wired up in.
func OnFunc(cmd *cobra.Command, opts func(*cobra.Command) backdrop.Options) {
	def := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		o := opts(c)
		out := c.OutOrStdout()
		if o.Off {
			// Straight through, with nothing buffered. A mode that streams,
			// or one that writes to somewhere other than c's own writer,
			// then behaves exactly as it did before this was wired in.
			def(c, args)
			return
		}
		var buf bytes.Buffer
		c.SetOut(&buf)
		def(c, args)
		c.SetOut(out)
		fmt.Fprint(out, backdrop.Render(buf.String(), o))
	})
}
