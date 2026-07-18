//go:build windows
// +build windows

// Package appserver pkg/app/appserver/stderr_windows.go c2-vis-appsvc
package appserver

func getIgnoreErrs() []string {
	ignoreErrs := []string{
		"Creating adapter",
		"Using existing driver",
		"rpc.Serve: accept:accept",
		"The route addition failed: The object already exists.",
	}
	return ignoreErrs
}
