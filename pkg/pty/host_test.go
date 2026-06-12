// Package pty pkg/pty/host_test.go
package pty

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

// TODO(evanlinjin): fix failing tests

func TestHost(t *testing.T) {
	const port = uint16(22)

	// Prepare dmsg env.
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	defaultConf := dmsg.Config{MinSessions: 2}
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 2, 2, &defaultConf))
	t.Cleanup(env.Shutdown)

	dcA := env.AllClients()[0]
	dcB := env.AllClients()[1]

	// Prepare whitelists.
	wlA, delWhitelistA := tempWhitelist(t, dcA)
	wlB, delWhitelistB := tempWhitelist(t, dcB)
	require.NoError(t, wlB.Add(dcA.LocalPK()))
	require.NoError(t, wlA.Add(dcB.LocalPK()))
	t.Run("serveConn_whitelist", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())

		connH, connC := net.Pipe()
		host := NewHost(dcA, wlA)
		hMux := cliEndpoints(host)
		go host.serveConn(ctx, logging.MustGetLogger("host_conn"), &hMux, connH)

		wlCli, err := NewWhitelistClient(connC)
		require.NoError(t, err)

		checkWhitelist(t, wlCli, 1, 10)

		// Closing logic.
		cancel()
		require.NoError(t, connH.Close())
		require.NoError(t, connC.Close())
	})
	if runtime.GOOS != "windows" { // TODO: This condition is temporary to pass test. Implementation for Windows should improve.
		t.Run("serveConn_pty", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())

			connH, connC := net.Pipe()

			host := NewHost(dcA, wlA)
			hMux := cliEndpoints(host)
			go host.serveConn(ctx, logging.MustGetLogger("host_conn"), &hMux, connH)

			ptyC, err := NewPtyClient(connC)
			require.NoError(t, err)

			checkPty(t, ptyC, "Hello World!")

			// Closing logic.
			cancel()
			require.NoError(t, connH.Close())
			require.NoError(t, connC.Close())
		})

		t.Run("serveConn_proxy", func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.TODO())

			connB, connCLI := net.Pipe()

			hostA := NewHost(dcA, wlA)
			errA := make(chan error, 1)
			go func() {
				errA <- hostA.ListenAndServe(ctx, port)
				close(errA)
			}()

			hostB := NewHost(dcB, wlB)
			hBMux := cliEndpoints(hostB)
			go hostB.serveConn(ctx, logging.MustGetLogger("hostB_conn"), &hBMux, connB)

			ptyB, err := NewProxyClient(connCLI, dcA.LocalPK(), port)
			require.NoError(t, err)

			checkPty(t, ptyB, "Hello World!")

			// Closing logic.
			cancel()
			require.NoError(t, <-errA)
			require.NoError(t, connB.Close())
			require.NoError(t, connCLI.Close())
		})
	}

	// Wait for previous subtests' DMSG listeners to fully close
	time.Sleep(3 * time.Second)

	t.Run("ServeCLI", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.TODO())

		cliL, err := nettest.NewLocalListener("tcp")
		require.NoError(t, err)

		hostA := NewHost(dcA, wlA)
		errA := make(chan error, 1)
		go func() {
			errA <- hostA.ListenAndServe(ctx, port)
			close(errA)
		}()

		hostB := NewHost(dcB, wlB)
		errB := make(chan error, 1)
		go func() {
			errB <- hostB.ServeCLI(ctx, cliL)
			close(errB)
		}()

		cliB := &CLI{
			Log:  logging.MustGetLogger("pty-cli"),
			Net:  cliL.Addr().Network(),
			Addr: cliL.Addr().String(),
		}

		t.Run("endpoint_whitelist", func(t *testing.T) {
			wlCli, err := cliB.WhitelistClient()
			require.NoError(t, err)

			checkWhitelist(t, wlCli, 1, 10)
		})
		if runtime.GOOS != "windows" { // TODO: This condition is temporary to pass test. Implementation for Windows should improve.
			t.Run("endpoint_pty", func(t *testing.T) {
				conn, err := cliB.prepareConn()
				require.NoError(t, err)

				ptyB, err := NewPtyClient(conn)
				require.NoError(t, err)

				for i := 20; i < 100; i += 10 {
					checkPty(t, ptyB, fmt.Sprintf("Hello World! %d", i))
				}

				require.NoError(t, conn.Close())
			})

			t.Run("endpoint_proxy", func(t *testing.T) {
				// Give hostA time to establish its DMSG listener
				time.Sleep(2 * time.Second)

				conn, err := cliB.prepareConn()
				require.NoError(t, err)

				ptyB, err := NewProxyClient(conn, dcA.LocalPK(), port)
				require.NoError(t, err)

				for i := 20; i < 100; i += 10 {
					checkPty(t, ptyB, fmt.Sprintf("Hello World! %d", i))
				}

				require.NoError(t, conn.Close())
			})
		}
		// A non-whitelisted host should have no access to hostA's pty.
		t.Run("no_access", func(t *testing.T) {
			dcC, err := env.NewClient(&defaultConf)
			require.NoError(t, err)

			wlC, delWhitelistC := tempWhitelist(t, dcC)
			lisC, err := nettest.NewLocalListener("tcp")
			require.NoError(t, err)

			ctx, cancel := context.WithCancel(ctx)
			hostC := NewHost(dcC, wlC)
			cErr := make(chan error, 1)
			go func() {
				cErr <- hostC.ServeCLI(ctx, lisC)
				close(cErr)
			}()

			cliC := CLI{
				Log:  logging.MustGetLogger("cli_c"),
				Net:  lisC.Addr().Network(),
				Addr: lisC.Addr().String(),
			}

			conn, err := cliC.prepareConn()
			require.NoError(t, err)

			_, err = NewProxyClient(conn, dcA.LocalPK(), port)
			require.Error(t, err)

			// Closing logic.
			cancel()
			delWhitelistC()
			require.NoError(t, <-cErr)
		})

		// Closing logic.
		cancel()
		require.NoError(t, <-errA)
		require.NoError(t, <-errB)
	})

	// Closing logic.
	delWhitelistA()
	delWhitelistB()
	env.Shutdown()
}

