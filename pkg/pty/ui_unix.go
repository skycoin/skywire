//go:build !windows
// +build !windows

// Package pty pkg/pty/ui_unix.go
package pty

import (
	"github.com/creack/pty"
)

// uiWinSize returns the initial pty window size and environment for a web
// terminal. The Start/StartSession/Attach decision is made in ui.go so the
// persistent-session reconnect logic stays platform-independent.
func (ui *UI) uiWinSize() (*WinSize, []string, error) {
	winSize, err := NewWinSize(&pty.Winsize{Rows: wsRows, Cols: wsCols})
	if err != nil {
		return nil, nil, err
	}
	// UI sessions use xterm-256color as they're accessed via web terminal
	return winSize, []string{"TERM=xterm-256color"}, nil
}
