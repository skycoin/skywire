//go:build android

// Package vpn pkg/vpn/share_android.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"net"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

/*
The phone's VPN hotspot (share.go) takes its connections from the app.

The app listens on the hotspot's TCP port itself, because only it can tell the
hotspot's interface from the network the phone is merely joined to — a café's
Wi-Fi must not get the tunnel. Each connection it accepts from the hotspot is
passed here as a descriptor over an abstract unix socket the app binds, one
byte plus SCM_RIGHTS per connection, and from then on it is this process's:
the app keeps no copy, and no byte of it crosses the JVM.

This side dials, as it does for the TUN, and keeps the channel open for the
whole run. The app's socket exists while its VPN service runs, so a refused
dial only means it is not up yet.
*/

const (
	// androidShareSocketEnvKey names the abstract socket the app hands
	// connections over on; the default is the name it uses today.
	androidShareSocketEnvKey  = "SKYWIRE_ANDROID_VPN_SHARE_SOCKET"
	androidShareSocketDefault = "@com.skycoin.skywire.vpn.share"

	androidShareRedial = 2 * time.Second
)

// startSharing receives the app's hotspot connections for as long as the
// client runs, and serves them through whichever tunnel session is up.
func (c *Client) startSharing() (stop func()) {
	q := newConnQueue()
	c.sharing = true

	go func() {
		if err := c.serveShared(q); err != nil && !errors.Is(err, net.ErrClosed) {
			print(fmt.Sprintf("VPN hotspot proxy stopped: %v\n", err))
		}
	}()
	go receiveFromApp(q)

	return func() { _ = q.Close() } //nolint:errcheck
}

// receiveFromApp keeps the channel to the app open until q closes.
func receiveFromApp(q *connQueue) {
	name := os.Getenv(androidShareSocketEnvKey)
	if name == "" {
		name = androidShareSocketDefault
	}

	for {
		if uc, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"}); err == nil {
			receiveUntilClosed(uc, q)
		}
		select {
		case <-q.done:
			return
		case <-time.After(androidShareRedial):
		}
	}
}

// receiveUntilClosed serves one channel, closing it when q closes so the
// blocked read returns.
func receiveUntilClosed(uc *net.UnixConn, q *connQueue) {
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-q.done:
		case <-finished:
		}
		_ = uc.Close() //nolint:errcheck
	}()

	// The abstract namespace has no permissions: whatever holds the name is
	// what answered. Only the app — our own UID — may hand us sockets to
	// carry through the tunnel.
	if !peerIsUs(uc) {
		print(fmt.Sprintf("VPN hotspot: %s is not held by this app; ignoring it\n", uc.RemoteAddr()))
		return
	}
	_ = receiveShared(uc, q.push) //nolint:errcheck
}

func peerIsUs(uc *net.UnixConn) bool {
	raw, err := uc.SyscallConn()
	if err != nil {
		return false
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil || credErr != nil {
		return false
	}
	return int(cred.Uid) == os.Getuid()
}
