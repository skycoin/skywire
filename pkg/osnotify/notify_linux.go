//go:build linux

package osnotify

import (
	"os"
	"os/exec"
)

// available requires notify-send on PATH AND evidence of a graphical session
// (an X11 or Wayland display). On a headless server / router — no display — a
// notification has nowhere to go, so we report unavailable and no-op.
func available() bool {
	if _, err := lookPath("notify-send"); err != nil {
		return false
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func send(n Notification) error {
	app := n.AppName
	if app == "" {
		app = "Skywire"
	}
	// Title/body are passed as separate argv elements (no shell), so untrusted
	// text cannot inject a command. `--` stops option parsing so a title
	// beginning with '-' isn't mistaken for a flag.
	c := exec.Command("notify-send", "--app-name="+app, "--", n.Title, n.Body) //nolint:gosec // args, not a shell line
	// Inherit the session env (DISPLAY / DBUS_SESSION_BUS_ADDRESS) so the
	// notification reaches the logged-in user's notification daemon.
	c.Env = os.Environ()
	return runCmd(c)
}
