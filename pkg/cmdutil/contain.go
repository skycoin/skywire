// Package cmdutil pkg/cmdutil/contain.go c1-com-util
package cmdutil

import (
	"fmt"
	"runtime/debug"
)

// Contain runs fn on the calling goroutine as an in-process run, so an Exit
// or a Fatal log line inside it returns an error here instead of ending the
// process. A panic in fn is returned as an error too.
func Contain(fn func()) (err error) {
	r := &inproc{exited: make(chan int, 1), stdin: Stdin(), stdout: Stdout(), stderr: Stderr()}
	id := goid()
	runsMu.Lock()
	runs[id] = r
	active.Add(1)
	runsMu.Unlock()
	defer func() {
		runsMu.Lock()
		delete(runs, id)
		active.Add(-1)
		forget(r)
		runsMu.Unlock()
	}()
	defer func() {
		switch p := recover().(type) {
		case nil:
			select {
			case code := <-r.exited:
				err = fmt.Errorf("exit status %d", code)
			default:
			}
		case exitCode:
			err = fmt.Errorf("exit status %d", int(p))
		default:
			err = fmt.Errorf("panic: %v\n\n%s", p, debug.Stack())
		}
	}()
	fn()
	return nil
}
