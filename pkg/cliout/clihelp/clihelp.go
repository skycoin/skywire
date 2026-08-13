// Package clihelp is the machine-readable shape of `skywire cli --help`.
//
// `--help --json` emits this instead of the rendered help text. The point is
// discovery without scraping: one call at the root returns the whole command
// tree with every flag, its type and its default, so a caller can find the
// command it needs — or check what a flag is called — without running 387
// commands and parsing prose that was written for a person.
//
// Cobra offers nothing here. It renders help through a text/template and has
// no notion of a machine format (there is no JSON anywhere in the library), so
// the schema is ours to define. What it does offer is SetHelpFunc, which
// children inherit — so registering once on the root covers every subcommand.
package clihelp

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Flag is one flag as the parser sees it.
type Flag struct {
	Name string `json:"name"`
	// Shorthand is the single-letter form without its dash, empty if none.
	Shorthand string `json:"shorthand,omitempty"`
	// Type is pflag's own name for the value: "string", "bool", "int",
	// "stringSlice" and so on. It is what tells a caller whether the flag
	// takes an argument.
	Type       string `json:"type"`
	Default    string `json:"default,omitempty"`
	Usage      string `json:"usage,omitempty"`
	Deprecated string `json:"deprecated,omitempty"`
	Hidden     bool   `json:"hidden,omitempty"`
}

// Command is one command and, recursively, everything under it.
type Command struct {
	// Name is the first word of Use; Path is how you would type it,
	// e.g. "skywire cli visor info". Both are given because callers want
	// different ones: Name to match, Path to run.
	Name    string `json:"name"`
	Path    string `json:"path"`
	Use     string `json:"use,omitempty"`
	Short   string `json:"short,omitempty"`
	Long    string `json:"long,omitempty"`
	Example string `json:"example,omitempty"`

	Aliases    []string `json:"aliases,omitempty"`
	Deprecated string   `json:"deprecated,omitempty"`
	Hidden     bool     `json:"hidden,omitempty"`
	// Runnable distinguishes a command that does something from a group that
	// only holds subcommands.
	Runnable bool `json:"runnable"`

	// Flags are declared on this command; Inherited come from ancestors
	// (--json among them). Kept apart because the distinction decides where
	// a flag may be placed on the command line.
	Flags     []Flag `json:"flags,omitempty"`
	Inherited []Flag `json:"inherited_flags,omitempty"`

	Subcommands []Command `json:"subcommands,omitempty"`
}

// Of builds the schema for cmd and everything beneath it.
func Of(cmd *cobra.Command) Command {
	out := Command{
		Name:       cmd.Name(),
		Path:       cmd.CommandPath(),
		Use:        cmd.Use,
		Short:      cmd.Short,
		Long:       cmd.Long,
		Example:    cmd.Example,
		Aliases:    cmd.Aliases,
		Deprecated: cmd.Deprecated,
		Hidden:     cmd.Hidden,
		Runnable:   cmd.Runnable(),
		Flags:      flagsOf(cmd.LocalFlags()),
		Inherited:  flagsOf(cmd.InheritedFlags()),
	}
	for _, sub := range cmd.Commands() {
		// Cobra's generated help/completion commands describe the CLI
		// framework rather than skywire, and repeating them under every group
		// is most of the noise in a full tree dump.
		if sub.Name() == "help" || sub.Name() == "completion" {
			continue
		}
		out.Subcommands = append(out.Subcommands, Of(sub))
	}
	return out
}

func flagsOf(set *pflag.FlagSet) []Flag {
	var out []Flag
	set.VisitAll(func(f *pflag.Flag) {
		out = append(out, Flag{
			Name:       f.Name,
			Shorthand:  f.Shorthand,
			Type:       f.Value.Type(),
			Default:    f.DefValue,
			Usage:      strings.TrimSpace(f.Usage),
			Deprecated: f.Deprecated,
			Hidden:     f.Hidden,
		})
	})
	return out
}

// Command deliberately does NOT implement cliout.Output. Rendering help as
// text is cobra's job and it already does it from these same fields; a Human
// method here would be a second layout to keep in step with the first, and a
// no-op one would silently print nothing. The help function branches instead:
// JSON from this schema, text from cobra's own help.
