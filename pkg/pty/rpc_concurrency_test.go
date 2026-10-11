//go:build !windows && !(js && wasm)

package pty

import (
	"bytes"
	"os/exec"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"

	"github.com/skycoin/skywire/internal/rpctest"
)

// TestPtyReadDoesNotBlockWrite runs cat in a pty and issues a Read that has
// nothing to return until a later Write on the same connection is served.
func TestPtyReadDoesNotBlockWrite(t *testing.T) {
	if _, err := exec.LookPath("cat"); err != nil {
		t.Skip("cat not found")
	}
	gw := &LocalPtyGateway{ses: NewPty()}
	c := rpctest.Serve(t, func(s *rpc.Server, name string) error { return registerLocalPtyGateway(s, name, gw) })
	if err := c.Call("Svc.Start", &CommandReq{Name: "cat"}, &struct{}{}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { gw.ses.Stop() }) //nolint:errcheck,gosec

	n := 4096
	var out []byte
	read := c.Go("Svc.Read", &n, &out, nil)
	in := []byte("ping\n")
	rpctest.CheckNotBlocked(t, c, read, func() error {
		var wn int
		return c.Call("Svc.Write", &in, &wn)
	}, 5*time.Second)
	if !bytes.Contains(out, []byte("ping")) {
		t.Errorf("Read = %q, want the echoed input", out)
	}
}
