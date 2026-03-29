// Package dmsgpty pkg/dmsgpty/pty_gateway.go
package dmsgpty

import "fmt"

// WinSize wraps around pty.Winsize and *windows.Coord
type WinSize struct {
	X    uint16
	Y    uint16
	Rows uint16
	Cols uint16
}

// PtyGateway represents a pty gateway, hosted by the pty.SessionServer
type PtyGateway interface {
	Start(req *CommandReq, _ *struct{}) error
	Stop(_, _ *struct{}) error
	Read(reqN *int, respB *[]byte) error
	Write(reqB *[]byte, respN *int) error
	SetPtySize(size *WinSize, _ *struct{}) error
}

// CommandReq represents a pty command.
type CommandReq struct {
	Name string
	Arg  []string
	Size *WinSize
	Env  []string // Environment variables to pass to the PTY (e.g., TERM=xterm-256color)
}

// LocalPtyGateway is the gateway to a local pty.
type LocalPtyGateway struct {
	ses *Pty
}

// NewPtyGateway creates a new gateway to a local pty.
func NewPtyGateway(ses *Pty) PtyGateway {
	return &LocalPtyGateway{ses: ses}
}

// Stop stops the local pty.
func (g *LocalPtyGateway) Stop(_, _ *struct{}) error {
	return g.ses.Stop()
}

// maxPtyReadSize is the maximum bytes that can be requested in a single PTY read.
const maxPtyReadSize = 64 * 1024 // 64KB

// Read reads from the local pty.
func (g *LocalPtyGateway) Read(reqN *int, respB *[]byte) error {
	n := *reqN
	if n <= 0 {
		return fmt.Errorf("invalid read size: %d", n)
	}
	if n > maxPtyReadSize {
		n = maxPtyReadSize
	}
	b := make([]byte, n)
	nr, err := g.ses.Read(b)
	*respB = b[:nr]
	return err
}

// Start starts the local pty.
func (g *LocalPtyGateway) Start(req *CommandReq, _ *struct{}) error {
	return g.ses.Start(req.Name, req.Arg, req.Size, req.Env)
}

// Write writes to the local pty.
func (g *LocalPtyGateway) Write(wb *[]byte, n *int) error {
	var err error
	*n, err = g.ses.Write(*wb)
	return err
}

// SetPtySize sets the local pty's window size.
func (g *LocalPtyGateway) SetPtySize(size *WinSize, _ *struct{}) error {
	return g.ses.SetPtySize(size)
}

// SetPtySize sets the remote pty's window size.
func (g *ProxiedPtyGateway) SetPtySize(size *WinSize, _ *struct{}) error {
	return g.ptyC.SetPtySize(size)
}

// ProxiedPtyGateway is an RPC gateway for a remote pty.
type ProxiedPtyGateway struct {
	ptyC *PtyClient
}

// NewProxyGateway creates a new pty-proxy gateway
func NewProxyGateway(ptyC *PtyClient) PtyGateway {
	return &ProxiedPtyGateway{ptyC: ptyC}
}

// Start starts the remote pty.
func (g *ProxiedPtyGateway) Start(req *CommandReq, _ *struct{}) error {
	return g.ptyC.Start(req.Name, req.Env, req.Arg...)
}

// Stop stops the remote pty.
func (g *ProxiedPtyGateway) Stop(_, _ *struct{}) error {
	return g.ptyC.Stop()
}

// Read reads from the remote pty.
func (g *ProxiedPtyGateway) Read(reqN *int, respB *[]byte) error {
	n := *reqN
	if n <= 0 {
		return fmt.Errorf("invalid read size: %d", n)
	}
	if n > maxPtyReadSize {
		n = maxPtyReadSize
	}
	b := make([]byte, n)
	nr, err := g.ptyC.Read(b)
	*respB = b[:nr]
	return err
}

// Write writes to the remote pty.
func (g *ProxiedPtyGateway) Write(reqB *[]byte, respN *int) error {
	var err error
	*respN, err = g.ptyC.Write(*reqB)
	return err
}
