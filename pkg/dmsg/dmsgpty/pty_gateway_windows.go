//go:build windows
// +build windows

// Package dmsgpty pkg/dmsgpty/pty_gateway_windows.go
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
		//nolint:gosec // Safe conversion: window coordinates are always positive
		X: uint16(w.X),
		//nolint:gosec // Safe conversion: window coordinates are always positive
		Y: uint16(w.Y),
	}, nil
}

// PtySize returns *windows.Coord object
func (w *WinSize) PtySize() *windows.Coord {
	return &windows.Coord{
		//nolint:gosec // Safe conversion: WinSize values fit in int16 range
		X: int16(w.X),
		//nolint:gosec // Safe conversion: WinSize values fit in int16 range
		Y: int16(w.Y),
	}
}
