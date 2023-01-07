//go:build !windows
// +build !windows

// Package dmsgpty pkg/dmsgpty/pty_unix.go
package dmsgpty

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

// Pty errors.
var (
	ErrPtyAlreadyRunning = errors.New("a pty session is already running")
	ErrPtyNotRunning     = errors.New("no active pty session")
)

// Pty runs a local pty.
type Pty struct {
	pty *os.File
	mx  sync.RWMutex
}

// NewPty creates a new Pty.
func NewPty() *Pty {
	return new(Pty)
}

// Stop stops the running command and closes the pty.
func (s *Pty) Stop() error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if s.pty == nil {
		return ErrPtyNotRunning
	}

	err := s.pty.Close()
	s.pty = nil
	return err
}

// Read reads any stdout or stderr outputs from the pty.
func (s *Pty) Read(b []byte) (int, error) {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.pty == nil {
		return 0, ErrPtyNotRunning
	}

	return s.pty.Read(b)
}

// Write writes to the stdin of the pty.
func (s *Pty) Write(b []byte) (int, error) {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.pty == nil {
		return 0, ErrPtyNotRunning
	}

	return s.pty.Write(b)
}

// Start runs a command with the given command name, args and optional window size.
func (s *Pty) Start(name string, args []string, size *WinSize) error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if s.pty != nil {
		return ErrPtyAlreadyRunning
	}

	cmd := exec.Command(name, args...) //nolint:gosec
	cmd.Env = os.Environ()
	var sz *pty.Winsize
	var err error

	if size == nil {
		sz = nil
	} else {
		sz = size.PtySize()
	}

	f, err := pty.StartWithSize(cmd, sz) //nolint:gosec
	if err != nil {
		return err
	}

	s.pty = f
	return nil
}

// SetPtySize sets the pty size.
func (s *Pty) SetPtySize(size *WinSize) error {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.pty == nil {
		return ErrPtyNotRunning
	}

	sz := size.PtySize()

	return pty.Setsize(s.pty, sz)
}
