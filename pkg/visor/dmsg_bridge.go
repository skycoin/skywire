// Package visor pkg/visor/dmsg_bridge.go
//
// TCP→dmsg bridge for the CLI's `--rpc dmsg://<pk>` flow.
//
// The CLI opens a TCP connection to skyenv.DmsgBridgeAddr on the
// local visor, writes a 35-byte header (33 PK bytes + 2 LE port
// bytes), and the visor opens a dmsg stream to the named target.
// After that, bytes flow bidirectionally between the TCP conn and
// the dmsg stream — the CLI runs its rpc.Client over the TCP
// conn, the remote visor runs rpc.Server over its end of the dmsg
// stream, and the bridge is transparent to both.
//
// The CLI inherits the visor's dmsg identity for the dial, which
// means a CLI running on the same host as the local hypervisor
// can reach any remote visor that lists the local PK as a
// hypervisor — without needing a dedicated CLI keypair in the
// remote's dmsgpty whitelist.
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

const (
	// dmsgBridgeHeaderSize is 33 bytes (PK) + 2 bytes (uint16
	// little-endian port) = 35 bytes total.
	dmsgBridgeHeaderSize = 35
	// dmsgBridgeHeaderTimeout caps the time the bridge waits for
	// the CLI to send its 35-byte header. A real client sends
	// it immediately after connecting; a stalled connection
	// without the header is a misbehaving caller and should be
	// closed quickly.
	dmsgBridgeHeaderTimeout = 5 * time.Second
	// dmsgBridgeDialTimeout caps the time the bridge spends
	// opening the dmsg stream to the target peer. dmsg-discovery
	// resolution + relay handshake usually take well under this;
	// a longer wait means the target is offline or unreachable.
	dmsgBridgeDialTimeout = 15 * time.Second
)

// serveDmsgBridge accepts TCP connections on lis, reads the
// 35-byte header from each, opens a dmsg stream to the target,
// and proxies bytes between the two. Returns when the listener is
// closed or ctx is canceled.
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

	var targetPK cipher.PubKey
	copy(targetPK[:], header[:33])
	targetPort := binary.LittleEndian.Uint16(header[33:35])

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

	// Bidirectional copy. Either direction closing closes both,
	// which is what we want — the CLI hanging up tears down the
	// dmsg stream, and the remote closing the stream propagates
	// EOF back to the CLI.
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
