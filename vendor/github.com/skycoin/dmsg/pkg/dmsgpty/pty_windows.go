//go:build windows
// +build windows

// Package dmsgpty pkg/dmsgpty/pty_windows.go
package dmsgpty

import (
	"errors"
	"os"
	"strings"
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

// Start runs a command with the given command name, args, optional window size, and optional environment variables.
// If env is provided, those variables will be merged with (and override) the host's environment.
func (s *Pty) Start(name string, args []string, size *WinSize, env []string) error {
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
		//nolint:gosec // Safe conversion: WinSize values fit in int16 range
		int16(size.X), int16(size.Y),
	)
	if err != nil {
		return err
	}

	_, _, err = pty.Spawn(
		name,
		args,
		&syscall.ProcAttr{
			Env: mergeEnv(os.Environ(), env),
		},
	)

	if err != nil {
		return err
	}

	s.pty = pty
	return nil
}

// mergeEnv merges the base environment with override variables.
// Variables in override will replace any matching variables in base.
func mergeEnv(base, override []string) []string {
	if len(override) == 0 {
		return base
	}

	// Build a map of override variables for quick lookup
	overrideMap := make(map[string]string)
	for _, e := range override {
		if idx := strings.Index(e, "="); idx > 0 {
			overrideMap[e[:idx]] = e
		}
	}

	// Filter base, keeping only variables not in override
	result := make([]string, 0, len(base)+len(override))
	for _, e := range base {
		if idx := strings.Index(e, "="); idx > 0 {
			key := e[:idx]
			if _, exists := overrideMap[key]; !exists {
				result = append(result, e)
			}
		}
	}

	// Add all override variables
	result = append(result, override...)

	return result
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
