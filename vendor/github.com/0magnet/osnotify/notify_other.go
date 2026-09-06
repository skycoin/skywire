//go:build !darwin && !linux && !windows

package osnotify

// available is always false on platforms without a desktop notification
// backend (e.g. *BSD, plan9, wasm) — Notify no-ops with ErrUnavailable.
func available() bool { return false }

func send(Notification) error { return ErrUnavailable }
