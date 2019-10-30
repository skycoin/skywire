package pty

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

// GatewayName is the universal RPC gateway name.
const GatewayName = "DirectGateway"

// Gateway represents a pty gateway.
type Gateway interface {
	Start(req *CommandReq, _ *struct{}) error
	Stop(_, _ *struct{}) error
	Read(reqN *int, respB *[]byte) error
	Write(reqB *[]byte, respN *int) error
	SetPtySize(size *pty.Winsize, _ *struct{}) error
}

// DirectGateway is the gateway to a local pty.
type DirectGateway struct {
	pty *os.File
	mx  sync.RWMutex
}

// NewDirectGateway creates a new gateway to a local pty.
func NewDirectGateway() Gateway {
	return new(DirectGateway)
}

// CommandReq represents a pty command.
type CommandReq struct {
	Name string
	Arg  []string
	Size *pty.Winsize
}

// Start starts the local pty.
func (g *DirectGateway) Start(req *CommandReq, _ *struct{}) error {
	g.mx.Lock()
	defer g.mx.Unlock()

	if g.pty != nil {
		return ErrPtyAlreadyRunning
	}

	f, err := pty.StartWithSize(exec.Command(req.Name, req.Arg...), req.Size) //nolint:gosec
	if err != nil {
		return err
	}

	g.pty = f
	return nil
}

// Stop stops the local pty.
func (g *DirectGateway) Stop(_, _ *struct{}) error {
	g.mx.Lock()
	defer g.mx.Unlock()

	if g.pty == nil {
		return ErrPtyNotRunning
	}

	err := g.pty.Close()
	g.pty = nil
	return err
}

// Read reads from the local pty.
func (g *DirectGateway) Read(reqN *int, respB *[]byte) error {
	return ptyReadLock(g, func() error {
		b := make([]byte, *reqN)
		n, err := g.pty.Read(b)
		*respB = b[:n]
		return err
	})
}

// Write writes to the local pty.
func (g *DirectGateway) Write(wb *[]byte, n *int) error {
	return ptyReadLock(g, func() (err error) {
		*n, err = g.pty.Write(*wb)
		return
	})
}

// SetPtySize sets the local pty's window size.
func (g *DirectGateway) SetPtySize(size *pty.Winsize, _ *struct{}) error {
	return ptyReadLock(g, func() error {
		return pty.Setsize(g.pty, size)
	})
}

func ptyReadLock(g *DirectGateway, fn func() error) error {
	g.mx.RLock()
	defer g.mx.RUnlock()
	if g.pty == nil {
		return ErrPtyNotRunning
	}
	return fn()
}

// ProxyGateway is an RPC gateway for a remote pty.
type ProxyGateway struct {
	ptyC *Client
}

// NewProxyGateway creates a new pty-proxy gateway
func NewProxyGateway(ptyC *Client) Gateway {
	return &ProxyGateway{ptyC: ptyC}
}

// Start starts the remote pty.
func (g *ProxyGateway) Start(req *CommandReq, _ *struct{}) error {
	return g.ptyC.Start(req.Name, req.Arg...)
}

// Stop stops the remote pty.
func (g *ProxyGateway) Stop(_, _ *struct{}) error {
	return g.ptyC.Stop()
}

// Read reads from the remote pty.
func (g *ProxyGateway) Read(reqN *int, respB *[]byte) error {
	b := make([]byte, *reqN)
	n, err := g.ptyC.Read(b)
	*respB = b[:n]
	return err
}

// Write writes to the remote pty.
func (g *ProxyGateway) Write(reqB *[]byte, respN *int) error {
	var err error
	*respN, err = g.ptyC.Write(*reqB)
	return err
}

// SetPtySize sets the remote pty's window size.
func (g *ProxyGateway) SetPtySize(size *pty.Winsize, _ *struct{}) error {
	return g.ptyC.SetPtySize(size)
}
