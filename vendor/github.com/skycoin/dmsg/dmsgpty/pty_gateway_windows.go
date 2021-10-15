//go:build windows
// +build windows

package dmsgpty

import (
	"errors"

	"golang.org/x/sys/windows"
)

// NewWinSize creates a new WinSize object
func NewWinSize(w *windows.Coord) (*WinSize, error) {
	if w == nil {
		return nil, errors.New("pty size is nil")
	}
	return &WinSize{
		X: uint16(w.X),
		Y: uint16(w.Y),
	}, nil
}

// PtySize returns *windows.Coord object
func (w *WinSize) PtySize() *windows.Coord {
	return &windows.Coord{
		X: int16(w.X),
		Y: int16(w.Y),
	}
}
