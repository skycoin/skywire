// Package cmdutil pkg/cmdutil/stdio.go c0-com-util
package cmdutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync/atomic"
)

// active counts the runs in progress, so a process with none pays nothing.
var active atomic.Int32

// family maps goroutines a run started to it, under runsMu. maxFamily bounds it
// for a long run that keeps starting goroutines; it is rebuilt as they print.
var family = map[uint64]*inproc{}

const maxFamily = 1 << 16

// Stdout is where the calling command writes its output: the stdout of the
// in-process run (Run) it belongs to, or the process's own.
func Stdout() io.Writer {
	if r := current(); r != nil && r.stdout != nil {
		return r.stdout
	}
	return os.Stdout
}

// Stderr is the calling command's standard error, as Stdout.
func Stderr() io.Writer {
	if r := current(); r != nil && r.stderr != nil {
		return r.stderr
	}
	return os.Stderr
}

// Stdin is the calling command's standard input, as Stdout.
func Stdin() io.Reader {
	if r := current(); r != nil && r.stdin != nil {
		return r.stdin
	}
	return os.Stdin
}

// current is the run of the calling goroutine: its own, its creator's, or the
// only one in progress. It never stops the world as Exit's search does.
func current() *inproc {
	if active.Load() == 0 {
		return nil
	}
	var buf [4096]byte
	stack := buf[:runtime.Stack(buf[:], false)]
	id := leadingID(bytes.TrimPrefix(stack, []byte("goroutine ")))
	var parent uint64
	if i := bytes.LastIndex(stack, []byte(" in goroutine ")); i >= 0 {
		parent = leadingID(stack[i+len(" in goroutine "):])
	}
	runsMu.Lock()
	defer runsMu.Unlock()
	if r := runs[id]; r != nil {
		return r
	}
	if r := family[id]; r != nil {
		return r
	}
	r := runs[parent]
	if r == nil {
		r = family[parent]
	}
	if r == nil && len(runs) == 1 {
		for _, v := range runs {
			r = v
		}
	}
	if r != nil {
		if len(family) >= maxFamily {
			family = map[uint64]*inproc{}
		}
		family[id] = r
	}
	return r
}

// forget drops a finished run's goroutines. runsMu is held.
func forget(r *inproc) {
	for id, v := range family {
		if v == r {
			delete(family, id)
		}
	}
}

// Print is fmt.Print to Stdout.
func Print(a ...any) (int, error) { return fmt.Fprint(Stdout(), a...) }

// Printf is fmt.Printf to Stdout.
func Printf(format string, a ...any) (int, error) { return fmt.Fprintf(Stdout(), format, a...) }

// Println is fmt.Println to Stdout.
func Println(a ...any) (int, error) { return fmt.Fprintln(Stdout(), a...) }

// Eprint is fmt.Fprint to Stderr.
func Eprint(a ...any) (int, error) { return fmt.Fprint(Stderr(), a...) }

// Eprintf is fmt.Fprintf to Stderr.
func Eprintf(format string, a ...any) (int, error) { return fmt.Fprintf(Stderr(), format, a...) }

// Eprintln is fmt.Fprintln to Stderr.
func Eprintln(a ...any) (int, error) { return fmt.Fprintln(Stderr(), a...) }
