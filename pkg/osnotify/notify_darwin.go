//go:build darwin

package osnotify

import (
	"os"
	"os/exec"
)

// available reports whether osascript (always present on macOS) is on PATH.
// A missing GUI session is not cheaply detectable here; if there is none the
// osascript run simply errors and Notify surfaces that (callers ignore it).
func available() bool {
	_, err := lookPath("osascript")
	return err == nil
}

// osaScript is a FIXED AppleScript that reads the title/body from environment
// variables via `system attribute`. The untrusted text therefore never enters
// the script source, so it cannot break out of the string literal or inject
// AppleScript — it is read at runtime as opaque data.
const osaScript = `display notification (system attribute "OSN_BODY") with title (system attribute "OSN_TITLE")`

func send(n Notification) error {
	c := exec.Command("osascript", "-e", osaScript) //nolint:gosec // fixed script; data via env
	// Inherit the environment so osascript runs within the user's Aqua session,
	// then add the notification fields as data.
	c.Env = append(os.Environ(),
		"OSN_TITLE="+n.Title,
		"OSN_BODY="+n.Body,
	)
	return runCmd(c)
}