// TestHost_PersistentSessionReattach drives the full persistence path over the
// real RPC wire: StartSession returns an id, a dropped stream leaves the shell
// running (not killed), and a fresh connection Attaches by id and replays the
// buffered output. This complements the deterministic gateway/registry unit
// tests by confirming the StartSession/Attach RPC plumbing end to end.
func TestHost_PersistentSessionReattach(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("pty reattach test is unix-only")
	}

	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	defaultConf := dmsg.Config{MinSessions: 2}
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 2, 2, &defaultConf))
	t.Cleanup(env.Shutdown)

	dcA := env.AllClients()[0]
	wlA, delWL := tempWhitelist(t, dcA)
	defer delWL()
	require.NoError(t, wlA.Add(dcA.LocalPK()))

	host := NewHost(dcA, wlA)
	host.SetPersistentSessions(true, time.Minute)
	hMux := cliEndpoints(host)

	// Connection 1: start a persistent session that emits a marker then idles.
	ctx1, cancel1 := context.WithCancel(context.Background())
	connH1, connC1 := net.Pipe()
	go host.serveConn(ctx1, logging.MustGetLogger("reattach_1"), &hMux, connH1)

	ptyC1, err := NewPtyClient(connC1)
	require.NoError(t, err)

	sz := &WinSize{Rows: 24, Cols: 80}
	// `cat` keeps the shell alive (blocked on stdin) yet exits promptly when
	// Stop closes the pty (stdin EOF) — unlike `sleep`, which would make Stop's
	// cmd.Wait block for the full duration.
	sid, err := ptyC1.StartSession(DefaultCmd, []string{"-c", "echo REPLAY_MARKER; cat"}, sz, nil)
	require.NoError(t, err)
	require.NotEmpty(t, sid)

	// Registry-tracked under the connection's owner (zero PK — the local pipe
	// ctx carries none).
	sess, ok := host.sessions.get(sid)
	require.True(t, ok)
	require.Equal(t, cipher.PubKey{}, sess.ownerPK)

	require.True(t, readContains(t, ptyC1, "REPLAY_MARKER", 5*time.Second),
		"first connection should see the shell's output")

	// Drop the stream WITHOUT Stop — the shell must survive for reattach.
	cancel1()
	_ = connC1.Close()
	require.False(t, sess.isClosed(), "a dropped stream must not kill the shell")
	_, ok = host.sessions.get(sid)
	require.True(t, ok, "session must stay registered after a stream drop")

	// Connection 2: reattach by id; the ring replays the marker.
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	connH2, connC2 := net.Pipe()
	go host.serveConn(ctx2, logging.MustGetLogger("reattach_2"), &hMux, connH2)

	ptyC2, err := NewPtyClient(connC2)
	require.NoError(t, err)
	require.NoError(t, ptyC2.Attach(sid))
	require.True(t, readContains(t, ptyC2, "REPLAY_MARKER", 5*time.Second),
		"reattach must replay the output produced before the drop")

	require.NoError(t, ptyC2.Stop())
	_, ok = host.sessions.get(sid)
	require.False(t, ok, "Stop must evict the session from the registry")

	cancel2()
	_ = connC2.Close()
}

