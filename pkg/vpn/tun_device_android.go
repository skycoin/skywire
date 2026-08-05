//go:build android
// +build android

// Package vpn pkg/vpn/tun_device_android.go c4-app-vpn
package vpn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

/*
On Android an app may not open /dev/net/tun: the TUN belongs to the system,
which hands it out through VpnService only after the user has granted the VPN
consent. So the phone's TUN is created by the Kotlin side and its file
descriptor is passed here over a unix socket as SCM_RIGHTS ancillary data —
the same handoff every non-root Android VPN uses.

The app is the server on an abstract unix socket; this side is the client. One
control connection lives for as long as the device does, carrying newline-
delimited JSON:

	→ {"op":"establish","addr":"192.168.255.6/29","gateway":"192.168.255.5","mtu":1500,"dns":""}
	← {"ok":true}                                        + the TUN fd (SCM_RIGHTS)
	→ {"op":"down"}
	← {"ok":true}

`establish` is the whole of SetupTUN: address, MTU, DNS and routes are declared
on VpnService.Builder, which is the only way to set them without CAP_NET_ADMIN.
`down` drops the interface, which is what routing traffic directly means here —
see os_client_android.go, where the shared client's route calls land.
*/

const (
	// androidVPNSocketEnvKey names the abstract unix socket the app listens
	// on. It is an env var rather than a constant so the app owns the name;
	// the default below is what it uses today.
	androidVPNSocketEnvKey = "SKYWIRE_ANDROID_VPN_SOCKET"

	// The leading '@' is Go's spelling of Linux's abstract socket namespace
	// (a leading NUL byte) — the namespace Android's LocalServerSocket uses.
	androidVPNSocketDefault = "@com.skycoin.skywire.vpn"

	// androidTUNName is cosmetic: the interface is named by the system, and
	// nothing on this side can rename it. It only reaches log lines and the
	// (ignored) ifcName argument of SetupTUN.
	androidTUNName = "tun0"

	// A round trip is a Builder call plus a sendmsg — milliseconds in
	// practice. The budget only has to be generous enough to survive a busy
	// main thread, and short enough that a dead app fails the dial instead of
	// wedging the tunnel.
	androidTUNHandshakeTimeout = 10 * time.Second

	androidTUNOpEstablish = "establish"
	androidTUNOpDown      = "down"
)

var (
	errAndroidTUNNotEstablished = errors.New("TUN is not established: the VPN service has not handed one over")
	errAndroidTUNNoFd           = errors.New("the VPN service acknowledged establish without sending a descriptor")
)

// androidTUNRequest is one control line to the app.
type androidTUNRequest struct {
	Op string `json:"op"`
	// Addr is the interface address in CIDR form, Gateway the peer address
	// the server assigned. Android needs only Addr, but the gateway is sent
	// so the app can log the pair it was configured with.
	Addr    string `json:"addr,omitempty"`
	Gateway string `json:"gateway,omitempty"`
	MTU     int    `json:"mtu,omitempty"`
	// DNS is empty unless the user set --dns; the app substitutes its own
	// default then, because a full-tunnel VPN that declares no resolver can
	// strand the phone on a LAN one it can no longer reach.
	DNS string `json:"dns,omitempty"`
}

// androidTUNReply is the app's answer to one request.
type androidTUNReply struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// androidTUN is a TUNDevice whose descriptor comes from the Android app.
//
// It exists before it has a descriptor: newTUNDevice is called by the shared
// client before the handshake with the VPN server is done, and only SetupTUN
// knows the address the interface must carry.
type androidTUN struct {
	mu   sync.Mutex
	ctl  *net.UnixConn
	file *os.File
	// params is the request `file` was established with — an identical one
	// is satisfied by the interface already up.
	params androidTUNRequest
	closed bool
}

func newTUNDevice() (TUNDevice, error) {
	return &androidTUN{}, nil
}

// Name implements TUNDevice.
func (t *androidTUN) Name() string { return androidTUNName }

// Read implements TUNDevice, reading one outbound packet from the phone.
func (t *androidTUN) Read(p []byte) (int, error) {
	f, err := t.established()
	if err != nil {
		return 0, err
	}

	return f.Read(p)
}

// Write implements TUNDevice, injecting one inbound packet into the phone.
func (t *androidTUN) Write(p []byte) (int, error) {
	f, err := t.established()
	if err != nil {
		return 0, err
	}

	return f.Write(p)
}

// Close implements TUNDevice. Closing the control channel is also how the app
// learns the core is gone: with the killswitch off that is its cue to drop the
// interface, and with it on it deliberately keeps blocking.
func (t *androidTUN) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return nil
	}
	t.closed = true

	var err error
	if t.file != nil {
		err = t.file.Close()
		t.file, t.params = nil, androidTUNRequest{}
	}
	t.dropCtl()

	return err
}

