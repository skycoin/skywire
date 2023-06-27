//go:build !windows
// +build !windows

// Package dmsgpty pkg/dmsgpty/cli_unix.go
package dmsgpty

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/creack/pty"
	"golang.org/x/term"
)

// ptyResizeLoop informs the remote of changes to the local CLI terminal window size.
func ptyResizeLoop(ctx context.Context, ptyC *PtyClient) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
			winSize, err := getPtySize(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to obtain window size: %v", err)
			}
			ws, err := NewWinSize(winSize)
			if err != nil {
				return fmt.Errorf("failed to convert pty size to WinSize: %v", err)
			}
			if err := ptyC.SetPtySize(ws); err != nil {
				return fmt.Errorf("failed to set remote window size: %v", err)
			}
		}
	}
}

// getPtySize obtains the size of the local terminal.
func getPtySize(t *os.File) (*pty.Winsize, error) {
	return pty.GetsizeFull(t)
}

// prepareStdin sets stdin to raw mode and provides a function to restore the original state.
func (cli *CLI) prepareStdin() (restore func(), err error) {
	var oldState *term.State
	if oldState, err = term.MakeRaw(int(os.Stdin.Fd())); err != nil {
		cli.Log.
			WithError(err).
			Warn("Failed to set stdin to raw mode.")
		return
	}
	restore = func() {
		// Attempt to restore state.
		if err = term.Restore(int(os.Stdin.Fd()), oldState); err != nil {
			cli.Log.
				WithError(err).
				Error("Failed to restore original stdin state.")
		}
	}
	return
}
