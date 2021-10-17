//go:build !windows
// +build !windows

package dmsgpty

import (
	"github.com/creack/pty"
)

func (ui *UI) uiStartSize(ptyC *PtyClient) error {
	winSize, err := NewWinSize(&pty.Winsize{Rows: wsRows, Cols: wsCols})
	if err != nil {
		return err
	}
	return ptyC.StartWithSize(ui.conf.CmdName, ui.conf.CmdArgs, winSize)
}
