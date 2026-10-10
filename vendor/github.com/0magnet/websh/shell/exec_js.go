//go:build js && wasm

package shell

import (
	"context"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"syscall/js"
	"time"

	"github.com/0magnet/bottle/proc"
	"github.com/0magnet/sh/v3/expand"
	"github.com/0magnet/sh/v3/interp"

	"github.com/0magnet/websh/widget"
)

// resizePoll is how often a child's terminal size is checked, as ssh does.
const resizePoll = 250 * time.Millisecond

// execExternal runs a program from the filesystem as a child wasm process via
// bottle's proc layer (globalThis.proc): a compiled binary — a Go toolchain,
// a TUI — on the PATH, built by Go or by TinyGo.
//
// It runs as a program in a terminal does. Its output reaches the terminal as
// it is written. What is typed reaches its stdin, and a program waiting for
// keys waits. Ctrl+C (cooked) stops it. When its stdout is this terminal it
// has a terminal of its own (see bottle's proc.TTY, and childtty): the size,
// resizes, and raw mode, in which every key is its own. It is told
// WEBSH_PLACEMENTS=1 then, and may lay widgets it offers (package widget)
// over its cells; they are withdrawn when it exits.
//
// The program is read from this shell's filesystem, which need not be the
// page's jsfs, and compiled once for as long as the file stays the same.
func (s *Shell) execExternal(ctx context.Context, args []string) (int, bool) {
	if !js.Global().Get("proc").Truthy() || !js.Global().Get("fs").Truthy() {
		return 0, false // no process/filesystem layer on this page
	}
	hc := interp.HandlerCtx(ctx)
	bin := s.lookPath(args[0], hc.Dir, envGet(hc.Env, "PATH"))
	if bin == "" {
		return 0, false // not on the PATH: let the caller say "not found"
	}

	c := &proc.Cmd{Path: bin, Args: args, Dir: hc.Dir, Stdout: hc.Stdout, Stderr: hc.Stderr}
	fi, err := s.FS.Stat(bin)
	if err != nil {
		Printf(hc.Stderr, "%s: %v\n", args[0], err)
		return 126, true
	}
	// argv[0] keys the compiled program, and the stamp tells this file from
	// the next one written there.
	// Only a wasm module is a program here; a text file on the PATH, or a
	// path typed at the prompt, is not run (as bash will not run a file
	// without its execute bit) but said to be what it is.
	if !isWasm(s, bin) {
		Printf(hc.Stderr, "%s: cannot execute: not a wasm program\n", args[0])
		return 126, true
	}
	c.Stamp = fmt.Sprintf("%s:%d:%d", bin, fi.Size(), fi.ModTime().UnixNano())
	if !proc.Cached(args[0], c.Stamp) {
		if c.Program, err = readAll(s, bin); err != nil {
			Printf(hc.Stderr, "%s: %v\n", args[0], err)
			return 126, true
		}
	}

	tty := s.Size != nil && s.IsTerminal != nil && s.IsTerminal(hc.Stdout)
	hc.Env.Each(func(name string, vr expand.Variable) bool {
		if vr.IsSet() {
			c.Env = append(c.Env, name+"="+vr.String())
		}
		return true
	})
	raw := false
	if tty {
		c.Env = append(c.Env, "WEBSH_PLACEMENTS=1")
		cols, rows := s.Size()
		c.TTY = &proc.TTY{Cols: cols, Rows: rows, OnRaw: func(on bool) {
			raw = on
			if s.RawMode != nil {
				s.RawMode(on)
			}
		}}
	}
	in, err := c.StdinPipe()
	if err != nil {
		Printf(hc.Stderr, "%s: %v\n", args[0], err)
		return 126, true
	}
	p, err := c.Start()
	if err != nil {
		Printf(hc.Stderr, "%s: %v\n", args[0], err)
		return 126, true
	}
	defer s.WithSource("local")()

	done := make(chan struct{})
	var code int
	go func() {
		code, err = p.Wait()
		close(done)
		// A key read for it after it is gone is the shell's again: wake the
		// read below rather than leave it waiting for one.
		if s.WakeStdin != nil {
			s.WakeStdin()
		}
	}()
	go func() {
		t := time.NewTicker(resizePoll)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				p.Kill(false)
				return
			case <-t.C:
				if tty {
					p.Resize(s.Size())
				}
			}
		}
	}()
	feed(hc.Stdin, in, done)
	<-done

	widget.Drop(p.ID)
	if raw && s.RawMode != nil {
		s.RawMode(false) // it left the terminal raw: killed, or it forgot
	}
	if err != nil {
		Printf(hc.Stderr, "%s: %v\n", args[0], err)
		return 126, true
	}
	return code, true
}

// feed copies what is typed (or piped) into a child's stdin until the child
// exits or the input ends. It reads on the caller's goroutine, so no reader is
// left behind to take the shell's next key.
func feed(r io.Reader, w io.WriteCloser, done <-chan struct{}) {
	defer w.Close() //nolint:errcheck // the end of its input either way
	if r == nil {
		return
	}
	buf := make([]byte, 4096)
	for {
		select {
		case <-done:
			return
		default:
		}
		n, err := r.Read(buf)
		select {
		case <-done:
			return
		default:
		}
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// readAll reads a program from the shell's filesystem.
func readAll(s *Shell, name string) ([]byte, error) {
	f, err := s.FS.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close() //nolint:errcheck // read-only
	return io.ReadAll(f)
}

// lookPath resolves name against the shell's filesystem: a path with a slash
// as given (relative to cwd), a bare name walked down PATH. It returns the
// first match that exists and is not a directory.
func (s *Shell) lookPath(name, cwd, pathEnv string) string {
	try := func(p string) string {
		if fi, err := s.FS.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
		return ""
	}
	if strings.Contains(name, "/") {
		if !path.IsAbs(name) {
			name = path.Join(cwd, name)
		}
		return try(name)
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			continue
		}
		if hit := try(path.Join(dir, name)); hit != "" {
			return hit
		}
	}
	return ""
}

func envGet(env expand.Environ, name string) string {
	if v := env.Get(name); v.IsSet() {
		return v.String()
	}
	return ""
}

// isWasm reports whether the file at name starts as a wasm module does.
func isWasm(s *Shell, name string) bool {
	f, err := s.FS.Open(name)
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck // read-only
	head := make([]byte, 4)
	n, err := io.ReadFull(f, head)
	return err == nil && n == 4 && string(head) == "\x00asm"
}
