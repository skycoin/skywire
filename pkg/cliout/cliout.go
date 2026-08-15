// Package cliout renders skywire-cli command results.
//
// A command produces ONE value. This package decides how it is written: as
// JSON for a machine, or as text for a person. That is the whole idea, and it
// is a correction of what came before — PrintOutput took a JSON value AND a
// pre-rendered human string as separate arguments, so nothing connected them.
// Two arguments describing one fact drift: `visor info` printed
// "DMSG Latency: 0s" from the human side while the JSON side carried the same
// unmeasured zero, because each was written on its own.
//
// So the value renders itself. Its struct tags are the machine contract; its
// Human method is the terminal rendering of the same fields. Neither can be
// updated without the other being right there.
//
// # Shape
//
// The printer is one method over a writer, after kubernetes' ResourcePrinter
// (`PrintObj(runtime.Object, io.Writer) error`): a command hands over a value
// and never learns which format was chosen, so a new format is a new printer
// rather than an edit to 387 commands.
//
// TTY handling follows gh: indented for a terminal, compact when piped, since
// the second case is a parser and the first is a person. gh reaches this
// through iostreams.IsStdoutTTY(); here the writer is inspected directly, to
// avoid threading a stream object through a CLI that does not have one yet.
//
// # Errors
//
// Print returns them. It does not exit. PrintFatalError's os.Exit(1) runs no
// deferred close, flush or unlock, and cannot be tested without a subprocess.
// A command's RunE returns the error and the root renders it once.
package cliout

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// JSONFlag is the persistent flag that selects machine output.
const JSONFlag = "json"

// Output is a command result that can render itself for a person.
//
// Implementing it is optional: a value without it still prints as JSON, and
// still prints in text mode via its Go representation, which is a reasonable
// last resort for a command whose result is a bare string or count.
type Output interface {
	// Human writes the terminal rendering. Trailing newline included.
	Human(w io.Writer) error
}

// Print writes v to the command's output in whichever format was asked for.
//
// The value is the single source of truth: its json tags define the machine
// shape, its Human method (when it has one) the text.
func Print(cmd *cobra.Command, v interface{}) error {
	w := cmd.OutOrStdout()
	// --shape: print the type skeleton instead of the value.
	if ShapeMode(cmd) {
		return Fprint(w, true, Skeleton(v))
	}
	// --jq: filter the JSON value through gojq (implies --json).
	if filter := JQFilter(cmd); filter != "" {
		return printJQ(w, v, filter)
	}
	return Fprint(w, JSONMode(cmd), v)
}

// Fprint is Print against an explicit writer, for callers that are not a
// cobra command — tests above all, which is the other thing the old helper
// made hard by writing to os.Stdout through fmt.Print.
func Fprint(w io.Writer, jsonMode bool, v interface{}) error {
	if jsonMode {
		enc := json.NewEncoder(w)
		if IsTerminal(w) {
			enc.SetIndent("", "  ")
		}
		// Encode streams rather than buffering the whole document, writes the
		// trailing newline itself, and renders a nil value as `null` — which
		// matters, because a consumer piping into a parser gets a valid
		// document for "no result" instead of an empty file and a syntax
		// error.
		return enc.Encode(v)
	}
	if v == nil {
		return nil
	}
	if h, ok := v.(Output); ok {
		return h.Human(w)
	}
	// No Human method. A bare string prints as itself; anything else prints
	// as Go sees it, which is at least honest about being unformatted.
	if s, ok := v.(string); ok {
		if s == "" {
			return nil
		}
		_, err := io.WriteString(w, s)
		return err
	}
	_, err := fmt.Fprintln(w, v)
	return err
}

// JSONMode reports whether machine output was requested.
//
// It searches the inherited flags too. The old helper called GetBool on the
// command's own set and discarded the error, so a command whose set did not
// carry the flag silently produced human text for `--json` — indistinguishable
// from the user not having asked.
func JSONMode(cmd *cobra.Command) bool {
	if f := cmd.Flags().Lookup(JSONFlag); f != nil {
		return f.Value.String() == "true"
	}
	if f := cmd.InheritedFlags().Lookup(JSONFlag); f != nil {
		return f.Value.String() == "true"
	}
	return false
}

// IsTerminal reports whether w is a character device, i.e. a person is
// probably reading it. Checking the file mode keeps this dependency-free;
// anything that is not an *os.File (a buffer in a test, a pipe) is not a
// terminal, which is the answer those callers want anyway.
func IsTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}
