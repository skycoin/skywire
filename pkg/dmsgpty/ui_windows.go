//go:build windows
// +build windows

// Package dmsgpty pkg/dmsgpty/ui_windows.go
package dmsgpty

import "golang.org/x/sys/windows"

func (ui *UI) uiStartSize(ptyC *PtyClient) error {
	ws, err := NewWinSize(&windows.Coord{
		X: wsCols,
		Y: wsRows,
	})
	if err != nil {
		return err
	}
	return ptyC.StartWithSize(ui.conf.CmdName, ui.conf.CmdArgs, ws)
}
