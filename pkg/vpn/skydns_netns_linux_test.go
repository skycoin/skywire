//go:build skydnsint && linux && !android

// Package vpn pkg/vpn/skydns_netns_linux_test.go c4-app-vpn
//
// SkyDNS end to end in a throwaway user, mount and net namespace. Mesh dials go
// to a SOCKS5 resolving proxy reached over a unix socket, since the namespace
// has no network of its own.
//
//	unshare -rmn env SKYDNS_SOCKS=/path/to.sock SKYDNS_HOST=<label>.dmsg \
//	  go test -tags skydnsint -run TestSkyDNSNetns -v ./pkg/vpn/
package vpn

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/proxy"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/vpn/netctl"
)

func TestSkyDNSNetns(t *testing.T) {
	sock, host := os.Getenv("SKYDNS_SOCKS"), os.Getenv("SKYDNS_HOST")
	if sock == "" || host == "" {
		t.Skip("set SKYDNS_SOCKS and SKYDNS_HOST")
	}
	if err := netctl.LinkUp("lo"); err != nil {
		t.Skipf("not in a writable net namespace (run under `unshare -rmn`): %v", err)
	}
	conf := filepath.Join(t.TempDir(), "resolv.conf")
	require.NoError(t, os.WriteFile(conf, []byte("nameserver 127.0.0.1\n"), 0o644))
	if out, err := exec.Command("mount", "--bind", conf, resolvConfPath).CombinedOutput(); err != nil {
		t.Skipf("cannot bind mount resolv.conf (need a mount namespace): %v\n%s", err, out)
	}
	defer exec.Command("umount", resolvConfPath).Run() //nolint:errcheck

	socks, err := proxy.SOCKS5("unix", sock, nil, proxy.Direct)
	require.NoError(t, err)
	dial := func(ctx context.Context, scheme string, dest cipher.PubKey, port uint16) (net.Conn, error) {
		return socks.(proxy.ContextDialer).DialContext(ctx, "tcp", fmt.Sprintf("%s.%s:%d", dest.Hex(), scheme, port))
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- RunSkyDNS(ctx, SkyDNSConfig{Dial: dial}) }()
	require.Eventually(t, func() bool {
		b, _ := os.ReadFile(resolvConfPath) //nolint:errcheck
		return strings.HasPrefix(string(b), resolvBegin)
	}, 10*time.Second, 50*time.Millisecond)

	// Go's resolver reads resolv.conf, as glibc does.
	addrs, err := net.DefaultResolver.LookupHost(context.Background(), host)
	require.NoError(t, err)
	require.NotEmpty(t, addrs)
	require.True(t, strings.HasPrefix(addrs[0], "198.18."), addrs)

	cl := &http.Client{Timeout: 30 * time.Second}
	resp, err := cl.Get("http://" + host + "/health")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body) //nolint:errcheck
	resp.Body.Close()                //nolint:errcheck,gosec
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	t.Logf("%s → %s: %s", host, addrs[0], body)

	if extra := os.Getenv("SKYDNS_EXEC"); extra != "" {
		out, err := exec.Command("sh", "-c", extra).CombinedOutput() //nolint:gosec
		t.Logf("%s\n%s", extra, out)
		require.NoError(t, err)
	}

	cancel()
	require.NoError(t, <-done)
	b, err := os.ReadFile(resolvConfPath)
	require.NoError(t, err)
	require.Equal(t, "nameserver 127.0.0.1\n", string(b), "resolv.conf restored")
}
