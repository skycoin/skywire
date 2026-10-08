// SPDX-FileCopyrightText: 2023 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

// Package ctxconn is a netctx.PacketConn that starts no goroutine per call.
// netctx watches each read and write's context from a new goroutine, one per
// DTLS record in each direction. context.AfterFunc runs only on cancellation.
package ctxconn

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/pion/transport/v5/netctx"
)

// veryOld is a deadline in the past, which makes a blocked call return.
var veryOld = time.Unix(0, 1) //nolint:gochecknoglobals

type packetConn struct {
	nextConn  net.PacketConn
	closed    chan struct{}
	closeOnce sync.Once

	readMu  sync.Mutex
	readCB  sync.WaitGroup // the read cancel callback, if it ran
	readErr error          // SetReadDeadline error from the callback

	writeMu  sync.Mutex
	writeCB  sync.WaitGroup
	writeErr error
}

// New wraps pconn.
func New(pconn net.PacketConn) netctx.PacketConn {
	return &packetConn{nextConn: pconn, closed: make(chan struct{})}
}

// ReadFromContext reads from the conn, and cancels the read when ctx is done.
func (p *packetConn) ReadFromContext(ctx context.Context, b []byte) (int, net.Addr, error) {
	p.readMu.Lock()
	defer p.readMu.Unlock()

	select {
	case <-p.closed:
		return 0, nil, net.ErrClosed
	default:
	}

	if ctx.Done() == nil {
		return p.nextConn.ReadFrom(b)
	}
	p.readErr = nil
	p.readCB.Add(1)
	stop := context.AfterFunc(ctx, func() {
		defer p.readCB.Done()
		p.readErr = p.nextConn.SetReadDeadline(veryOld)
	})
	n, raddr, err := p.nextConn.ReadFrom(b)
	if stop() {
		p.readCB.Done()
	} else {
		p.readCB.Wait()
		if p.readErr == nil {
			p.readErr = p.nextConn.SetReadDeadline(time.Time{})
		}
	}
	if e := ctx.Err(); e != nil && n == 0 {
		err = e
	}
	if err == nil && p.readErr != nil {
		err = p.readErr
	}

	return n, raddr, err
}

// WriteToContext writes to the conn, and cancels the write when ctx is done.
func (p *packetConn) WriteToContext(ctx context.Context, b []byte, raddr net.Addr) (int, error) {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()

	select {
	case <-p.closed:
		return 0, netctx.ErrClosing
	default:
	}

	if ctx.Done() == nil {
		return p.nextConn.WriteTo(b, raddr)
	}
	p.writeErr = nil
	p.writeCB.Add(1)
	stop := context.AfterFunc(ctx, func() {
		defer p.writeCB.Done()
		p.writeErr = p.nextConn.SetWriteDeadline(veryOld)
	})
	n, err := p.nextConn.WriteTo(b, raddr)
	if stop() {
		p.writeCB.Done()
	} else {
		p.writeCB.Wait()
		if p.writeErr == nil {
			p.writeErr = p.nextConn.SetWriteDeadline(time.Time{})
		}
	}
	if e := ctx.Err(); e != nil && n == 0 {
		err = e
	}
	if err == nil && p.writeErr != nil {
		err = p.writeErr
	}

	return n, err
}

// Close closes the conn and makes later calls fail.
func (p *packetConn) Close() error {
	err := p.nextConn.Close()
	p.closeOnce.Do(func() {
		p.writeMu.Lock()
		p.readMu.Lock()
		close(p.closed)
		p.readMu.Unlock()
		p.writeMu.Unlock()
	})

	return err
}

// LocalAddr returns the local address of the conn.
func (p *packetConn) LocalAddr() net.Addr { return p.nextConn.LocalAddr() }

// Conn returns the wrapped conn.
func (p *packetConn) Conn() net.PacketConn { return p.nextConn }
