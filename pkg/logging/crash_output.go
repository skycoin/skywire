// Package logging pkg/logging/crash_output.go c0-com-log
package logging

import (
	"os"
	"runtime/debug"
)

// CrashOutputTo mirrors the traceback of an unrecovered panic or fatal error
// to f as well as stderr, with every goroutine's stack in it.
//
// The traceback level is raised to "all" first. A binary with file
// capabilities (the dev loop's setcap cap_net_admin) runs with AT_SECURE, and
// the Go runtime then forces GOTRACEBACK=none (runtime/security_unix.go
// secureEnv), so a crash printed only the panic line and the signal line.
// SetTraceback can only raise the level, so a GOTRACEBACK=crash or system the
// operator set still applies.
func CrashOutputTo(f *os.File) error {
	debug.SetTraceback("all")
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}
