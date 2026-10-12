// Package cmdutil pkg/cmdutil/inprocess.go c0-com-util
package cmdutil

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/logging"
)

// exitCode unwinds an in-process run to Run, which returns it.
type exitCode int

// inproc is one Run. exited takes the code of an Exit from a goroutine the
// command started, which cannot be unwound to Run.
type inproc struct {
	exited chan int
}

var (
	// startMu is held from a run's start until cobra has parsed its flags,
	// since the args and flag values live on the shared command tree.
	startMu sync.Mutex
	holder  atomic.Pointer[inproc]

	runsMu sync.Mutex
	runs   = map[uint64]*inproc{}
)

func init() {
	cobra.OnInitialize(func() {
		if holder.Swap(nil) != nil {
			startMu.Unlock()
		}
	})
	logrus.StandardLogger().ExitFunc = Exit
	logging.ExitFunc = Exit
}

// Run executes root with args in this process, on the given stdio, and returns
// the exit status a separate process of the binary would have had.
//
// Runs may overlap, as a visor and a cli command do in one shell: each sets its
// stdio on the command it runs, not the root, and resets only that path's flags.
func Run(ctx context.Context, root *cobra.Command, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	r := &inproc{exited: make(chan int, 1)}
	startMu.Lock()
	holder.Store(r)
	defer func() {
		if holder.CompareAndSwap(r, nil) {
			startMu.Unlock()
		}
	}()

	leaf, _, err := root.Find(args)
	if err != nil || leaf == nil {
		leaf = root
	}
	for c := leaf; c != nil; c = c.Parent() {
		c.Flags().VisitAll(resetFlag)
		c.PersistentFlags().VisitAll(resetFlag)
	}
	leaf.SetIn(stdin)
	leaf.SetOut(stdout)
	leaf.SetErr(stderr)
	defer func() {
		leaf.SetIn(nil)
		leaf.SetOut(nil)
		leaf.SetErr(nil)
	}()
	root.SetArgs(args)

	done := make(chan int, 1)
	go func() {
		id := goid()
		runsMu.Lock()
		runs[id] = r
		runsMu.Unlock()
		defer func() {
			runsMu.Lock()
			delete(runs, id)
			runsMu.Unlock()
		}()
		done <- execute(ctx, root, stderr)
	}()
	select {
	case code := <-done:
		return code
	case code := <-r.exited:
		return code
	}
}

func execute(ctx context.Context, root *cobra.Command, stderr io.Writer) (code int) {
	defer func() {
		switch p := recover().(type) {
		case nil:
		case exitCode:
			code = int(p)
		default:
			// A crash ends the run as Go ends a process, not the host.
			fmt.Fprintf(stderr, "panic: %v\n\n%s", p, debug.Stack()) //nolint:errcheck
			code = 2
		}
	}()
	if err := root.ExecuteContext(ctx); err != nil {
		// RunRoot reports what a root that silences cobra does not.
		if root.SilenceErrors {
			fmt.Fprintln(stderr, "Failed to execute command:", err) //nolint:errcheck
		}
		return 1
	}
	return 0
}

// Exit ends the command with code: the process, or only the in-process run
// (Run) the calling goroutine belongs to.
func Exit(code int) {
	id := goid()
	runsMu.Lock()
	_, own := runs[id]
	n := len(runs)
	runsMu.Unlock()
	if own {
		panic(exitCode(code))
	}
	if n > 0 {
		if r := ancestorRun(id); r != nil {
			select {
			case r.exited <- code:
			default:
			}
		}
		// A goroutine that is not the run's own cannot be unwound to Run.
		runtime.Goexit()
	}
	os.Exit(code)
}

// ancestorRun is the run of the nearest ancestor of goroutine id that is a
// run's own, found through each stack's "created by … in goroutine N".
func ancestorRun(id uint64) *inproc {
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	parents := map[uint64]uint64{}
	for _, g := range bytes.Split(buf, []byte("\n\n")) {
		gid := leadingID(bytes.TrimPrefix(g, []byte("goroutine ")))
		if i := bytes.LastIndex(g, []byte(" in goroutine ")); i >= 0 {
			parents[gid] = leadingID(g[i+len(" in goroutine "):])
		}
	}
	runsMu.Lock()
	defer runsMu.Unlock()
	for i := 0; i < 64 && id != 0; i++ {
		if r, ok := runs[id]; ok {
			return r
		}
		id = parents[id]
	}
	if len(runs) == 1 {
		for _, r := range runs {
			return r
		}
	}
	return nil
}

// leadingID parses the number b starts with.
func leadingID(b []byte) uint64 {
	end := 0
	for end < len(b) && b[end] >= '0' && b[end] <= '9' {
		end++
	}
	id, _ := strconv.ParseUint(string(b[:end]), 10, 64) //nolint:errcheck
	return id
}

// Fatal is log.Fatal through Exit.
func Fatal(v ...any) {
	log.Output(2, fmt.Sprint(v...)) //nolint:errcheck,gosec // as log.Fatal does
	Exit(1)
}

// Fatalf is log.Fatalf through Exit.
func Fatalf(format string, v ...any) {
	log.Output(2, fmt.Sprintf(format, v...)) //nolint:errcheck,gosec // as log.Fatal does
	Exit(1)
}

// Fatalln is log.Fatalln through Exit.
func Fatalln(v ...any) {
	log.Output(2, fmt.Sprintln(v...)) //nolint:errcheck,gosec // as log.Fatal does
	Exit(1)
}

// resetFlag puts a flag a previous run set back to its default.
func resetFlag(f *pflag.Flag) {
	if !f.Changed {
		return
	}
	if sv, ok := f.Value.(pflag.SliceValue); ok {
		var def []string
		if s := strings.Trim(f.DefValue, "[]"); s != "" {
			def = strings.Split(s, ",")
		}
		_ = sv.Replace(def) //nolint:errcheck
	} else {
		_ = f.Value.Set(f.DefValue) //nolint:errcheck
	}
	f.Changed = false
}

// goid is the calling goroutine's id, from the first line runtime.Stack writes.
func goid() uint64 {
	var buf [64]byte
	return leadingID(bytes.TrimPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine ")))
}
