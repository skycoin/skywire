// Package netutil pkg/netutil/netutil_more_test.go: unit tests for the port
// reserver, the pure IP/address helpers, and the connection copier.
package netutil

import (
	"context"
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeCloser struct{ closed atomic.Bool }

func (f *fakeCloser) Close() error { f.closed.Store(true); return nil }

type stringerVal struct{ s string }

func (v stringerVal) String() string { return v.s }

// --- porter.go ---

func TestPorterReserve(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)

	// Port 0 is reserved up front.
	ok, free := p.Reserve(0, nil)
	assert.False(t, ok)
	assert.Nil(t, free)

	ok, free = p.Reserve(8080, "listener")
	require.True(t, ok)
	require.NotNil(t, free)
	assert.Equal(t, 1, p.Count())

	v, ok := p.PortValue(8080)
	assert.True(t, ok)
	assert.Equal(t, "listener", v)

	// Double reserve fails.
	ok2, free2 := p.Reserve(8080, "other")
	assert.False(t, ok2)
	assert.Nil(t, free2)

	// Free releases (idempotently).
	free()
	free()
	assert.Equal(t, 0, p.Count())
	ok3, _ := p.Reserve(8080, "again")
	assert.True(t, ok3)
}

func TestPorterReserveChild(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)

	// Child of a missing parent fails.
	ok, free := p.ReserveChild(7000, 1, "x")
	assert.False(t, ok)
	assert.Nil(t, free)

	p.Reserve(7000, "parent") //nolint:errcheck
	ok, childFree := p.ReserveChild(7000, 1, "child")
	require.True(t, ok)
	require.NotNil(t, childFree)

	// Duplicate child fails.
	ok2, _ := p.ReserveChild(7000, 1, "dup")
	assert.False(t, ok2)

	childFree() // releases the child
}

func TestPorterChildFreerDeletesEnsurePort(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	// An "ensure" port has a nil value; freeing its last child removes it.
	p.Reserve(7100, nil) //nolint:errcheck
	_, childFree := p.ReserveChild(7100, 2, "c")
	childFree()
	_, ok := p.PortValue(7100)
	assert.False(t, ok, "ensure port with nil value and no children should be deleted")
}

func TestPorterReserveEphemeral(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	ctx := context.Background()

	seen := map[uint16]struct{}{}
	for range 5 {
		port, free, err := p.ReserveEphemeral(ctx, "v")
		require.NoError(t, err)
		require.NotNil(t, free)
		assert.GreaterOrEqual(t, port, PorterMinEphemeral)
		_, dup := seen[port]
		assert.False(t, dup)
		seen[port] = struct{}{}
	}
}

func TestPorterReserveEphemeralCancelled(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := p.ReserveEphemeral(ctx, "v")
	assert.ErrorIs(t, err, context.Canceled)
}

func TestPorterReserveEphemeralExhausted(t *testing.T) {
	p := NewPorter(65535) // single ephemeral slot
	_, _, err := p.ReserveEphemeral(context.Background(), "v")
	require.NoError(t, err)
	_, _, err = p.ReserveEphemeral(context.Background(), "v")
	assert.ErrorIs(t, err, ErrEphemeralPortSpace)
}

func TestPorterResetEphemeral(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	p.Reserve(80, "listener") //nolint:errcheck — well-known, must survive
	for range 3 {
		_, _, err := p.ReserveEphemeral(context.Background(), "v")
		require.NoError(t, err)
	}
	freed := p.ResetEphemeral()
	assert.Equal(t, 3, freed)
	_, ok := p.PortValue(80)
	assert.True(t, ok, "well-known port preserved")
}

func TestPorterRange(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	p.Reserve(81, "a")         //nolint:errcheck
	p.Reserve(82, "b")         //nolint:errcheck
	p.ReserveChild(81, 1, "c") //nolint:errcheck

	count := 0
	p.RangePortValues(func(_ uint16, _ any) bool { count++; return true })
	assert.GreaterOrEqual(t, count, 3) // includes port 0

	// Early stop visits exactly one.
	visited := 0
	p.RangePortValues(func(_ uint16, _ any) bool { visited++; return false })
	assert.Equal(t, 1, visited)

	withChildren := 0
	p.RangePortValuesAndChildren(func(_ uint16, _ PorterValue) bool { withChildren++; return true })
	assert.GreaterOrEqual(t, withChildren, 3)
}

