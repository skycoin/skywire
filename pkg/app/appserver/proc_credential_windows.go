//go:build windows
// +build windows

// Package appserver pkg/app/appserver/proc_credential_windows.go
//
// POSIX-style setuid before exec doesn't translate to Windows. The
// closest Windows equivalent is CreateProcessAsUser, which needs a
// LogonUser-issued token and a configured-secret-or-PSExec dance —
// out of scope for the visor's app launcher. Document the limitation
// and accept that User/Group fields are silently no-ops on Windows.
package appserver

import (
	"fmt"
	"os/exec"
)

// applyProcCredentials is a no-op on Windows. Returns an explicit
// error when the operator set User/Group anyway, so the configuration
// surface fails loudly rather than silently running as the wrong UID.
func applyProcCredentials(_ *exec.Cmd, username, _ string) error {
	if username == "" {
		return nil
	}
	return fmt.Errorf("AppConfig.User is not supported on Windows; remove the field or run on a POSIX host")
}
