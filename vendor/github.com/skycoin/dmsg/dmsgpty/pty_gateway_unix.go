//go:build !windows
// +build !windows

package dmsgpty

import (
	"errors"

	"github.com/creack/pty"
)

// NewWinSize creates a new WinSize wrapper object
func NewWinSize(w *pty.Winsize) (*WinSize, error) {
	if w == nil {
		return nil, errors.New("pty size cannot be nil")
	}
	return &WinSize{
		X:    w.X,
		Y:    w.Y,
		Rows: w.Rows,
		Cols: w.Cols,
	}, nil
}

// PtySize returns *pty.Winsize
func (w *WinSize) PtySize() *pty.Winsize {
	return &pty.Winsize{
		Rows: w.Rows,
		Cols: w.Cols,
		X:    w.X,
		Y:    w.Y,
	}
}
