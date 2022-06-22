//go:build windows
// +build windows

package appserver

func getIgnoreErrs() []string {
	ignoreErrs := []string{
		"Creating adapter",
		"Using existing driver 0.14",
		"rpc.Serve: accept:accept",
		"The route addition failed: The object already exists.",
	}
	return ignoreErrs
}
