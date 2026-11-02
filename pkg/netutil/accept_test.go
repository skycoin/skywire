// Package netutil pkg/netutil/accept_test.go c0-com-util
package netutil

import (
	"errors"
	"net"
	"os"
	"testing"
)

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func TestIsTransientAcceptErr(t *testing.T) {
	for _, errno := range transientAcceptErrnos {
		err := &net.OpError{Op: "accept", Err: os.NewSyscallError("accept", errno)}
		if !IsTransientAcceptErr(err) {
			t.Errorf("%v: want transient", err)
		}
	}
	if !IsTransientAcceptErr(fakeTimeout{}) {
		t.Error("timeout: want transient")
	}
	for _, err := range []error{net.ErrClosed, errors.New("boom"), os.ErrPermission} {
		if IsTransientAcceptErr(err) {
			t.Errorf("%v: want fatal", err)
		}
	}
}

func TestAcceptBackoff(t *testing.T) {
	var b AcceptBackoff
	if b.Retry(net.ErrClosed) {
		t.Fatal("closed listener must not be retried")
	}
	for range 2 {
		if !b.Retry(fakeTimeout{}) {
			t.Fatal("timeout must be retried")
		}
	}
	if b.delay != 10e6 {
		t.Fatalf("delay = %v, want 10ms", b.delay)
	}
	b.Reset()
	if b.delay != 0 {
		t.Fatal("reset did not clear the delay")
	}
}
