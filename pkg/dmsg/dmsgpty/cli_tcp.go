// Package dmsgpty pkg/dmsg/dmsgpty/cli_tcp.go — direct-TCP client
// for the dmsgpty service. Counterpart to host_tcp.go: dials a raw
// TCP listener exposed by the remote visor's dmsgpty.Host, runs an
// XK noise handshake pinning the remote PK, and drives the same
// PtyClient surface used over the dmsg-overlay path.
//
// Identity model: the local PK/SK passed to each method is the
// caller's stable identity, which is what the remote whitelist
// gates on. Operators wanting ssh-equivalent semantics pass a
// pinned --sk so the remote can authorize repeated connections
// from the same client across runs.
package dmsgpty

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
)

// tcpDialTimeout bounds the underlying TCP dial. tcpHandshakeTimeout
// is reused from host_tcp.go for the noise step. Together they bound
// the time spent on a wedged peer before the caller's outer context
// can drive cancellation.
const tcpDialTimeout = 10 * time.Second

// ExecRemoteTCP runs a one-shot command on a remote visor via the
// dmsgpty-host's direct-TCP entry point (Dmsgpty.TCPListen).
// rPK is pinned during the noise handshake — equivalent to ssh's
// host-key check. addr is `host:port` (any net.Dial-acceptable form).
// localPK/localSK is the caller's identity; the remote's whitelist
// gates on it.
//
// Semantics match ExecRemote (dmsg-overlay path): returns the
// command's stdout / stderr / exit code on success; an err here is
// dial / handshake / RPC-layer failure, not a non-zero command exit.
func (cli *CLI) ExecRemoteTCP(
	ctx context.Context,
	rPK cipher.PubKey,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	name string,
	args []string,
	env []string,
	stdin []byte,
	timeout time.Duration,
) (*CommandExecResult, error) {
	conn, err := dialNoiseTCP(ctx, addr, localPK, localSK, rPK)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck

	ptyC, err := NewPtyClient(conn)
	if err != nil {
		return nil, fmt.Errorf("dmsgpty-tcp: NewPtyClient: %w", err)
	}
	defer ptyC.Close() //nolint:errcheck

	req := &CommandExecReq{
		Name:      name,
		Arg:       args,
		Env:       env,
		Stdin:     stdin,
		TimeoutMS: timeout.Milliseconds(),
	}
	return ptyC.Exec(req)
}

// StartRemotePtyTCP opens an interactive pty session against a
// remote dmsgpty-host over the direct-TCP entry point. Mirrors
// StartRemotePty over the dmsg-overlay path.
func (cli *CLI) StartRemotePtyTCP(
	ctx context.Context,
	rPK cipher.PubKey,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	cmd string,
	args ...string,
) error {
	conn, err := dialNoiseTCP(ctx, addr, localPK, localSK, rPK)
	if err != nil {
		return err
	}

	ptyC, err := NewPtyClient(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return fmt.Errorf("dmsgpty-tcp: NewPtyClient: %w", err)
	}

	restore, err := cli.prepareStdin()
	if err != nil {
		_ = ptyC.Close() //nolint:errcheck,gosec
		return err
	}
	defer restore()

	return cli.servePty(ctx, ptyC, cmd, args)
}

// dialNoiseTCP opens a TCP conn to addr, runs an XK noise handshake
// pinning rPK, and returns the noise-wrapped net.Conn ready for the
// PtyClient. Failure modes:
//   - dial: TCP unreachable, dial timeout
//   - handshake: wrong server PK, expired keypair, peer not a
//     dmsgpty-tcp endpoint (no noise speaker on the other side)
func dialNoiseTCP(
	ctx context.Context,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	rPK cipher.PubKey,
) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: tcpDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dmsgpty-tcp: dial %s: %w", addr, err)
	}

	ns, err := noise.New(noise.HandshakeXK, noise.Config{
		LocalPK:   localPK,
		LocalSK:   localSK,
		RemotePK:  rPK,
		Initiator: true,
	})
	if err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("dmsgpty-tcp: noise init: %w", err)
	}
	rw := noise.NewReadWriter(conn, ns)
	if err := rw.Handshake(tcpHandshakeTimeout); err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("dmsgpty-tcp: noise handshake (server-pk pinned to %s): %w", rPK, err)
	}
	return &noiseNetConn{Conn: conn, rw: rw, rPK: rPK}, nil
}