// establish asks the app for an interface carrying these parameters and takes
// over the descriptor it sends back. This is what SetupTUN does on Android.
func (t *androidTUN) establish(addrCIDR, gateway string, mtu int, dns string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return os.ErrClosed
	}

	req := androidTUNRequest{
		Op:      androidTUNOpEstablish,
		Addr:    addrCIDR,
		Gateway: gateway,
		MTU:     mtu,
		DNS:     dns,
	}

	// Reconnecting to the same server lands the same address, and then the
	// interface already up IS the one being asked for. Leaving it alone is
	// what keeps a killswitched reconnect seamless: establishing again would
	// swap the descriptor out from under a tunnel that never went down.
	if t.file != nil && t.params == req {
		return nil
	}

	file, err := t.roundTrip(req)
	if err != nil {
		return err
	}

	// Our copy of the interface this one replaces. The app closes its own
	// when it establishes the new one, so nothing is left holding the old.
	if t.file != nil {
		_ = t.file.Close()
	}
	t.file, t.params = file, req

	return nil
}

// down drops the interface, so the phone goes back to its normal networking.
// Idempotent: the shared client deletes both half-space routes, and a stop
// with the killswitch on runs the same path a second time.
func (t *androidTUN) down() error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.file == nil {
		return nil
	}

	// Ours first, so a Read still blocked on the old interface unblocks
	// rather than waiting for a packet that will never come.
	_ = t.file.Close()
	t.file, t.params = nil, androidTUNRequest{}

	if t.ctl == nil {
		return nil
	}
	_, err := t.roundTrip(androidTUNRequest{Op: androidTUNOpDown})

	return err
}

// established returns the live descriptor, or says why there isn't one.
func (t *androidTUN) established() (*os.File, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	switch {
	case t.closed:
		return nil, os.ErrClosed
	case t.file == nil:
		return nil, errAndroidTUNNotEstablished
	default:
		return t.file, nil
	}
}

// roundTrip sends one request and reads its reply, redialing once if the
// existing control channel turns out to be dead — an app that was restarted
// under us should cost a reconnect attempt, not the session. Call with mu held.
func (t *androidTUN) roundTrip(req androidTUNRequest) (*os.File, error) {
	conn, err := t.dial()
	if err != nil {
		return nil, err
	}

	file, err := requestTUN(conn, req)
	if err == nil {
		return file, nil
	}

	t.dropCtl()
	conn, dialErr := t.dial()
	if dialErr != nil {
		return nil, fmt.Errorf("%w (redial: %v)", err, dialErr)
	}

	return requestTUN(conn, req)
}

// dial returns the control channel, opening it on first use. Call with mu held.
func (t *androidTUN) dial() (*net.UnixConn, error) {
	if t.ctl != nil {
		return t.ctl, nil
	}

	name := os.Getenv(androidVPNSocketEnvKey)
	if name == "" {
		name = androidVPNSocketDefault
	}

	conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: name, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("error reaching the VPN service on %s (is it running?): %w", name, err)
	}
	t.ctl = conn

	return conn, nil
}

// dropCtl closes the control channel so the next request redials. Call with
// mu held.
func (t *androidTUN) dropCtl() {
	if t.ctl == nil {
		return
	}
	_ = t.ctl.Close()
	t.ctl = nil
}

// requestTUN writes one control line and reads the reply, which for an
// establish carries the interface's descriptor as ancillary data.
func requestTUN(conn *net.UnixConn, req androidTUNRequest) (*os.File, error) {
	line, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(androidTUNHandshakeTimeout)); err != nil {
		return nil, err
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	if _, err := conn.Write(append(line, '\n')); err != nil {
		return nil, fmt.Errorf("error sending %s to the VPN service: %w", req.Op, err)
	}

	buf := make([]byte, 4096)
	oob := make([]byte, unix.CmsgSpace(4))
	n, oobn, _, _, err := conn.ReadMsgUnix(buf, oob)
	if err != nil {
		return nil, fmt.Errorf("error reading the VPN service's %s reply: %w", req.Op, err)
	}

	var reply androidTUNReply
	if err := json.Unmarshal(bytes.TrimSpace(buf[:n]), &reply); err != nil {
		return nil, fmt.Errorf("malformed %s reply from the VPN service: %w", req.Op, err)
	}
	if !reply.OK {
		return nil, fmt.Errorf("the VPN service refused %s: %s", req.Op, reply.Error)
	}
	if req.Op != androidTUNOpEstablish {
		return nil, nil
	}

	return parseTUNFd(oob[:oobn])
}

// parseTUNFd picks the handed-over descriptor out of the ancillary data.
func parseTUNFd(oob []byte) (*os.File, error) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return nil, fmt.Errorf("error parsing the TUN control message: %w", err)
	}

	for i := range msgs {
		fds, err := unix.ParseUnixRights(&msgs[i])
		if err != nil || len(fds) == 0 {
			continue
		}
		// Exactly one is expected; anything else is a protocol error whose
		// descriptors must not be leaked into this process.
		for _, extra := range fds[1:] {
			_ = unix.Close(extra)
		}

		return tunFile(fds[0])
	}

	return nil, errAndroidTUNNoFd
}

// tunFile wraps the descriptor so the Go runtime polls it.
//
// The mode matters: a blocking descriptor leaves a Read parked in the kernel
// with no way out, so replacing the interface (or a killswitched stop) would
// hang the copy goroutine forever. A non-blocking one is registered with the
// netpoller instead, and Close unblocks every pending Read with os.ErrClosed.
func tunFile(fd int) (*os.File, error) {
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("error putting the TUN descriptor in non-blocking mode: %w", err)
	}

	return os.NewFile(uintptr(fd), androidTUNName), nil
}
