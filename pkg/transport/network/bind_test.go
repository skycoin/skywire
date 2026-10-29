package network

import (
	"errors"
	"io"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func fastBindTimings(t *testing.T) {
	t.Helper()
	r, m, i := bindRetryDelay, bindMaxRetryDelay, reRegisterInterval
	bindRetryDelay, bindMaxRetryDelay, reRegisterInterval = time.Millisecond, 2*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { bindRetryDelay, bindMaxRetryDelay, reRegisterInterval = r, m, i })
}

func TestBindUntilBound(t *testing.T) {
	fastBindTimings(t)
	c := &genericClient{log: testLog(), done: make(chan struct{})}
	var calls atomic.Int32
	require.True(t, c.bindUntilBound("test", func() error {
		if calls.Add(1) < 3 {
			return errors.New("not yet")
		}
		return nil
	}))
	require.Equal(t, int32(3), calls.Load())

	close(c.done)
	require.False(t, c.bindUntilBound("test", func() error { return errors.New("never") }))
}

func TestReRegisterRetriesAndTrigger(t *testing.T) {
	fastBindTimings(t)
	reRegisterInterval = time.Hour
	c := &genericClient{log: testLog(), done: make(chan struct{})}
	trigger := make(chan struct{})
	var calls atomic.Int32
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		c.reRegister("test", func() error {
			if calls.Add(1) < 3 {
				return errors.New("refresh failed")
			}
			return nil
		}, trigger)
	}()
	trigger <- struct{}{}
	require.Eventually(t, func() bool { return calls.Load() == 3 }, time.Second, time.Millisecond,
		"a failed refresh must be retried")
	close(c.done)
	<-stopped
}

func TestListenWithFallback(t *testing.T) {
	c := &genericClient{log: testLog(), done: make(chan struct{})}
	port := 7000
	var tried []string
	addr, err := listenWithFallback(c, &port, func(addr string) (string, error) {
		tried = append(tried, addr)
		if addr == ":7000" {
			return "", syscall.EADDRINUSE
		}
		return addr, nil
	})
	require.NoError(t, err)
	require.Equal(t, ":7001", addr)
	require.Equal(t, 7001, port)
	require.Equal(t, []string{":7000", ":7001"}, tried)

	close(c.done)
	_, err = listenWithFallback(c, &port, func(string) (string, error) { return "", syscall.EADDRINUSE })
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestRebindTriggersReRegister(t *testing.T) {
	fastBindTimings(t)
	reRegisterInterval = time.Hour
	c := &genericClient{log: testLog(), done: make(chan struct{}), rebind: make(chan struct{}, 1)}
	var calls atomic.Int32
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		c.reRegister("test", func() error { calls.Add(1); return nil }, nil)
	}()
	c.Rebind()
	c.Rebind() // folded into the pending one, or a second bind; never blocks
	require.Eventually(t, func() bool { return calls.Load() >= 1 }, time.Second, time.Millisecond)
	close(c.done)
	<-stopped

	// A client with no loop ignores it.
	(&genericClient{}).Rebind()
}
