//go:build !windows
// +build !windows

// Package dmsgpty pkg/dmsgpty/pty_unix.go
package dmsgpty

import (
	"errors"
	"os"
	"os/exec"
	"strings"
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
	cmd *exec.Cmd
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
	// Reap the child process to avoid zombies.
	if s.cmd != nil {
		_ = s.cmd.Wait() //nolint:errcheck
		s.cmd = nil
	}
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

// Start runs a command with the given command name, args, optional window size, and optional environment variables.
// If env is provided, those variables will be merged with (and override) the host's environment.
func (s *Pty) Start(name string, args []string, size *WinSize, env []string) error {
	s.mx.Lock()
	defer s.mx.Unlock()

	if s.pty != nil {
		return ErrPtyAlreadyRunning
	}

	cmd := exec.Command(name, args...) //nolint:gosec
	cmd.Env = mergeEnv(os.Environ(), env)
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
	s.cmd = cmd
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

	sz := size.PtySize()

	return pty.Setsize(s.pty, sz)
}
