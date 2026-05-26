// Package visor pkg/visor/rpc_bridge.go
//
// TCP→{dmsg,skynet} bridge for the CLI's `--via <scheme>://<pk>`
// flow. Multiplexed onto the visor's existing CLI RPC port
// (v.conf.CLIAddr, default localhost:3435) via cmux: any conn
// starting with the 6-byte magic bridgeMagic is routed here,
// everything else falls through to gRPC or net/rpc.
//
// Header (sent by the CLI after cmux matched the magic prefix):
//
//	6 bytes — magic ("SKYBRI"). cmux peeks this but doesn't
//	          consume it, so we read all 42 bytes ourselves.
//	1 byte  — scheme: 0 = dmsg, 1 = skynet
//	33 bytes — target visor PK
//	2 bytes — little-endian uint16. Dmsg port for scheme=0
//	          (typically DmsgVisorRPCPort = 44). Ignored for
//	          scheme=1.
//
// Total: 42 bytes.
//
// After header is read, the visor opens the appropriate stream
// using ITS OWN identity (dmsg client for scheme=0, transport-RPC
// mux for scheme=1), then bidirectionally io.Copy's between the
// TCP conn and the stream. CLI's rpc.Client runs on the TCP conn;
// remote's rpc.Server runs on the other side of the stream; both
// speak gob, no protocol translation. CLI inherits the visor's
// whitelist eligibility on the remote without needing its own
// keypair.
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

// bridgeMagic is the 6-byte cmux-matched prefix. Chosen so it
// can't collide with gob's first bytes (used by net/rpc) or the
// HTTP/2 preface (used by gRPC).
const bridgeMagic = "SKYBRI"

const (
	bridgeSchemeDmsg   byte = 0
	bridgeSchemeSkynet byte = 1

	// bridgeHeaderSize = 6 magic + 1 scheme + 33 PK + 2 port = 42.
	bridgeHeaderSize = 42

	// bridgeHeaderTimeout caps how long the visor waits for the
	// CLI to send the header. A real CLI sends it immediately
	// after connecting; a stalled connection without the header
	// is a misbehaving caller and should be closed quickly.
	bridgeHeaderTimeout = 5 * time.Second

	// bridgeDialTimeout caps the time spent opening the dmsg or
	// skynet stream to the target peer. dmsg-discovery / transport
	// auto-create usually finish well under this; a longer wait
	// means the target is offline.
	bridgeDialTimeout = 15 * time.Second
)

// serveRPCBridge accepts conns on lis (which was cmux-matched by
// the bridgeMagic prefix) and dispatches each to the dmsg or
// skynet stream opener based on the header's scheme byte.
func serveRPCBridge(ctx context.Context, log *logging.Logger, lis net.Listener, v *Visor) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			log.WithError(err).Debug("rpc bridge accept error")
			continue
		}
		go handleBridgeConn(ctx, log, conn, v)
	}
}

func handleBridgeConn(ctx context.Context, log *logging.Logger, tcpConn net.Conn, v *Visor) {
	defer tcpConn.Close() //nolint:errcheck,gosec

	if err := tcpConn.SetReadDeadline(time.Now().Add(bridgeHeaderTimeout)); err != nil {
		log.WithError(err).Debug("rpc bridge: set header deadline")
		return
	}
	header := make([]byte, bridgeHeaderSize)
	if _, err := io.ReadFull(tcpConn, header); err != nil {
		log.WithError(err).Debug("rpc bridge: read header")
		return
	}
	if err := tcpConn.SetReadDeadline(time.Time{}); err != nil {
		log.WithError(err).Debug("rpc bridge: clear header deadline")
		return
	}

	if string(header[:6]) != bridgeMagic {
		log.WithField("got", string(header[:6])).Warn("rpc bridge: magic prefix mismatch")
		return
	}
	scheme := header[6]
	var targetPK cipher.PubKey
	copy(targetPK[:], header[7:40])
	targetPort := binary.LittleEndian.Uint16(header[40:42])

	var stream io.ReadWriteCloser
	var err error
	switch scheme {
	case bridgeSchemeDmsg:
		stream, err = openDmsgStream(ctx, v.dmsgC, targetPK, targetPort)
	case bridgeSchemeSkynet:
		stream, err = openSkynetStream(v, targetPK)
	default:
		log.WithField("scheme", scheme).Warn("rpc bridge: unsupported scheme")
		return
	}
	if err != nil {
		log.WithError(err).
			WithField("scheme", scheme).
			WithField("target_pk", targetPK.String()).
			Debug("rpc bridge: open stream failed")
		return
	}
	defer stream.Close() //nolint:errcheck,gosec

	log.WithField("scheme", scheme).
		WithField("target_pk", targetPK.String()).
		Debug("rpc bridge: stream established")

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stream, tcpConn) //nolint:errcheck,gosec
		_ = stream.Close()              //nolint:errcheck,gosec
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(tcpConn, stream) //nolint:errcheck,gosec
		_ = tcpConn.Close()             //nolint:errcheck,gosec
	}()
	wg.Wait()
}

// openDmsgStream opens a dmsg stream from the visor's own dmsg
// client to (targetPK, targetPort).
func openDmsgStream(ctx context.Context, dmsgC *dmsg.Client, targetPK cipher.PubKey, targetPort uint16) (io.ReadWriteCloser, error) {
	if dmsgC == nil {
		return nil, errors.New("dmsg client not available")
	}
	dialCtx, cancel := context.WithTimeout(ctx, bridgeDialTimeout)
	defer cancel()
	return dmsgC.Dial(dialCtx, dmsg.Addr{PK: targetPK, Port: targetPort})
}

// openSkynetStream opens a VStream via the visor's transport-RPC
// mux to targetPK on route ID 0 (VisorRPCPacket). Auto-creates a
// stcpr or sudph transport if one doesn't already exist (same
// stcpr→sudph fallback as autoUpgradeHypervisorTransport).
func openSkynetStream(v *Visor, targetPK cipher.PubKey) (io.ReadWriteCloser, error) {
	if v.transportRPCMux == nil {
		return nil, errors.New("transport-RPC mux not initialized (no hypervisors or dmsgpty whitelist configured)")
	}
	if err := v.ensureFastTransport(targetPK); err != nil {
		return nil, err
	}
	return v.transportRPCMux.Dial(targetPK)
}