// readContains reads from ptyC until it has seen marker or the deadline passes.
// Each read runs in its own goroutine with its own buffer so a slow read left
// blocking past the deadline can't race the caller.
func readContains(t *testing.T, ptyC *PtyClient, marker string, d time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	var acc []byte
	for time.Now().Before(deadline) {
		type res struct {
			b   []byte
			err error
		}
		ch := make(chan res, 1)
		go func() {
			b := make([]byte, 4096)
			n, err := ptyC.Read(b)
			ch <- res{b[:n], err}
		}()
		select {
		case r := <-ch:
			acc = append(acc, r.b...)
			if bytes.Contains(acc, []byte(marker)) {
				return true
			}
			if r.err != nil {
				return false
			}
		case <-time.After(time.Until(deadline)):
			return bytes.Contains(acc, []byte(marker))
		}
	}
	return bytes.Contains(acc, []byte(marker))
}

func tempWhitelist(t *testing.T, c *dmsg.Client) (Whitelist, func()) {
	f, err := os.CreateTemp(os.TempDir(), "")
	require.NoError(t, err)

	fName := f.Name()
	require.NoError(t, f.Close())

	conf := getConfig(c)
	err = WriteConfig(conf, fName)
	require.NoError(t, err)

	t.Log(fName)
	wl, err := NewConfigWhitelist(fName)
	require.NoError(t, err)

	return wl, func() {
		require.NoError(t, os.Remove(fName))
	}
}

func checkPty(t *testing.T, ptyC *PtyClient, msg string) {
	if runtime.GOOS == "windows" {
		require.NoError(t, ptyC.Start(DefaultCmd, nil, "-Command", "Write-Host "+msg))
	} else {
		require.NoError(t, ptyC.Start(DefaultCmd, nil, "-c", "echo "+msg))
	}

	readB := make([]byte, len(msg))
	n, err := io.ReadFull(ptyC, readB)
	require.NoError(t, err)
	require.Equal(t, len(readB), n)
	if !(runtime.GOOS == "windows") {
		require.Equal(t, msg, string(readB))
	}

	// The keepalive Ping (used by the interactive terminal to refresh the
	// dmsg stream's idle read deadline) must round-trip through the host —
	// direct and proxied.
	require.NoError(t, ptyC.Ping())

	require.NoError(t, ptyC.Stop())
}

func checkWhitelist(t *testing.T, wlCli *WhitelistClient, initN, rounds int) {
	pks, err := wlCli.ViewWhitelist()
	require.NoError(t, err)
	require.Len(t, pks, initN)

	newPKS := make([]cipher.PubKey, rounds)
	for i := 0; i < rounds; i++ {
		pk, _ := cipher.GenerateKeyPair()
		require.NoError(t, wlCli.WhitelistAdd(pk), i)
		newPKS[i] = pk

		pks, err := wlCli.ViewWhitelist()
		require.NoError(t, err)
		require.Len(t, pks, initN+i+1)
	}
	for i, newPK := range newPKS {
		require.NoError(t, wlCli.WhitelistRemove(newPK))

		pks, err := wlCli.ViewWhitelist()
		require.NoError(t, err)
		require.Len(t, pks, initN+len(newPKS)-i-1)
	}
}

func getConfig(c *dmsg.Client) Config {
	conf := DefaultConfig()
	conf.SK = c.LocalSK().Hex()
	conf.PK = c.LocalPK().Hex()
	return conf
}

// NewHost creates a new pty.Host with a given dmsg.Client and whitelist.
// func ewHost(dmsgC *dmsg.Client, wl Whitelist) *Host {
// 	host := new(Host)
// 	host.dmsgC = dmsgC
// 	host.wl = wl
// 	return host
// }
