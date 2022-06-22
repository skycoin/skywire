//go:build !windows
// +build !windows

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
