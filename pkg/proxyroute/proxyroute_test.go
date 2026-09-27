package proxyroute

import (
	"context"
	"io"
	"net"
	"testing"

	"github.com/armon/go-socks5"
	"github.com/stretchr/testify/require"
)

func TestPick(t *testing.T) {
	rules, err := Normalize([]Rule{
		{Suffix: "example.com", Upstream: "127.0.0.1:1081"},
		{Suffix: ".cdn.example.com", Upstream: Direct},
		{Suffix: "other.org", Upstream: "127.0.0.1:1082"},
	})
	require.NoError(t, err)
	require.Equal(t, "cdn.example.com", rules[0].Suffix, "longest suffix first")

	for host, want := range map[string]string{
		"example.com":         "127.0.0.1:1081",
		"www.example.com:443": "127.0.0.1:1081",
		"EXAMPLE.COM.":        "127.0.0.1:1081",
		"a.cdn.example.com":   "",
		"notexample.com":      "127.0.0.1:1080",
		"other.org:80":        "127.0.0.1:1082",
		"unmatched.net":       "127.0.0.1:1080",
	} {
		require.Equal(t, want, Pick(rules, "127.0.0.1:1080", host), host)
	}
}

func TestParseRuleAndNormalize(t *testing.T) {
	r, err := ParseRule(".Example.com=direct")
	require.NoError(t, err)
	require.Equal(t, Rule{Suffix: "example.com", Upstream: Direct}, r)

	for _, bad := range []string{"example.com", "=127.0.0.1:1", "example.com=", "example.com=nohostport"} {
		_, err := ParseRule(bad)
		require.Error(t, err, bad)
	}

	rules, err := Normalize([]Rule{
		{Suffix: "a.com", Upstream: "127.0.0.1:1"},
		{Suffix: "A.com", Upstream: "127.0.0.1:2"},
	})
	require.NoError(t, err)
	require.Equal(t, []Rule{{Suffix: "a.com", Upstream: "127.0.0.1:2"}}, rules, "a later rule for a suffix replaces the earlier one")
}

// TestForwarderRoutesByDomain runs two SOCKS5 upstreams that each rewrite every
// CONNECT to their own backend, so the reply names the exit a host went out of.
func TestForwarderRoutesByDomain(t *testing.T) {
	exitA := serveSOCKS(t, backend(t, "A"))
	exitB := serveSOCKS(t, backend(t, "B"))

	rules, err := Normalize([]Rule{{Suffix: "b.test", Upstream: exitB}})
	require.NoError(t, err)
	fw := NewForwarder(exitA, rules)
	require.True(t, fw.Active())

	require.Equal(t, "A", readAll(t, fw, "a.test:80"))
	require.Equal(t, "B", readAll(t, fw, "www.b.test:80"))

	require.False(t, NewForwarder("", nil).Active())
}

func readAll(t *testing.T, fw *Forwarder, addr string) string {
	t.Helper()
	c, err := fw.Dial("tcp", addr)
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	b, err := io.ReadAll(c)
	require.NoError(t, err)
	return string(b)
}

// backend accepts connections and writes name to each.
func backend(t *testing.T, name string) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte(name)) //nolint:errcheck
			_ = c.Close()                //nolint:errcheck
		}
	}()
	return l.Addr().String()
}

// serveSOCKS runs a SOCKS5 server that resolves every name locally and sends
// every CONNECT to target.
func serveSOCKS(t *testing.T, target string) string {
	t.Helper()
	srv, err := socks5.New(&socks5.Config{
		Resolver: fakeResolver{},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, target)
		},
	})
	require.NoError(t, err)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() }) //nolint:errcheck
	go srv.Serve(l)                     //nolint:errcheck
	return l.Addr().String()
}

type fakeResolver struct{}

func (fakeResolver) Resolve(ctx context.Context, _ string) (context.Context, net.IP, error) {
	return ctx, net.IPv4(127, 0, 0, 1), nil
}
