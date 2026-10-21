//go:build android && cgo

package commands

import (
	"github.com/0magnet/gio/app"
	_ "github.com/0magnet/gio/app/permission/foreground" // declares the service in the APK
)

// Android stops an app soon after it leaves the screen, and a visor or shell
// in it with it, unless a foreground service holds the process.
func init() {
	keepRunning = func() {
		_ = app.StartForeground("Skywire", "skywire sh is running") //nolint:errcheck // without it the app still runs while in front
	}
}
