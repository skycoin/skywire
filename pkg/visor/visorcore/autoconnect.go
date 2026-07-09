package visorcore

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Connector is the platform-neutral "connect to visors" primitive shared by the
// native visor (pkg/visor) and the browser wasm-visor. It owns the per-target
// transport-establishment logic (skip self / existing / same-LAN / dmsg-probe,
// then SaveTransport) parameterized over the transport TYPE, so the native shell
// can drive it with [SUDPH, STCPR] while the wasm shell can later drive it with
// [WS, WT] without the primitive hardcoding either. The platform-coupled pieces
// (public-visor sourcing, transport-discovery caching, the periodic loop) stay in
// the calling shell.
type Connector struct {
	Tm             *transport.Manager
	DmsgC          *dmsg.Client // for reachability probes
	ClientPublicIP string
	Log            *logging.Logger
}

// ConnectPhaseResult tracks the outcome of a connection phase.
type ConnectPhaseResult struct {
	Connected []cipher.PubKey
	Count     int
}

// ConnectToVisors attempts to establish transports of the given type to the given PKs.
// Skips self, existing transports, same-LAN visors, and non-SUDPH-capable visors.
// Returns the list of PKs we attempted (for tracking) and the count of new transports.
func (c *Connector) ConnectToVisors(
	ctx context.Context,
	selfPK cipher.PubKey,
	targets []cipher.PubKey,
	tpType tptypes.Type,
	existingByPK map[cipher.PubKey]map[tptypes.Type]bool,
	sudphCapable map[cipher.PubKey]struct{},
	maxCount int,
	currentCount int,
	trackAll bool, // if true, add to result even on failure (for phase 1 → phase 2 handoff)
) (result ConnectPhaseResult, err error) {
	for _, pk := range ShufflePubKeys(targets) {
		select {
		case <-ctx.Done():
			return result, context.Canceled
		default:
		}

		if currentCount+result.Count >= maxCount {
			break
		}

		if pk == selfPK {
			continue
		}

		// Skip if we already have this transport type to this visor
		if existingByPK[pk][tpType] {
			if trackAll {
				result.Connected = append(result.Connected, pk)
			}
			continue
		}

		// For SUDPH: skip visors not registered in address resolver
		if tpType == tptypes.SUDPH && sudphCapable != nil {
			if _, ok := sudphCapable[pk]; !ok {
				if trackAll {
					result.Connected = append(result.Connected, pk)
				}
				continue
			}
		}

		// Skip visors behind the same NAT
		if c.ClientPublicIP != "" {
			if sameLAN := c.isSameLAN(ctx, pk, tpType); sameLAN {
				c.Log.WithField("pk", pk).Debugln("Skipping same-LAN visor")
				continue
			}
		}

		logger := c.Log.WithField("pk", pk).WithField("type", string(tpType))

		// Probe the candidate on dmsg port 136 before attempting SUDPH
		// transports (which need DMSG for signaling). Skip the probe for
		// STCPR — it uses direct TCP and doesn't depend on DMSG. The old
		// blanket probe was filtering out visors reachable via STCPR but
		// with broken DMSG paths, reducing public visor transport counts.
		if tpType != tptypes.STCPR && c.DmsgC != nil {
			probeCtx, probeCancel := context.WithTimeout(ctx, dmsg.HandshakeTimeout)
			reachable := c.DmsgC.Probe(probeCtx, pk, skyenv.DmsgAwaitSetupPort)
			probeCancel()
			if !reachable {
				logger.Debug("Skipping visor: dmsg probe failed (unreachable)")
				if trackAll {
					result.Connected = append(result.Connected, pk)
				}
				continue
			}
		}

		logger.Debugln("Trying to add transport")

		if err := c.tryEstablishTransport(ctx, pk, tpType, logger); err != nil {
			if isContextError(err) {
				logger.WithError(err).Debugln("Transport creation canceled (shutdown)")
			} else {
				logger.WithError(err).Warnln("Failed to add transport")
			}
			if trackAll {
				result.Connected = append(result.Connected, pk)
			}
			continue
		}

		result.Count++
		result.Connected = append(result.Connected, pk)
	}

	return result, nil
}

// ShufflePubKeys returns the given slice shuffled in place using crypto/rand.
func ShufflePubKeys(keys []cipher.PubKey) []cipher.PubKey {
	n := len(keys)
	for i := n - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		keys[i], keys[j] = keys[j], keys[i]
	}
	return keys
}

// perAttemptTransportTimeout bounds a SINGLE autoconnect transport-establishment
// attempt. The autoconnect loop hands its visor-lifetime context straight down to
// SaveTransport, and a dial that neither completes nor errors blocks that call
// forever: SaveTransport waits on an unbuffered errCh fed by an async dial, and
// the WebRTC dialer's awaitConn only unblocks on connCh/errCh/ctx.Done() — so a
// last-resort Phase-4 WebRTC ICE negotiation to an unreachable peer (no answer,
// no error) with a never-canceled ctx wedges the whole loop: no further ticks
// fire and no transports are ever made (observed live, a v1.3.79 visor stuck here
// for 5+ days with 0 transports). Every dial path (stcpr DialContext, sudph, quic,
// webrtc awaitConn) honors ctx, so a bounded per-attempt deadline lets a stuck
// dial return DeadlineExceeded and the loop advances to the next target/tick.
const perAttemptTransportTimeout = 30 * time.Second

// tryEstablishTransport attempts to establish a transport of the specified type to the given public key, and return error.
func (c *Connector) tryEstablishTransport(ctx context.Context, pk cipher.PubKey, netType tptypes.Type, logger *logrus.Entry) error {
	ctx, cancel := context.WithTimeout(ctx, perAttemptTransportTimeout)
	defer cancel()

	if _, err := c.Tm.SaveTransport(ctx, pk, netType, transport.LabelAutomatic); err != nil {
		return err
	}

	logger.Debugln("Added transport to visor")
	return nil
}

// isSameLAN checks if the remote visor is behind the same NAT as us by comparing
// public IPs via the address resolver. Returns false on any error (fail-open).
func (c *Connector) isSameLAN(ctx context.Context, pk cipher.PubKey, netType tptypes.Type) bool {
	arClient := c.Tm.ARClient()
	if arClient == nil {
		return false
	}

	visorData, err := arClient.Resolve(ctx, string(netType), pk)
	if err != nil {
		return false
	}

	remoteIP := visorData.RemoteAddr
	if host, _, err := net.SplitHostPort(remoteIP); err == nil {
		remoteIP = host
	}

	return remoteIP != "" && remoteIP == c.ClientPublicIP
}

// isContextError returns true if the error is a context cancellation/deadline.
// net/http and url.Error wrap context errors with %w, so errors.Is unwraps to
// the original context.Canceled / context.DeadlineExceeded sentinel.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
