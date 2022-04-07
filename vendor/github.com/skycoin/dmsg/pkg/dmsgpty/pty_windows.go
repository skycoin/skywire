//go:build windows
// +build windows

package dmsgpty

import (
	"errors"
	"os"
	"sync"
	"syscall"

	"github.com/ActiveState/termtest/conpty"
)

// Pty errors.
var (
	ErrPtyAlreadyRunning = errors.New("a pty session is already running")
	ErrPtyNotRunning     = errors.New("no active pty session")
)

// Pty runs a local pty.
type Pty struct {
	pty *conpty.ConPty
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

	return s.pty.OutPipe().Read(b)
}

// Write writes to the stdin of the pty.
func (s *Pty) Write(b []byte) (int, error) {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.pty == nil {
		return 0, ErrPtyNotRunning
	}

	res, err := s.pty.Write(b)
	return int(res), err
}

// Start runs a command with the given command name, args and optional window size.
func (s *Pty) Start(name string, args []string, size *WinSize) error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if s.pty != nil {
		return ErrPtyAlreadyRunning
	}

	var err error

	if size == nil {
		size, err = getSize()
		if err != nil {
			return err
		}

	}
	pty, err := conpty.New(
		int16(size.X), int16(size.Y),
	)
	if err != nil {
		return err
	}

	_, _, err = pty.Spawn(
		name,
		args,
		&syscall.ProcAttr{
			Env: os.Environ(),
		},
	)

	if err != nil {
		return err
	}

	s.pty = pty
	return nil
}

// SetPtySize sets the pty size.
func (s *Pty) SetPtySize(size *WinSize) error {
	s.mx.RLock()
	defer s.mx.RUnlock()

	if s.pty == nil {
		return ErrPtyNotRunning
	}

	return s.pty.Resize(size.X, size.Y)
}
