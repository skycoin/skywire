//go:build windows
// +build windows

// Package dmsgpty pkg/dmsgpty/pty_client_windows.go
package dmsgpty

import (
	"errors"

	"golang.org/x/sys/windows"
)

// getSize gets windows terminal size
func getSize() (*WinSize, error) {
	var bufInfo windows.ConsoleScreenBufferInfo
	c, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE)
	if err != nil {
		return nil, err
	}
	if err = windows.GetConsoleScreenBufferInfo(c, &bufInfo); err != nil {
		if errors.Is(err, windows.ERROR_INVALID_HANDLE) {
			return &WinSize{
				X: 80,
				Y: 30,
			}, nil
		}
		return nil, err
	}
	return NewWinSize(&windows.Coord{
		X: bufInfo.Window.Right - bufInfo.Window.Left + 1,
		Y: bufInfo.Window.Bottom - bufInfo.Window.Top + 1,
	})
}
