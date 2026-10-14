//go:build !(js && wasm)

package shell

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/0magnet/afero"
	"github.com/0magnet/sh/v3/expand"
	"github.com/0magnet/sh/v3/interp"
	"github.com/0magnet/sh/v3/syntax"
)

// execExternal is a no-op off js/wasm: websh is a self-contained shell there,
// with no process layer to exec into. Returns handled=false so the caller
// reports "command not found". A shell on the OS (NewOS) runs programs
// instead; see execProgram.
func (s *Shell) execExternal(ctx context.Context, args []string) (code int, handled bool) {
	return 0, false
}

// NewOS creates a shell on this machine rather than over a filesystem of its
// own: the files are the OS's, the environment and working directory are the
// process's, and a command that is not an applet or the embedder's runs as a
// program from the PATH, as in any other shell.
//
// The applets still come first, so websh on the desktop behaves as websh in a
// page does. A program wanted instead of an applet of the same name is run by
// its path (/bin/ls).
//
// extraEnv overrides or adds to the environment. SHELL is set to this
// executable, so a program that starts "the user's shell" starts websh.
func NewOS(stdin io.Reader, stdout, stderr io.Writer, extraEnv ...string) (*Shell, error) {
	s := &Shell{
		FS:     afero.NewOsFs(),
		parser: syntax.NewParser(),
		native: true,
	}
	env := os.Environ()
	if exe, err := os.Executable(); err == nil {
		env = append(env, "SHELL="+exe)
	}
	// No file handlers: the interpreter's own are the OS's.
	if err := s.start(append(env, extraEnv...), stdin, stdout, stderr); err != nil {
		return nil, err
	}
	if wd, err := os.Getwd(); err == nil {
		s.Runner.Dir = wd
	}
	return s, nil
}

// killTimeout is how long a program has after an interrupt before it is
// killed, as the interpreter's own default.
const killTimeout = 2 * time.Second

// execProgram runs args as a program from the PATH.
//
// When its input is the terminal (Shell.TTY) it is given the terminal, made
// blocking again for as long as it runs: TTY is non-blocking so the shell's
// own reads of it can be ended, and a program handed a non-blocking terminal
// mostly fails on its first read. Any other input goes to the interpreter's
// own exec, which also runs a file the kernel will not (a script without #!).
func (s *Shell) execProgram(ctx context.Context, args []string, next interp.ExecHandlerFunc) error {
	hc := interp.HandlerCtx(ctx)
	path, err := interp.LookPathDir(hc.Dir, hc.Env, args[0])
	if err != nil {
		fprintf(hc.Stderr, "websh: %s: command not found\n", args[0])
		return interp.ExitStatus(127)
	}
	if s.TTY == nil || hc.Stdin != io.Reader(s.TTY) {
		return next(ctx, args)
	}
	in, err := blockingDup(s.TTY)
	if err != nil {
		return next(ctx, args)
	}
	// Non-blocking again on the way out, through the dup: the flag is shared.
	defer func() {
		restoreNonblock(in)
		in.Close() //nolint:errcheck,gosec // a dup of the terminal
	}()

	cmd := exec.CommandContext(ctx, path) //nolint:gosec // running what was typed is the shell's job
	cmd.Args = args
	cmd.Env = execEnv(hc.Env)
	cmd.Dir = hc.Dir
	cmd.Stdin = in
	cmd.Stdout = hc.Stdout
	cmd.Stderr = hc.Stderr
	cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
	cmd.WaitDelay = killTimeout
	err = cmd.Start()
	if errors.Is(err, syscall.ENOEXEC) {
		return next(ctx, args)
	}
	if err == nil {
		err = cmd.Wait()
	}
	var ee *exec.ExitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &ee):
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return interp.ExitStatus(128 + uint8(ws.Signal()&0x7f)) //nolint:gosec // masked to a signal number
		}
		code := ee.ExitCode()
		if code < 0 || code > 255 {
			code = 1
		}
		return interp.ExitStatus(code) //nolint:gosec // in range: checked above
	default:
		fprintf(hc.Stderr, "websh: %s: %v\n", args[0], err)
		return interp.ExitStatus(126)
	}
}

// execEnv is a program's environment: the shell's exported variables. It is
// the interpreter's own (unexported) rule, including that a variable unset in
// the shell hides the one it inherited.
func execEnv(env expand.Environ) []string {
	list := make([]string, 0, 64)
	for name, vr := range env.Each {
		if !vr.IsSet() && !vr.Local {
			for i, kv := range list {
				if strings.HasPrefix(kv, name+"=") {
					list[i] = ""
				}
			}
		}
		if vr.IsSet() && vr.Exported && vr.Kind == expand.String {
			list = append(list, name+"="+vr.String())
		}
	}
	return list
}
