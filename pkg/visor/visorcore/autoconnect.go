// Package visorcore pkg/visor/visorcore/autoconnect.go c3-vis-core
package visorcore

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Connector is the platform-neutral "connect to visors" primitive shared by the
// native visor (pkg/visor) and the browser wasm-visor. It owns the per-target
// transport-establishment logic (skip self / existing / same-LAN, then
// SaveTransport) parameterized over the transport TYPE, so the native shell
// can drive it with [SUDPH, STCPR] while the wasm shell can later drive it with
// [WS, WT] without the primitive hardcoding either. The platform-coupled pieces
// (public-visor sourcing, transport-discovery caching, the periodic loop) stay in
// the calling shell.
type Connector struct {
	Tm    *transport.Manager
	DmsgC *dmsg.Client // for reachability probes
	Log   *logging.Logger

	// failed remembers (target, type) pairs whose last dial failed and when
	// they may be tried again — see autoconnect_backoff.go.
	failedMu sync.Mutex
	failed   map[dialKey]dialFailure
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
	// Dial the targets CONCURRENTLY (bounded). Each target costs a SaveTransport
	// (up to perAttemptTransportTimeout, 30s), so dialing them sequentially made
	// one slow/dead peer stall the whole phase for its full timeout — the measured
	// cause of ~26s to the first
	// autoconnect transport. A bounded worker pool overlaps the waits so the phase
	// finishes in ~the slowest single dial rather than their sum, while the
	// semaphore keeps a visor from opening dozens of handshakes at once.
	//
	// The read-only inputs (existingByPK, sudphCapable) are dialed without a lock;
	// mu guards the mutated result (Count/Connected) and the budget read. The budget
	// (maxCount) is still honored, but because successes register asynchronously the
	// stop point can overshoot by up to dialConcurrency-1 in-flight dials — a couple
	// of extra transports is harmless (and mildly beneficial for path diversity).
	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	sem := make(chan struct{}, dialConcurrency)

	// A budgeted phase (maxCount > 0 — the few WT/WS/QUIC slots, and for a
	// browser visor with no raw sockets its ONLY transports) spends its slots on
	// distinct MACHINES. Public visors are often several to a host, so random
	// picks put most of a tab's handful of relays on one machine, and when that
	// machine dropped its sessions the tab lost nearly every relay at once —
	// every leg of every tunnel. hosts is seeded with the hosts our automatic
	// transports already reach; a new transport to one of them does not count.
	var hosts map[string]bool
	sameHostExtra := 0
	if maxCount > 0 {
		hosts = c.automaticTransportHosts()
	}

	for _, pk := range ShufflePubKeys(targets) {
		select {
		case <-ctx.Done():
			wg.Wait()
			return result, context.Canceled
		default:
		}

		mu.Lock()
		// maxCount <= 0 means unlimited: the direct carriers (stcpr, sudph) are no
		// longer budget-limited, so a phase can enumerate every target.
		reached := maxCount > 0 && currentCount+result.Count >= maxCount
		mu.Unlock()
		if reached {
			break
		}

		if pk == selfPK {
			continue
		}
		// A target whose last dial failed waits out its backoff (autoconnect_backoff.go).
		if c.inBackoff(pk, tpType, time.Now()) {
			if trackAll {
				mu.Lock()
				result.Connected = append(result.Connected, pk)
				mu.Unlock()
			}
			continue
		}

		// Skip if we already have this transport type to this visor
		if existingByPK[pk][tpType] {
			if trackAll {
				mu.Lock()
				result.Connected = append(result.Connected, pk)
				mu.Unlock()
			}
			continue
		}

		// For SUDPH: skip visors not registered in address resolver
		if tpType == tptypes.SUDPH && sudphCapable != nil {
			if _, ok := sudphCapable[pk]; !ok {
				if trackAll {
					mu.Lock()
					result.Connected = append(result.Connected, pk)
					mu.Unlock()
				}
				continue
			}
		}

		wg.Add(1)
		sem <- struct{}{}
		go func(pk cipher.PubKey) {
			defer wg.Done()
			defer func() { <-sem }()
			// Recover per-dial so one bad AR reply / dial panic can't take down
			// the whole autoconnect loop.
			defer func() {
				if r := recover(); r != nil {
					c.Log.WithField("pk", pk).Errorf("autoconnect dial recovered panic: %v", r)
				}
			}()

			logger := c.Log.WithField("pk", pk).WithField("type", string(tpType))

			logger.Debugln("Trying to add transport")

			if err := c.tryEstablishTransport(ctx, pk, tpType, logger); err != nil {
				if isContextError(err) {
					logger.WithError(err).Debugln("Transport creation canceled (shutdown)")
				} else {
					wait := c.noteFailure(pk, tpType, time.Now())
					logger.WithError(err).WithField("retry_in", wait).Warnln("Failed to add transport")
				}
				if trackAll {
					mu.Lock()
					result.Connected = append(result.Connected, pk)
					mu.Unlock()
				}
				return
			}

			// A transport to a host we already reach is KEPT — it is still another
			// relay, and a browser visor may have few machines to choose from —
			// but it does not use up a budget slot, so the phase keeps dialing for
			// a new machine. sameHostExtra bounds those extras to maxCount, so a
			// phase opens at most 2*maxCount transports.
			counts := true
			if hosts != nil {
				if tp, err := c.Tm.GetTransport(pk, tpType); err == nil && tp != nil {
					if h := tp.RemoteIP(); h != "" {
						mu.Lock()
						if hosts[h] && sameHostExtra < maxCount {
							sameHostExtra++
							counts = false
						}
						hosts[h] = true
						mu.Unlock()
						if !counts {
							logger.WithField("host", h).Debugln("Transport reaches a host we already reach; kept, but dialing on for another machine")
						}
					}
				}
			}

			mu.Lock()
			c.noteSuccess(pk, tpType)
			if counts {
				result.Count++
			}
			result.Connected = append(result.Connected, pk)
			mu.Unlock()
		}(pk)
	}

	wg.Wait()
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

// dialConcurrency bounds how many autoconnect dials run at once within a single
// ConnectToVisors phase. Chosen to overlap the per-target waits (up to a
// 30s SaveTransport) without letting a visor open an unbounded number of
// simultaneous transport handshakes.
const dialConcurrency = 8

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

// isContextError returns true if the error is a context cancellation/deadline.
// net/http and url.Error wrap context errors with %w, so errors.Is unwraps to
// the original context.Canceled / context.DeadlineExceeded sentinel.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

// automaticTransportHosts is the set of hosts the automatic transports already
// reach (their remote IP, or the hostname a browser carrier dialed).
func (c *Connector) automaticTransportHosts() map[string]bool {
	hosts := make(map[string]bool)
	if c.Tm == nil {
		return hosts
	}
	for _, tp := range c.Tm.GetTransportsByLabel(transport.LabelAutomatic) {
		if h := tp.RemoteIP(); h != "" {
			hosts[h] = true
		}
	}
	return hosts
}
