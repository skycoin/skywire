// Package visor pkg/visor/dmsg_bridge.go
//
// TCP→dmsg bridge for the CLI's `--via dmsg://<pk>` flow.
//
// The bridge is multiplexed onto the visor's existing CLI RPC port
// (v.conf.CLIAddr, default localhost:3435) using cmux. A conn that
// starts with the magic prefix dmsgBridgeMagic is treated as a
// bridge request; anything else falls through to gRPC or the
// standard rpc.Server.
//
// Protocol on a bridge conn (after cmux has handed it to us):
//
//   1. CLI has already sent the magic prefix (cmux consumed nothing,
//      it's still in the conn buffer).
//   2. CLI writes the 35-byte body: 33-byte target PK + 2-byte
//      little-endian dmsg port. We read magic+body together as
//      6+35 = 41 bytes.
//   3. Visor opens a dmsg stream to (target PK, target port) using
//      its own dmsg client.
//   4. Visor bidirectionally io.Copy's between the TCP conn and
//      the dmsg stream until either side closes.
//
// The CLI runs rpc.Client over the TCP conn; remote visor runs
// rpc.Server on its dmsg listener (DmsgVisorRPCPort). Both speak
// gob, no protocol translation. CLI inherits the visor's dmsg
// identity, which is in the remote's whitelist when the remote
// reports to it as hypervisor.
package visor

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// dmsgBridgeMagic is the 6-byte prefix the CLI sends to identify
// a connection as a dmsg-bridge request (vs. gRPC or rpc). cmux's
// PrefixMatcher peeks for this and routes the conn here. Chosen so
// it can't collide with the first bytes of net/rpc's gob protocol
// or HTTP2's gRPC preface.
const dmsgBridgeMagic = "SKYBRI"

const (
	// dmsgBridgeHeaderSize is the FULL bridge handshake size:
	// 6 magic + 33 PK + 2 LE port = 41 bytes. cmux's PrefixMatcher
	// peeks the 6 magic bytes but doesn't consume them, so we read
	// the full 41 bytes ourselves before doing the dmsg dial.
	dmsgBridgeHeaderSize = 41
	// dmsgBridgeHeaderTimeout caps how long we'll wait for the
	// header. A real CLI sends it immediately after connecting.
	dmsgBridgeHeaderTimeout = 5 * time.Second
	// dmsgBridgeDialTimeout caps the dmsg dial. dmsg-discovery +
	// relay handshake usually take well under this.
	dmsgBridgeDialTimeout = 15 * time.Second
)

// serveDmsgBridge accepts conns on lis (which was matched by the
// dmsgBridgeMagic prefix via cmux), reads the rest of the header,
// opens a dmsg stream to the target, and bridges bytes between the
// conn and the stream. Returns when the listener is closed.
func serveDmsgBridge(ctx context.Context, log *logging.Logger, lis net.Listener, dmsgC *dmsg.Client) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			log.WithError(err).Debug("dmsg bridge accept error")
			continue
		}
		go handleDmsgBridgeConn(ctx, log, conn, dmsgC)
	}
}

func handleDmsgBridgeConn(ctx context.Context, log *logging.Logger, tcpConn net.Conn, dmsgC *dmsg.Client) {
	defer tcpConn.Close() //nolint:errcheck,gosec

	if err := tcpConn.SetReadDeadline(time.Now().Add(dmsgBridgeHeaderTimeout)); err != nil {
		log.WithError(err).Debug("dmsg bridge: set header deadline")
		return
	}
	header := make([]byte, dmsgBridgeHeaderSize)
	if _, err := io.ReadFull(tcpConn, header); err != nil {
		log.WithError(err).Debug("dmsg bridge: read header")
		return
	}
	// Clear the deadline now that we have the header — subsequent
	// I/O is bridge traffic that should flow without a read timeout.
	if err := tcpConn.SetReadDeadline(time.Time{}); err != nil {
		log.WithError(err).Debug("dmsg bridge: clear header deadline")
		return
	}

	// Validate magic. cmux already peeked it, so any mismatch here
	// means cmux misrouted (shouldn't happen) — log + drop.
	if string(header[:6]) != dmsgBridgeMagic {
		log.WithField("got", string(header[:6])).
			Warn("dmsg bridge: magic prefix mismatch")
		return
	}

	var targetPK cipher.PubKey
	copy(targetPK[:], header[6:39])
	targetPort := binary.LittleEndian.Uint16(header[39:41])

	dialCtx, cancel := context.WithTimeout(ctx, dmsgBridgeDialTimeout)
	dmsgConn, err := dmsgC.Dial(dialCtx, dmsg.Addr{PK: targetPK, Port: targetPort})
	cancel()
	if err != nil {
		log.WithError(err).
			WithField("target_pk", targetPK.String()).
			WithField("target_port", targetPort).
			Debug("dmsg bridge: dial failed")
		return
	}
	defer dmsgConn.Close() //nolint:errcheck,gosec

	log.WithField("target_pk", targetPK.String()).
		WithField("target_port", targetPort).
		Debug("dmsg bridge: stream established, bridging bytes")

	// Bidirectional copy. Either direction closing tears down the
	// other — the CLI hanging up closes the dmsg stream, remote
	// closing the stream propagates EOF back to the CLI.
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(dmsgConn, tcpConn) //nolint:errcheck,gosec
		_ = dmsgConn.Close()              //nolint:errcheck,gosec
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(tcpConn, dmsgConn) //nolint:errcheck,gosec
		_ = tcpConn.Close()               //nolint:errcheck,gosec
	}()
	wg.Wait()
}
