//go:build !windows
// +build !windows

// Package dmsgpty pkg/dmsgpty/ui_unix.go
package dmsgpty

import (
	"github.com/creack/pty"
)

func (ui *UI) uiStartSize(ptyC *PtyClient) error {
	winSize, err := NewWinSize(&pty.Winsize{Rows: wsRows, Cols: wsCols})
	if err != nil {
		return err
	}
	// UI sessions use xterm-256color as they're accessed via web terminal
	env := []string{"TERM=xterm-256color"}
	return ptyC.StartWithSize(ui.conf.CmdName, ui.conf.CmdArgs, winSize, env)
}
