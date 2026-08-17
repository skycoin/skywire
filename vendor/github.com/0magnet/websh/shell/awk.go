package shell

// awk via goawk — a full POSIX awk implementation in pure Go. Input
// comes from stdin or vfs files; goawk's own file opening is disabled
// so everything stays inside the virtual filesystem.

import (
	"bytes"
	"context"
	"strings"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/interp"
	goawkinterp "github.com/benhoyt/goawk/interp"
	"github.com/benhoyt/goawk/parser"
)

func init() {
	applets["awk"] = applet{"pattern scanning and processing (goawk; -F sep, -v var=val)", runAwk}
}

func runAwk(ctx context.Context, s *Shell, hc *interp.HandlerContext, args []string) int {
	fieldSep := ""
	var vars []string
	var rest []string
	for i := 0; i < len(args); i++ {
		switch {
		case args[i] == "-F" && i+1 < len(args):
			i++
			fieldSep = args[i]
		case strings.HasPrefix(args[i], "-F") && len(args[i]) > 2:
			fieldSep = args[i][2:]
		case args[i] == "-v" && i+1 < len(args):
			i++
			if name, val, ok := strings.Cut(args[i], "="); ok {
				vars = append(vars, name, val)
			}
		default:
			rest = append(rest, args[i])
		}
	}
	if len(rest) == 0 {
		fprintln(hc.Stderr, "usage: awk [-F sep] [-v var=val] 'program' [file...]")
		return 2
	}
	prog, err := parser.ParseProgram([]byte(rest[0]), nil)
	if err != nil {
		return fail(hc, "awk", err)
	}

	// read input files from the vfs (goawk would use the OS otherwise)
	var input bytes.Buffer
	config := &goawkinterp.Config{
		Output: hc.Stdout,
		Error:  hc.Stderr,
		Vars:   vars,
	}
	if fieldSep != "" {
		config.Vars = append(config.Vars, "FS", fieldSep)
	}
	if len(rest) > 1 {
		for _, f := range rest[1:] {
			data, err := afero.ReadFile(s.FS, resolveArg(hc, f))
			if err != nil {
				return fail(hc, "awk", err)
			}
			input.Write(data)
		}
		config.Stdin = &input
	} else {
		config.Stdin = hc.Stdin
	}

	status, err := goawkinterp.ExecProgram(prog, config)
	if err != nil {
		return fail(hc, "awk", err)
	}
	return status
}