func TestPorterEphemeralDiag(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	p.Reserve(90, "listener")                                               //nolint:errcheck
	_, _, _ = p.ReserveEphemeral(context.Background(), stringerVal{"info"}) //nolint
	_, _, _ = p.ReserveEphemeral(context.Background(), nil)                 //nolint

	d := p.EphemeralDiag()
	assert.Equal(t, 2, d.Ephemeral)
	assert.Equal(t, 2, d.Listeners) // port 0 (always reserved) + port 90
	assert.Equal(t, 1, d.NilValues)
	assert.Equal(t, uint64(2), d.TotalReserved)
	assert.Equal(t, 2, d.AgeBucket["0-30s"])
	assert.NotEmpty(t, d.LeakRate)
	assert.NotEmpty(t, d.Sample)
}

func TestPorterSweepStaleEphemeral(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	fc := &fakeCloser{}
	_, _, err := p.ReserveEphemeral(context.Background(), fc)
	require.NoError(t, err)

	// Negative maxAge → everything is "stale".
	swept := p.SweepStaleEphemeral(-time.Second)
	assert.Equal(t, 1, swept)
	assert.True(t, fc.closed.Load(), "stale closer should be closed")
}

func TestPorterCloseAll(t *testing.T) {
	p := NewPorter(PorterMinEphemeral)
	fc := &fakeCloser{}
	p.Reserve(95, fc)   //nolint:errcheck
	p.Reserve(96, "no") //nolint:errcheck — not a Closer, skipped

	p.CloseAll(nil) // nil logger → uses a default internally
	assert.True(t, fc.closed.Load())
}

// --- net.go ---

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"1.1.1.1", true},
		{"127.0.0.1", false},    // loopback
		{"10.0.0.1", false},     // private A
		{"172.16.0.1", false},   // private B
		{"172.31.255.1", false}, // private B upper
		{"192.168.1.1", false},  // private C
		{"169.254.1.1", false},  // link-local
		{"::1", false},          // IPv6 loopback
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, IsPublicIP(net.ParseIP(c.ip)), "ip %s", c.ip)
	}
}

func TestExtractPort(t *testing.T) {
	tcp, err := ExtractPort(&net.TCPAddr{Port: 1234})
	require.NoError(t, err)
	assert.Equal(t, uint16(1234), tcp)

	udp, err := ExtractPort(&net.UDPAddr{Port: 5678})
	require.NoError(t, err)
	assert.Equal(t, uint16(5678), udp)

	_, err = ExtractPort(&net.IPAddr{IP: net.ParseIP("1.2.3.4")})
	assert.Error(t, err)
}

func TestIsVirtualInterface(t *testing.T) {
	for _, n := range []string{"docker0", "br-abc", "veth123", "virbr0", "lxcbr0", "cni0", "flannel.1", "calico1"} {
		assert.Truef(t, IsVirtualInterface(n), "%s should be virtual", n)
	}
	for _, n := range []string{"eth0", "en0", "wlan0", "br"} { // "br" shorter than "br-"
		assert.Falsef(t, IsVirtualInterface(n), "%s should not be virtual", n)
	}
}

func TestLocalAddresses(t *testing.T) {
	// System-dependent, but must not error and returns a slice.
	addrs, err := LocalAddresses()
	require.NoError(t, err)
	assert.NotNil(t, addrs)
}

// --- copy.go ---

func TestCopyReadWriteCloser(t *testing.T) {
	a1, a2 := net.Pipe()
	b1, b2 := net.Pipe()

	errCh := make(chan error, 1)
	go func() { errCh <- CopyReadWriteCloser(a2, b1) }()

	go func() { _, _ = a1.Write([]byte("hello")) }() //nolint

	got := make([]byte, 5)
	_, err := io.ReadFull(b2, got)
	require.NoError(t, err)
	assert.Equal(t, "hello", string(got))

	// Closing one external end ends the copy.
	_ = a1.Close() //nolint
	select {
	case <-errCh:
	case <-time.After(3 * time.Second):
		t.Fatal("CopyReadWriteCloser did not return after a side closed")
	}
	_ = b2.Close() //nolint
}

// nonDeadlineConn is an io.ReadWriteCloser without deadline methods, exercising
// forceInterrupt's type-assertion miss path.
type nonDeadlineConn struct{}

func (nonDeadlineConn) Read([]byte) (int, error)    { return 0, io.EOF }
func (nonDeadlineConn) Write(p []byte) (int, error) { return len(p), nil }
func (nonDeadlineConn) Close() error                { return nil }

func TestForceInterruptNonDeadline(t *testing.T) {
	assert.NotPanics(t, func() { forceInterrupt(nonDeadlineConn{}) })
}
