package pty

import (
	"errors"
	"os"
	"os/exec"
	"sync"

	"github.com/creack/pty"
)

var (
	ErrPtyAlreadyRunning = errors.New("a pty session is already running")
	ErrPtyNotRunning     = errors.New("no active pty session")
)

const GatewayName = "DirectGateway"

type Gateway interface {
	Start(req *CommandReq, _ *struct{}) error
	Stop(_, _ *struct{}) error
	Read(reqN *int, respB *[]byte) error
	Write(reqB *[]byte, respN *int) error
	SetPtySize(size *pty.Winsize, _ *struct{}) error
}

type DirectGateway struct {
	pty *os.File
	mx  sync.RWMutex
}

func NewDirectGateway() Gateway {
	return new(DirectGateway)
}

type CommandReq struct {
	Name string
	Arg  []string
	Size *pty.Winsize
}

func (g *DirectGateway) Start(req *CommandReq, _ *struct{}) error {
	g.mx.Lock()
	defer g.mx.Unlock()

	if g.pty != nil {
		return ErrPtyAlreadyRunning
	}

	f, err := pty.StartWithSize(exec.Command(req.Name, req.Arg...), req.Size)
	if err != nil {
		return err
	}

	g.pty = f
	return nil
}

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

func (g *DirectGateway) Read(reqN *int, respB *[]byte) error {
	return ptyReadLock(g, func() error {
		b := make([]byte, *reqN)
		n, err := g.pty.Read(b)
		*respB = b[:n]
		return err
	})
}

func (g *DirectGateway) Write(wb *[]byte, n *int) error {
	return ptyReadLock(g, func() (err error) {
		*n, err = g.pty.Write(*wb)
		return
	})
}

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

type ProxyGateway struct {
	ptyC *Client
}

func NewProxyGateway(ptyC *Client) Gateway {
	return &ProxyGateway{ptyC: ptyC}
}

func (g *ProxyGateway) Start(req *CommandReq, _ *struct{}) error {
	return g.ptyC.Start(req.Name, req.Arg...)
}

func (g *ProxyGateway) Stop(_, _ *struct{}) error {
	return g.ptyC.Stop()
}

func (g *ProxyGateway) Read(reqN *int, respB *[]byte) error {
	b := make([]byte, *reqN)
	n, err := g.ptyC.Read(b)
	*respB = b[:n]
	return err
}

func (g *ProxyGateway) Write(reqB *[]byte, respN *int) error {
	var err error
	*respN, err = g.ptyC.Write(*reqB)
	return err
}

func (g *ProxyGateway) SetPtySize(size *pty.Winsize, _ *struct{}) error {
	return g.ptyC.SetPtySize(size)
}
