//go:build !windows
// +build !windows

// Package appserver pkg/app/appserver/stderr_unix.go c2-vis-appsvc
package appserver

func getIgnoreErrs() []string {
	ignoreErrs := []string{
		"RTNETLINK answers: File exists",
		"RTNETLINK answers: Operation not permitted",
		"Fatal: can't open lock file /run/xtables.lock: Permission denied",
		"rpc.Serve: accept:accept",
	}
	return ignoreErrs
}
