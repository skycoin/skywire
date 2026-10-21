// SPDX-License-Identifier: Unlicense OR MIT

package app

// StartForeground keeps the program running while its window is not in front.
// On Android that takes a foreground service, which shows a notification of
// title and text that opens the app, and the app must import package
// github.com/0magnet/gio/app/permission/foreground. Elsewhere a program keeps
// running anyway, and it does nothing.
func StartForeground(title, text string) error { return startForeground(title, text) }

// StopForeground ends what StartForeground started.
func StopForeground() { stopForeground() }
