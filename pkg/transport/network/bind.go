// Package network pkg/transport/network/bind.go c2-net-transport
//
// The address resolver bind and listen loops shared by stcpr, squicr and swtr.
package network

import (
	"fmt"
	"io"
	"time"
)

var (
	// bindRetryDelay is the first wait after a failed bind. It doubles up to
	// bindMaxRetryDelay.
	bindRetryDelay    = 5 * time.Second
	bindMaxRetryDelay = 60 * time.Second
	// reRegisterInterval refreshes a binding well inside the resolver's TTL.
	reRegisterInterval = 90 * time.Second
)

const (
	// reRegisterRetries is how many times a failed refresh is retried before
	// waiting for the next interval. A missed refresh drops the entry.
	reRegisterRetries = 3
)

// sleep waits for d and reports false if the client closed first.
func (c *genericClient) sleep(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c.done:
		return false
	case <-t.C:
		return true
	}
}

// bindUntilBound calls bind until it succeeds, backing off between attempts.
// It reports false if the client closed first.
func (c *genericClient) bindUntilBound(name string, bind func() error) bool {
	delay := bindRetryDelay
	for attempt := 1; ; attempt++ {
		err := bind()
		if err == nil {
			c.log.Debugf("Bound %s to the address resolver", name)
			return true
		}
		c.log.WithError(err).Warnf("Failed to bind %s (attempt %d), retrying in %v", name, attempt, delay)
		if !c.sleep(delay) {
			return false
		}
		delay = min(delay*2, bindMaxRetryDelay)
	}
}

// reRegister calls bind every reRegisterInterval, and whenever trigger fires,
// until the client closes. A nil trigger never fires.
func (c *genericClient) reRegister(name string, bind func() error, trigger <-chan struct{}) {
	ticker := time.NewTicker(reRegisterInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-ticker.C:
		case <-trigger:
		}
		err := bind()
		if err == nil {
			continue
		}
		c.log.WithError(err).Warnf("Failed to re-register %s, retrying", name)
		delay := bindRetryDelay
		for attempt := 1; attempt <= reRegisterRetries; attempt++ {
			if !c.sleep(delay) {
				return
			}
			if err = bind(); err == nil {
				break
			}
			c.log.WithError(err).Warnf("%s re-registration retry %d/%d failed", name, attempt, reRegisterRetries)
			delay = min(delay*2, bindMaxRetryDelay)
		}
	}
}

// listenWithFallback calls listen on *port, moving to the next port while the
// port is taken, until it succeeds or the client closes. Port 0 lets the OS
// choose.
func listenWithFallback[L any](c *genericClient, port *int, listen func(addr string) (L, error)) (L, error) {
	for {
		addr := ""
		if *port != 0 {
			addr = fmt.Sprintf(":%d", *port)
		}
		l, err := listen(addr)
		if err == nil {
			return l, nil
		}
		if c.isClosed() {
			var zero L
			return zero, io.ErrClosedPipe
		}
		c.log.WithError(err).Warnf("Failed to listen on port %d", *port)
		*port = nextListenPort(err, *port)
	}
}
