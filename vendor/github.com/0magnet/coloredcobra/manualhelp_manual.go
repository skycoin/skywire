//go:build tinygo || js

package coloredcobra

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

// installManualHelp installs a template-free usage/help renderer that
// reproduces cobra's default layout with the colors carried in cfg.
//
// TinyGo cannot execute cobra's text/template help: it calls template methods
// through reflect.Value.Call, which TinyGo does not implement. The Go js/wasm
// build can, but a usage template makes text/template's MethodByName
// reachable, and the linker then keeps every exported method in the program:
// about 5 MB of a 29 MB page. So js builds use this renderer too.
const templatedHelp = false

func installManualHelp(cfg *Config) {
	cfg.RootCmd.SetUsageFunc(func(c *cobra.Command) error {
		return renderUsage(cfg, c)
	})
	// As cobra's help template has it: the description, a blank line, then the
	// usage, all of it on stdout.
	cfg.RootCmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		w := c.OutOrStdout()
		if text := strings.TrimSpace(c.Long); text != "" {
			_, _ = fmt.Fprintf(w, "%s\n\n", text)
		} else if text := strings.TrimSpace(c.Short); text != "" {
			_, _ = fmt.Fprintf(w, "%s\n\n", text)
		}
		_, _ = fmt.Fprint(w, c.UsageString())
	})
}

const ansiReset = "\x1b[0m"

func renderUsage(cfg *Config, c *cobra.Command) error {
	w := &errWriter{w: c.OutOrStderr()}
	on := colorEnabled()
	paint := func(param uint8, s string) string {
		if !on {
			return s
		}
		code := ansiCode(param)
		if code == "" {
			return s
		}
		return code + s + ansiReset
	}

	w.printf("%s\n  %s\n", paint(cfg.Headings, "Usage:"), paint(cfg.ExecName, c.UseLine()))

	if len(c.Aliases) > 0 {
		w.printf("\n%s\n  %s\n", paint(cfg.Headings, "Aliases:"), paint(cfg.Aliases, c.NameAndAliases()))
	}
	if c.HasExample() {
		w.printf("\n%s\n%s\n", paint(cfg.Headings, "Examples:"), paint(cfg.Example, c.Example))
	}

	if c.HasAvailableSubCommands() {
		w.printf("\n%s\n", paint(cfg.Headings, "Available Commands:"))
		for _, sub := range c.Commands() {
			if !sub.IsAvailableCommand() || sub.Name() == "completion" {
				continue
			}
			w.printf("  %s %s\n",
				paint(cfg.Commands, padRight(sub.Name(), 18)),
				paint(cfg.CmdShortDescr, sub.Short))
		}
	}
	if c.HasAvailableLocalFlags() {
		w.printf("\n%s\n%s", paint(cfg.Headings, "Flags:"), colorFlags(cfg, c.LocalFlags().FlagUsages(), on))
	}
	if c.HasAvailableInheritedFlags() {
		w.printf("\n%s\n%s", paint(cfg.Headings, "Global Flags:"), colorFlags(cfg, c.InheritedFlags().FlagUsages(), on))
	}
	if c.HasAvailableSubCommands() {
		w.printf("\nUse \"%s [command] --help\" for more information about a command.\n", c.CommandPath())
	}
	return w.err
}

// colorFlags colorizes the flag name and description on each line of a
// pflag.FlagUsages() block. Continuation (wrapped) lines are left untouched.
func colorFlags(cfg *Config, usages string, on bool) string {
	if !on {
		return usages
	}
	lines := strings.Split(usages, "\n")
	for i, ln := range lines {
		spec := strings.TrimLeft(ln, " ")
		if !strings.HasPrefix(spec, "-") {
			continue
		}
		indent := ln[:len(ln)-len(spec)]
		// The flag spec is separated from the description by 3+ spaces.
		if idx := strings.Index(spec, "   "); idx >= 0 {
			rest := spec[idx:]
			descOff := len(rest) - len(strings.TrimLeft(rest, " "))
			lines[i] = indent + wrap(cfg.Flags, spec[:idx]) + rest[:descOff] + wrap(cfg.FlagsDescr, rest[descOff:])
		} else {
			lines[i] = indent + wrap(cfg.Flags, spec)
		}
	}
	return strings.Join(lines, "\n")
}

func wrap(param uint8, s string) string {
	code := ansiCode(param)
	if code == "" || s == "" {
		return s
	}
	return code + s + ansiReset
}

// ansiCode maps a coloredcobra color value (low nibble = color 1-15, plus the
// Bold/Italic/Underline bit flags) to an ANSI SGR escape sequence.
func ansiCode(param uint8) string {
	var codes []string
	switch cv := param & 0x0F; {
	case cv >= Black && cv <= White: // 1..8 -> 30..37
		codes = append(codes, strconv.Itoa(29+int(cv)))
	case cv >= HiRed && cv <= HiWhite: // 9..15 -> 91..97
		codes = append(codes, strconv.Itoa(82+int(cv)))
	}
	if param&Bold != 0 {
		codes = append(codes, "1")
	}
	if param&Italic != 0 {
		codes = append(codes, "3")
	}
	if param&Underline != 0 {
		codes = append(codes, "4")
	}
	if len(codes) == 0 {
		return ""
	}
	return "\x1b[" + strings.Join(codes, ";") + "m"
}

func padRight(s string, n int) string {
	if len(s) >= n {
		return s
	}
	return s + strings.Repeat(" ", n-len(s))
}

// colorEnabled reports whether colored output should be emitted, mirroring
// fatih/color's rules (which coloredcobra uses on other toolchains): honor
// NO_COLOR and TERM=dumb, and only colorize when stdout is a terminal.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	return isTerminal(os.Stdout.Fd())
}

// errWriter keeps the first write error, so the usage can be written in one
// pass and the error reported once at the end.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, a ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, a...)
	}
}
