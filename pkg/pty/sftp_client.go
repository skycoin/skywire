// Package pty pkg/pty/sftp_client.go — client-side helpers for the
// dmsgpty sftp subsystem.
//
// Two surfaces: DialSftpTCP for the direct-TCP path (same noise-XK
// pin-by-PK model used by `skywire cli ssh`), and DialSftpDmsg for the
// dmsg-overlay path. Both return an io.ReadWriteCloser ready to hand
// to github.com/pkg/sftp.NewClientPipe — the dmsgpty mux handshake
// (writeRequest(SftpURI) + readResponse) has already been performed.
//
// Callers own the returned conn; close it via the resulting
// *sftp.Client.Close (which closes the conn underneath).
package pty

import (
	"context"
	"fmt"
	"io"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// DialSftpTCP opens an sftp-subsystem stream to a remote dmsgpty-host
// over its direct-TCP entry point (Dmsgpty.SshListen). The noise-XK
// handshake pins rPK; localPK/localSK identify this client to the
// remote whitelist. Returns a conn whose stream is positioned at the
// start of the sftp byte protocol — pass to sftp.NewClientPipe.
//
// Error surfaces: dial / handshake errors propagate verbatim; an
// "invalid request" response from the remote means the server lacks
// SftpURI registration (older host binary) — surface this as a clear
// "remote dmsgpty-host has no sftp subsystem" so operators don't
// chase it as a transport bug.
func DialSftpTCP(
	ctx context.Context,
	addr string,
	localPK cipher.PubKey,
	localSK cipher.SecKey,
	rPK cipher.PubKey,
) (io.ReadWriteCloser, error) {
	conn, err := dialNoiseTCP(ctx, addr, localPK, localSK, rPK)
	if err != nil {
		return nil, err
	}
	if err := openSftpSubsystem(conn); err != nil {
		_ = conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return conn, nil
}

// DialSftpDmsg opens an sftp-subsystem stream to (rPK, rPort) over
// the supplied dmsg.Client. rPort==0 falls back to DefaultPort (22)
// to match the rest of the pty surface.
func DialSftpDmsg(
	ctx context.Context,
	c *dmsg.Client,
	rPK cipher.PubKey,
	rPort uint16,
) (io.ReadWriteCloser, error) {
	if c == nil {
		return nil, fmt.Errorf("dmsgpty-sftp: nil dmsg client")
	}
	if rPort == 0 {
		rPort = DefaultPort
	}
	stream, err := c.DialStream(ctx, dmsg.Addr{PK: rPK, Port: rPort})
	if err != nil {
		return nil, fmt.Errorf("dmsgpty-sftp: dial %s:%d: %w", rPK, rPort, err)
	}
	if err := openSftpSubsystem(stream); err != nil {
		_ = stream.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return stream, nil
}

// OpenSftpConn performs the dmsgpty sftp-subsystem handshake on a
// stream that is ALREADY connected to a remote dmsgpty host — e.g. a
// conn bridged through the local visor's authorized dmsg client (the
// `cli pty fs mount` via-visor path). The dial + whitelist auth has
// happened on the visor side using its own identity; here we only run
// the mux handshake (writeRequest(SftpURI) + readResponse). On success
// the returned conn is positioned at the start of the sftp byte
// protocol — pass to sftp.NewClientPipe. The caller owns conn.
func OpenSftpConn(conn net.Conn) (io.ReadWriteCloser, error) {
	if conn == nil {
		return nil, fmt.Errorf("dmsgpty-sftp: nil conn")
	}
	if err := openSftpSubsystem(conn); err != nil {
		return nil, err
	}
	return conn, nil
}

// openSftpSubsystem performs the dmsgpty mux handshake selecting the
// sftp subsystem. The conn is positioned for sftp protocol bytes on
// successful return.
func openSftpSubsystem(conn net.Conn) error {
	if err := writeRequest(conn, SftpURI); err != nil {
		return fmt.Errorf("dmsgpty-sftp: write request: %w", err)
	}
	if err := readResponse(conn); err != nil {
		// Distinguish "no sftp subsystem on remote" (operator-actionable —
		// older binary on the remote) from generic rejection.
		return fmt.Errorf("dmsgpty-sftp: remote rejected sftp subsystem (peer may lack SftpURI registration): %w", err)
	}
	return nil
}
