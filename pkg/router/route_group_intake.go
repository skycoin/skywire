// Package router pkg/router/route_group_intake.go c2-net-routing
package router

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// handlePacket accepts one inbound packet from the router's shared transport
// read loop. It NEVER blocks: the packet is queued onto this group's intake
// queue and handled by the group's own worker (serveIntake).
//
// A full queue drops the packet instead of waiting. That is safe for a mux
// group (the SACK/retransmit layer recovers the frame) and no worse than the
// pre-existing "readCh full for 30 s, drop" for a legacy group — while waiting
// here would be exactly the all-paths stall this queue exists to prevent.
//
// Close packets bypass the queue and run inline, as they did before: a close
// must take effect even when the worker is parked on a full readCh (the worker
// leaves that select on rg.closed / rg.remoteClosed, which handleClosePacket
// sets), and a queued close would be dropped by a full queue — leaving a dead
// group alive until the keep-alive GC reaped it.
func (rg *RouteGroup) handlePacket(packet routing.Packet) error {
	// An inbound packet is what opens a reorder gap and what moves the ack
	// frontier, so it both records receive activity (which holds the gated loops
	// awake for the grace period, so a download does not park and unpark them
	// per packet) and releases them if they are already parked. One atomic store
	// plus one relaxed load when nothing is parked.
	rg.lastRecv.Store(time.Now().UnixNano())
	rg.signalServiceWake()

	if packet.Type() == routing.ClosePacket {
		return rg.handlePacketNow(packet)
	}

	select {
	case <-rg.closed:
		return io.ErrClosedPipe
	case <-rg.remoteClosed:
		return io.ErrClosedPipe
	default:
	}

	select {
	case rg.inCh <- packet:
		return nil
	default:
		rg.noteIntakeDrop(packet)
		return nil
	}
}

// serveIntake drains this route group's intake queue, one packet at a time, in
// arrival order. One worker per group: whatever the handler blocks on —  a full
// readCh, rg.mu held across a transport write — is confined to this group.
func (rg *RouteGroup) serveIntake() {
	for {
		select {
		case <-rg.closed:
			return
		case <-rg.remoteClosed:
			return
		case packet := <-rg.inCh:
			if err := rg.handlePacketNow(packet); err != nil {
				rg.logger.WithError(err).
					Debugf("Intake worker failed to handle %s packet", packet.Type())
			}
		}
	}
}

// noteIntakeDrop counts a packet dropped by a full intake queue and warns at
// most once per intakeWarnInterval for this group.
func (rg *RouteGroup) noteIntakeDrop(packet routing.Packet) {
	drops := rg.intakeDrops.Add(1)

	now := time.Now().UnixNano()
	last := rg.intakeWarnAt.Load()
	if now-last < int64(intakeWarnInterval) || !rg.intakeWarnAt.CompareAndSwap(last, now) {
		return
	}

	rg.logger.WithField("drops", drops).
		WithField("type", packet.Type().String()).
		WithField("capacity", cap(rg.inCh)).
		Warn("Dropping inbound packet: route group intake queue full (application not reading)")
}

// intakeQueue is the depth, capacity and lifetime drop count of the group's
// inbound intake queue.
func (rg *RouteGroup) intakeQueue() (queue, capacity int, drops uint64) {
	return len(rg.inCh), cap(rg.inCh), rg.intakeDrops.Load()
}

// handlePacketNow is the synchronous packet handler. It runs on the group's own
// intake worker (and inline for close packets); it may block — on readCh or on
// rg.mu — which is precisely why it is not called from the router's shared loop.
func (rg *RouteGroup) handlePacketNow(packet routing.Packet) error {
	switch packet.Type() {
	case routing.ClosePacket:
		closeCode, err := closeCodeFromPacket(packet)
		if err != nil {
			return err
		}
		return rg.handleClosePacket(closeCode)
	case routing.DataPacket:
		// IN-BAND leg control (mux_control_frame.go): a re-home/split message
		// rides a DataPacket so every relay forwards it. Dispatched here,
		// before anything reads it as application data — it must not flip the
		// "first packet is data, so the peer is an old visor" inference below,
		// and it never reaches the reorder buffer, the SACK tracker or the
		// delivery CRC.
		if rg.isMuxControlPacket(packet) {
			return rg.handleMuxControlFrame(packet)
		}
		rg.handshakeProcessedOnce.Do(func() {
			// first packet is data packet, so we're communicating with the old visor
			rg.encrypt = false
			close(rg.handshakeProcessed)
		})
		return rg.handleDataPacket(packet)
	case routing.RepairPacket:
		return rg.handleRepairPacket(packet)
	case routing.LegStatePacket:
		return rg.handleLegStatePacket(packet)
	case routing.DirectionPacket:
		return rg.handleDirectionPacket(packet)
	case routing.LegRehomePacket:
		return rg.handleLegRehomePacket(packet)
	case routing.HandshakePacket:
		// A handshake on an aux leg proves the peer registered that leg's
		// rule, so it is safe to start sending on it. The primary leg's
		// handshake (which creates the mux below) needs no marking — leg 0
		// is ready from growLegs. This matters most for download-heavy
		// flows: the bulk-sending side would otherwise never receive data
		// on its aux legs and so never learn it may spread onto them.
		if rg.mux != nil {
			rg.mu.Lock()
			rid := packet.RouteID()
			readyLeg := -1
			for i, rule := range rg.rvs {
				if rule != nil && rule.KeyRouteID() == rid {
					rg.mux.markLegReady(i)
					readyLeg = i
					break
				}
			}
			rg.mu.Unlock()
			// Signal the just-ready leg's BORN-standby state to the peer. An aux
			// leg grown into the warm pool (standbyNewLegs) is standby WITHOUT ever
			// passing through the rotation engine's demote path — the only place
			// sendLegState was previously called — so the peer (accept side, no
			// standby of its own) kept striping its send traffic across it. Now that
			// the leg's rules are peer-confirmed (this handshake), tell the peer to
			// park it too, so the bulk-sending direction is bounded to the active
			// set. Only signals standby legs; leg 0 / active legs are the peer's
			// default, and the accept side (all-active) never enters this branch.
			// Sent after the rg.mu unlock — sendLegState takes rg.mu itself.
			if readyLeg > 0 && rg.mux.isLegStandby(readyLeg) {
				rg.sendLegState(readyLeg, true)
			}
		}
		firstHandshake := false
		rg.handshakeProcessedOnce.Do(func() {
			firstHandshake = true
			// first packet is handshake packet, so we're communicating with the new visor
			rg.encrypt = true
			if packet.Payload()[0] == 0 {
				rg.encrypt = false
			}

			// Extended capabilities negotiation
			remoteCaps := packet.HandshakeCapabilities()
			if remoteCaps&routing.CapMux != 0 {
				sack := remoteCaps&routing.CapSACK != 0
				rg.mux = newRouteMux(rg.logger, sack)
				// One holder for the group and its mux, so `route settings --app <n>`
				// moves the dataplane and the service loops together.
				rg.mux.knobHolder = rg.knobHolder
				// The mux's own latency signal is the first-hop transport RTT,
				// which says nothing about a multihop leg's far side. Give it
				// the end-to-end route latency the leg-liveness pongs already
				// measure, so the no-direct-leg forward confinement can pick
				// the fastest leg rather than whichever was added first.
				rg.mux.SetLegLatencyFn(rg.legEndToEndLatencyMs)
				// The forward direction rides one leg; every move of it is
				// recorded, so `visor state --select diag` answers "which leg is
				// the upload on, and why did it change".
				rg.mux.SetForwardRehomeFn(rg.noteForwardRehome)
				// …and when a sustained upload widens it over that leg's
				// siblings, or the load subsides and it narrows back.
				rg.mux.SetForwardFanoutFn(rg.noteForwardFanout)
				// …and a leg cut to a probe (or given its share back) is
				// recorded there too, so a collapse onto one bad leg says so.
				rg.mux.SetLegProbeRulingFn(rg.noteLegProbeRuling)
				// Every SACK's per-leg send→ack delay also feeds the leg's
				// shared-bottleneck window (rate-limited to one sample per
				// SBDSampleInterval), so the detector can rule while a transfer
				// is running instead of after sbdMinSamples liveness pongs.
				rg.mux.onLegDelaySample = rg.foldLegDelaySample
				// If a promoting rotation engine was already wired (SetRotation
				// before the handshake), route new aux legs through warm standby
				// on add. Set BEFORE growLegs so it governs any aux legs already
				// present in tps[]; the primary (index 0) is always active.
				if rg.standbyNewLegs {
					rg.mux.SetStandbyNewLegs(true)
				}
				// rg.tps already has the primary transport at this
				// point; size the leg counters to match. Subsequent
				// AppendRoute calls (mux 2/N+) extend the slice via
				// appendRules.
				rg.mux.growLegs(len(rg.tps))
				rg.logger.Debug("Route multiplexing enabled (both peers support CapMux)")
				if sack {
					rg.logger.Debug("SACK retransmission enabled (both peers support CapSACK)")
					go rg.servicePacketLoop("sack", rg.cfg.KeepAliveInterval/2, rg.sackServiceFn, nil)
					// Tail-Loss Probe: re-send the in-flight tail after an idle PTO so a
					// lost burst-tail (which the receiver never reports — its bitmap ends
					// at the last seq it got) recovers in one PTO instead of stalling until
					// the retx entry ages out (RFC 8985). Reuses the SACK retransmit path.
					// Gated: a tail probe needs an outstanding tail, so this is a
					// no-op whenever the retransmit buffer is empty.
					go rg.serviceKnobLoopGated("tlp", routersettings.TLPCheckInterval, rg.tlpServiceFn, rg.muxDormant)
					// Proactive HoL retransmit reuses the SACK channel, so it is only
					// enabled when SACK is too. Both peers must advertise CapHOLRetx;
					// otherwise the group keeps the reactive SACK behavior.
					if remoteCaps&routing.CapHOLRetx != 0 {
						rg.mux.holRetxEnabled = true
						rg.logger.Debug("Proactive HoL retransmit enabled (both peers support CapHOLRetx)")
					}
				}

				// Leg-state signaling negotiation. Both edges must advertise
				// CapLegState; then a park/promote is mirrored to the remote so it
				// stops striping its send traffic across a leg the other end parked
				// (the wide-mux download stall). No new loop — it rides the existing
				// rotation park/promote path. A peer without the bit never receives a
				// LegStatePacket and keeps the send-side-only standby behavior.
				if remoteCaps&routing.CapLegState != 0 {
					rg.mux.legStateEnabled = true
					rg.logger.Debug("Leg-state signaling enabled (both peers support CapLegState)")
					if !rg.initiator {
						// Acceptor: the initiator owns the active set and promotes the
						// legs it wants via the mirror. Default every aux leg to STANDBY
						// (born-standby + park any already present) so we never send the
						// bulk direction across a leg the initiator hasn't activated. The
						// mirror's active signals + periodic resync then promote exactly
						// the initiator's active set. Without this the acceptor births aux
						// legs ACTIVE (no promoting engine → standbyNewLegs stays false)
						// and sprays the download across every established leg until the
						// lossy event mirror parks it — measured the acceptor holding 23
						// active while the initiator had 2 (~11x reorder over-subscription,
						// wedge). Complements honorsMirrorActiveSet (#4351), which stops the
						// acceptor's OWN controllers from re-admitting.
						rg.mux.SetStandbyNewLegs(true)
						rg.mux.parkAllAuxStandby()
					}
				}

				// Unidirectional send selection negotiation. Both edges must
				// advertise CapUniDir; then each end restricts its own send to legs
				// matching its direction (initiator→direct upload, acceptor→multihop
				// download by default), so the two directions ride disjoint legs
				// instead of both striping every leg. Local decision from role + leg
				// directness; the endpoints identify a direct leg (its transport's
				// remote is one of them).
				if remoteCaps&routing.CapUniDir != 0 {
					rg.mux.setDirectional(rg.initiator, rg.desc.DstPK(), rg.desc.SrcPK())
					rg.logger.Debug("Unidirectional send selection enabled (both peers support CapUniDir)")
				}

				// Leg re-home negotiation. Both edges must advertise CapLegRehome;
				// then a STANDBY group's whole chain can be adopted as one of this
				// group's mux legs by rewriting its consume rule at each edge, with
				// no setup-node dial. Inert until an operator or the pool asks for
				// one; see leg_rehome.go.
				if remoteCaps&routing.CapLegRehome != 0 {
					rg.mux.legRehomeEnabled = true
					rg.logger.Debug("Leg re-home enabled (both peers support CapLegRehome)")
				}

				// Delivery-check negotiation. Both edges must advertise
				// CapDeliveryCRC, and we must still want it ourselves — the knob is
				// read here as well as at advertise time so a group born while it was
				// off never strips a trailer its peer was not told to stamp. From now
				// on every data frame this group sends carries a CRC32C over
				// (seq ‖ payload) and every frame it delivers is verified against it.
				if remoteCaps&routing.CapDeliveryCRC != 0 && DeliveryCRCAdvertised() {
					rg.mux.deliveryCRC = true
					rg.mux.groupPort = rg.desc.SrcPort()
					rg.logger.Debug("Delivery CRC enabled (both peers support CapDeliveryCRC)")
				}

				// FEC negotiation. Requires CapMux (rg.mux set above); enabled purely
				// when both edges advertised CapFEC — no flag gate, like every other
				// mux capability (CapFEC is advertised unconditionally above). Enables
				// block erasure coding so a gap-blocked reorder frontier is
				// reconstructed from repair frames on fast legs instead of waiting on
				// the slow leg.
				if remoteCaps&routing.CapFEC != 0 {
					rg.mux.fecEnabled = true
					rg.mux.fecInit()
					if rg.mux.fecEnabled {
						rg.logger.Debug("FEC enabled (both peers support CapFEC)")
						// Tail protection: flush a pending partial block on idle so
						// the final <K frames of a transfer gain repair coverage.
						go rg.servicePacketLoop("fec-flush", fecDefaultIdleFlush, rg.fecFlushServiceFn, nil)
					}
				}

				// Per-frame noise negotiation (both edges advertised CapPerFrameNoise
				// and we want it). Piggybacked on the same handshake as the KK
				// message. Wiring is SYNCHRONOUS here so perFrameNoiseActive is set
				// before handshakeProcessed closes and saveRouteGroupRules decides
				// whether to bypass EncryptConn.
				if rg.perFrameNoiseWant && rg.encrypt && remoteCaps&routing.CapPerFrameNoise != 0 {
					noiseMsg := packet.HandshakeNoisePayload()
					if rg.initiator {
						// Reverse handshake: process the responder's msg2 (our ns
						// exists from sending msg1) and wire on completion.
						if rg.ns != nil {
							if err := rg.ns.ProcessHandshakeMessage(noiseMsg); err != nil {
								rg.logger.Warnf("per-frame noise: process responder msg2 failed: %v", err)
							} else if rg.ns.HandshakeFinished() {
								rg.wirePerFrameNoise()
							}
						}
					} else {
						// Forward handshake: create our session, process the
						// initiator's msg1, produce+cache msg2 (this finishes our
						// cipher and wires the mux synchronously), then emit the
						// reverse handshake carrying msg2 from a goroutine so we
						// never block the router read loop.
						if ns, err := noise.New(noise.HandshakeKK, rg.nsConf); err != nil {
							rg.logger.Warnf("per-frame noise: session create failed: %v", err)
						} else {
							rg.ns = ns
							if err := rg.ns.ProcessHandshakeMessage(noiseMsg); err != nil {
								rg.logger.Warnf("per-frame noise: process initiator msg1 failed: %v", err)
								rg.ns = nil
							} else if _, err := rg.nextPerFrameNoiseMsg(); err != nil {
								rg.logger.Warnf("per-frame noise: produce responder msg2 failed: %v", err)
								rg.ns = nil
							} else {
								go func() {
									if err := rg.sendHandshake(rg.encrypt); err != nil {
										rg.logger.Debugf("per-frame noise: reverse handshake send failed: %v", err)
									}
								}()
							}
						}
					}
				}
			}

			close(rg.handshakeProcessed)
		})
		// A duplicate forward handshake means the initiator's retransmit loop is
		// still running because its reciprocal (reverse) handshake never arrived —
		// most likely that single reverse-ack packet was lost. Re-emit our reverse
		// handshake so the initiator's retransmit can complete instead of eating
		// the whole handshake-await timeout. Only the responder (which sends its
		// handshake in reply to the forward one) re-acks; the initiator's own
		// resends are what drive this. Sent from a goroutine so we never block the
		// router read loop, and never while holding rg.mu (sendHandshake locks it).
		if !firstHandshake && !rg.initiator {
			go func() {
				if err := rg.sendHandshake(rg.encrypt); err != nil {
					rg.logger.Debugf("Failed to re-ack duplicate handshake: %v", err)
				}
			}()
		}
	case routing.PingPacket:
		return rg.handlePingPacket(packet)
	case routing.PongPacket:
		return rg.handlePongPacket(packet)
	case routing.ErrorPacket:
		return rg.handleErrorPacket(packet)
	case routing.SACKPacket:
		return rg.handleSACKPacket(packet)
	}

	return nil
}

func (rg *RouteGroup) handleDataPacket(packet routing.Packet) (err error) {
	// in this case remote is already closed, and `readCh` is closed too,
	// but some packets may still reach the rg causing panic on writing
	// to `readCh`, so we simple omit such packets
	if rg.isRemoteClosed() {
		return nil
	}

	// Defensive recover. readCh is no longer closed on route-group close (closure
	// is signaled via rg.closed / rg.remoteClosed — see close()), so the old
	// "send on closed channel" panic can no longer occur here. The recover is kept
	// purely as a safety net: an unexpected panic in this hot packet-dispatch path
	// drops the packet and keeps the router goroutine serving other route groups
	// rather than crashing the whole visor.
	defer func() {
		if r := recover(); r != nil {
			rg.logger.WithField("recover", r).Debug("handleDataPacket: recovered from panic")
			err = io.ErrClosedPipe
		}
	}()

	rg.networkStats.AddBandwidthReceived(uint64(packet.Size()))

	// Per-mux-leg recv counter. We resolve the leg from the
	// packet's route ID (which matches one of rg.rvs[].KeyRouteID,
	// each leg has its own consume rule). The lookup is O(legs)
	// but legs is small (typically 1-16) and this only runs for
	// data packets after we've already paid for noise decryption.
	arrivalLeg := -1
	if rg.mux != nil {
		rg.mu.Lock()
		rid := packet.RouteID()
		for i, rule := range rg.rvs {
			if rule != nil && rule.KeyRouteID() == rid {
				rg.mux.recordRecv(i, uint64(packet.Size()))
				arrivalLeg = i
				// Inbound traffic on leg i proves the peer registered its
				// rule for this leg, so it is now safe to send on it.
				rg.mux.markLegReady(i)
				break
			}
		}
		rg.mu.Unlock()
	}

	if rg.mux != nil {
		seq := packet.SequenceNumber()
		data := packet.DataPayloadAfterSeq()

		// arrivalLeg lets deliverData credit this leg's UNIQUE payload share
		// (first arrival of a seq), the confound-free per-direction attribution.
		delivered, gapDetected := rg.mux.deliverData(arrivalLeg, seq, data)

		// A gap is the normal case for mux (legs interleave), so rate-limit the
		// SACK: without this, latency-skew reordering fires a SACK goroutine per
		// out-of-order packet — thousands per second under load.
		if gapDetected && rg.mux.shouldSendSACK() {
			go rg.sendSACK() //nolint:errcheck
		} else if len(delivered) > 0 {
			// Delayed ack for CLEAN in-order delivery (RFC 1122 spirit). Without
			// it, gap-free traffic is acknowledged only by the periodic SACK
			// service (KeepAliveInterval/2 ≈ 15s), so on a trickle — keepalives,
			// status polls — every frame sat unacked long past the sender's
			// TLP/RACK horizons and was re-sent up to its backoff cap: measured
			// ~2,500 spurious retransmits (plus their dup/repair echoes) on ONE
			// IDLE route overnight, and Karn's rule then starves the ack-delay
			// EWMA of samples so the sender can never learn better. One SACK
			// ~100ms after in-order data (coalescing anything else that arrives
			// meanwhile) purges the sender's buffer at ~RTT, keeps TLP quiet,
			// and feeds the RACK basis honest samples.
			rg.scheduleDelayedAck()
		}

		// Proactive HoL nudge (CapHOLRetx). When the reorder frontier has stayed
		// gap-blocked for ~one fastest-live-leg RTT (low floor of a few ms), promptly
		// send the sender a SACK so it fast-retransmits the stuck frontier seq on a
		// fast leg — bounding the stall to a fast-leg RTT instead of the reactive
		// retxMinAge/reorderTimeout waits. Cheap-guarded (only when a gap is actually
		// buffered) so the common in-order path pays nothing, and rate-limited to one
		// nudge per fast-leg RTT via its own holSACKNano clock. Arrival-driven: the
		// fast legs keep delivering out-of-order frames while blocked, so this fires
		// promptly once the threshold passes without needing a tight timer.
		if rg.mux.holRetxEnabled && rg.mux.reorderPending() > 0 {
			rg.mu.Lock()
			fastMs := rg.mux.fastestLegLatency(rg.tps)
			rg.mu.Unlock()
			interval := rg.mux.holPerSeqInterval(fastMs)
			if rg.mux.gapAge() > rg.mux.holGapThreshold(fastMs) && rg.mux.shouldSendHolSACK(interval) {
				go rg.sendSACK() //nolint:errcheck
			}
		}

		for _, d := range delivered {
			// FEC padding frames carry an empty payload (the app never writes a
			// zero-length frame — Write rejects len==0 — so an empty delivered
			// payload is unambiguously FEC tail-flush padding). They exist only to
			// complete a partial block and advance the reorder frontier; drop them
			// from the app stream instead of surfacing a spurious 0-byte read.
			if len(d) == 0 {
				continue
			}
			// Both rg.closed (local-initiated close) and
			// rg.remoteClosed (remote-initiated close) must be
			// watched here; the latter was previously missing
			// and caused the send-on-closed-channel panic
			// documented in the function-level defer.
			select {
			case <-rg.closed:
				return io.ErrClosedPipe
			case <-rg.remoteClosed:
				return io.ErrClosedPipe
			case rg.readCh <- d:
			case <-time.After(30 * time.Second):
				rg.logger.Warn("Dropping packet: readCh full for 30s (application not reading)")
			}
		}
		return nil
	}

	// Legacy path: deliver payload directly
	select {
	case <-rg.closed:
		return io.ErrClosedPipe
	case <-rg.remoteClosed:
		return io.ErrClosedPipe
	case rg.readCh <- packet.Payload():
	case <-time.After(30 * time.Second):
		rg.logger.Warn("Dropping packet: readCh full for 30s (application not reading)")
	}

	return nil
}

// handleRepairPacket records an incoming FEC repair symbol and delivers any
// frontier frames the reassembler can now reconstruct (opened, in order). A
// repair for a group that never negotiated FEC is ignored. Mirrors
// handleDataPacket's readCh delivery.
func (rg *RouteGroup) handleRepairPacket(packet routing.Packet) error {
	if rg.mux == nil || !rg.mux.fecEnabled {
		return nil
	}
	rg.mux.fecRepairBytesRecv.Add(uint64(packet.Size()))
	// Per-leg repair accounting: resolve the arrival leg from the packet's route
	// ID (same reverse-rule match as handleDataPacket) and credit both its recv
	// counters and the repair-specific one — before this, FEC repairs were
	// invisible in the per-leg telemetry, so a leg carrying repair overhead was
	// indistinguishable from an idle one.
	rg.mu.Lock()
	rid := packet.RouteID()
	for i, rule := range rg.rvs {
		if rule != nil && rule.KeyRouteID() == rid {
			rg.mux.recordRecv(i, uint64(packet.Size()))
			rg.mux.recordRepair(i, uint64(packet.Size()))
			break
		}
	}
	rg.mu.Unlock()
	delivered := rg.mux.fecOnRecvRepair(packet.RepairBlockID(), packet.RepairIndex(), packet.RepairSymLen(), packet.RepairSymbol())
	for _, d := range delivered {
		if len(d) == 0 { // FEC padding frame — advances the frontier, not app data
			continue
		}
		select {
		case <-rg.closed:
			return io.ErrClosedPipe
		case <-rg.remoteClosed:
			return io.ErrClosedPipe
		case rg.readCh <- d:
		case <-time.After(30 * time.Second):
			rg.logger.Warn("Dropping FEC-reconstructed packet: readCh full for 30s (application not reading)")
		}
	}
	return nil
}

// handleLegStatePacket mirrors a peer's leg active/standby decision onto this
// side's mux, so a leg the peer parked is also parked HERE — i.e. this side stops
// striping its send traffic across it. The leg is identified by the route ID the
// packet arrived on (the peer sent it ON that leg), matched against the reverse
// rules exactly like an aux-leg handshake (markLegReady). Ignored unless
// CapLegState was negotiated. Never parks leg 0 (setLegStandby enforces that).
func (rg *RouteGroup) handleLegStatePacket(packet routing.Packet) error {
	if rg.mux == nil || !rg.mux.legStateEnabled {
		return nil
	}
	standby := packet.LegStateStandby()
	rid := packet.RouteID()
	rg.mu.Lock()
	idx := -1
	for i, rule := range rg.rvs {
		if rule != nil && rule.KeyRouteID() == rid {
			idx = i
			break
		}
	}
	rg.mu.Unlock()
	if idx < 0 {
		return nil // no matching leg (already pruned / unknown route)
	}
	// The peer re-broadcasts its COMPLETE set every legStateResyncInterval, so only
	// a real transition is an event — otherwise a steady state would post one every
	// tick and flood the 256-deep ring.
	changed := rg.mux.isLegStandby(idx) != standby
	rg.mux.setLegStandby(idx, standby)
	rg.notePeerLegState(idx, standby)
	if rg.logger != nil {
		rg.logger.Debugf("LegState: peer marked leg %d %s", idx, map[bool]string{true: "standby", false: "active"}[standby])
	}
	if changed {
		// A leg parked/promoted from the far end is a first-class lifecycle event:
		// it changes which legs carry traffic here just as much as a local park
		// does, and adopting it silently made a five-minute one-leg download look
		// like a group that had simply never churned.
		kind, reason := MuxEventLegPromoted, "peer-mirrored promote: the remote end marked this leg active (CapLegState)"
		if standby {
			kind, reason = MuxEventLegParked, "peer-mirrored park: the remote end marked this leg standby (CapLegState)"
		}
		tp := rg.legTransportAt(idx)
		rg.noteLegEvent(kind, reason, MuxByPeer, idx, rg.legCount(), tp, rg.legHopsFor(tpEntryID(tp)))
	}
	return nil
}

// notePeerLegState records whether leg idx is standby because the PEER said so.
// The leg-state resync re-broadcasts this side's own active/standby set to repair
// a dropped signal; a park the peer originated is the peer's to re-assert, and
// echoing it back pins the leg down from both ends — measured as a peer-mirrored
// park that survived five consecutive downloads because our own 7s resync kept
// re-asserting standby. A promote (standby=false) clears the record, so the moment
// the peer promotes the leg our resync owns it again.
func (rg *RouteGroup) notePeerLegState(idx int, standby bool) {
	rg.peerParkMu.Lock()
	defer rg.peerParkMu.Unlock()
	if !standby {
		delete(rg.peerParkedLegs, idx)
		return
	}
	if rg.peerParkedLegs == nil {
		rg.peerParkedLegs = make(map[int]struct{})
	}
	rg.peerParkedLegs[idx] = struct{}{}
}

// legParkedByPeer reports whether leg idx's standby state was mirrored from the
// peer rather than decided here.
func (rg *RouteGroup) legParkedByPeer(idx int) bool {
	rg.peerParkMu.Lock()
	defer rg.peerParkMu.Unlock()
	_, ok := rg.peerParkedLegs[idx]
	return ok
}

// sendLegState signals leg idx's new active/standby state to the remote so it
// mirrors the parking on its send side (CapLegState). Sent ON the leg (its
// forward rule + transport) so the remote identifies the leg by the route the
// packet arrives on — the same identity an aux-leg handshake uses. Best-effort:
// parking keeps the leg's rules and transport installed, so the signal still
// rides a just-demoted leg. No-op unless CapLegState was negotiated.
func (rg *RouteGroup) sendLegState(idx int, standby bool) {
	if rg.mux == nil || !rg.mux.legStateEnabled {
		return
	}
	rg.mu.Lock()
	var tp *transport.ManagedTransport
	var rule routing.Rule
	if idx >= 0 && idx < len(rg.tps) && idx < len(rg.fwd) {
		tp = rg.tps[idx]
		rule = rg.fwd[idx]
	}
	rg.mu.Unlock()
	if tp == nil || rule == nil {
		return
	}
	packet := routing.MakeLegStatePacket(rule.NextRouteID(), standby)
	if err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID()); err != nil {
		rg.logger.WithError(err).Debugf("LegState: failed to signal leg %d", idx)
	}
}

// SetDirectionPin applies the operator's MANUAL direction pin to this route
// group and signals it to the peer. mode is routing.DirectionAuto (release —
// the flip controller resumes on both ends), DirectionPinDefault (initiator
// sends on the direct leg / download on the multihop mux) or
// DirectionPinFlipped (the swapped mapping). Errors unless the group is
// directional (CapUniDir negotiated) — a pin on a symmetric mux would never be
// read.
//
// The pin is coordinated over the wire (DirectionPacket) because a one-sided
// pin would DESYNC the ends: both could end up sending on the same leg class.
// It is sent on the PRIMARY leg (leg 0's rule/transport): the pin addresses the
// GROUP, not one leg, and leg 0 is the one leg that is never standby — the
// signal always rides an installed, carrying path. Best-effort: an old peer
// (no DirectionPacket) ignores the frame, leaving the pin local-only — its
// controller may then fight the pinned mapping, which is why the caller-facing
// docs call a pin against an old peer best-effort.
func (rg *RouteGroup) SetDirectionPin(mode byte) error {
	if mode > routing.DirectionPinFlipped {
		return fmt.Errorf("invalid direction pin mode %d", mode)
	}
	if rg.mux == nil || !rg.mux.isDirectional() {
		return errors.New("route group is not directional (CapUniDir not negotiated)")
	}
	rg.mux.setFlipPin(mode)
	rg.logger.Infof("direction-pin: local pin set to %q", flipPinString(mode))

	rg.mu.Lock()
	var tp *transport.ManagedTransport
	var rule routing.Rule
	if len(rg.tps) > 0 && len(rg.fwd) > 0 {
		tp = rg.tps[0]
		rule = rg.fwd[0]
	}
	rg.mu.Unlock()
	if tp == nil || rule == nil {
		return nil // pin applied locally; no leg to signal on
	}
	packet := routing.MakeDirectionPacket(rule.NextRouteID(), mode)
	if err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID()); err != nil {
		rg.logger.WithError(err).Warn("direction-pin: failed to signal peer (pin applied locally only)")
	}
	return nil
}

// handleDirectionPacket applies a peer's manual direction pin to this side's
// mux, so the two ends keep sending on disjoint leg classes: the pinned
// mapping is mirrored here AND this side's flip controller goes dormant (it
// would otherwise fight the pin on its next tick). Mode auto releases both
// ends' controllers. Ignored unless the group is directional — a symmetric mux
// has no direction mapping to pin.
func (rg *RouteGroup) handleDirectionPacket(packet routing.Packet) error {
	if rg.mux == nil || !rg.mux.isDirectional() {
		return nil
	}
	mode := packet.DirectionMode()
	if mode > routing.DirectionPinFlipped {
		return nil // unknown mode from a newer peer — ignore, keep current state
	}
	rg.mux.setFlipPin(mode)
	rg.logger.Infof("direction-pin: peer pinned direction mapping to %q", flipPinString(mode))
	return nil
}

func (rg *RouteGroup) handleErrorPacket(packet routing.Packet) error {

	// in this case remote is already closed, and `readCh` is closed too,
	// but some packets may still reach the rg causing panic on writing
	// to `readCh`, so we simple omit such packets
	if rg.isRemoteClosed() {
		return nil
	}

	rg.SetError(errors.New((string(packet.Payload()))))
	return nil
}

func (rg *RouteGroup) handlePingPacket(packet routing.Packet) error {
	payload := packet.Payload()

	timestamp := binary.BigEndian.Uint64(payload)
	throughput := binary.BigEndian.Uint64(payload[8:])

	rg.logger.WithField("func", "RouteGroup.handlePingPacket").Tracef("Throughput is around %d", throughput)

	rg.networkStats.SetUploadSpeed(uint32(throughput)) //nolint: gosec

	return rg.sendPong(int64(timestamp)) //nolint: gosec
}

func (rg *RouteGroup) handlePongPacket(packet routing.Packet) error {
	payload := packet.Payload()

	sentAtMs := binary.BigEndian.Uint64(payload)

	// Per-leg liveness (issue #2): if this pong echoes one of our outstanding
	// leg-liveness probes, mark that leg alive. A pong arriving AT ALL proves
	// the leg's end-to-end round trip works, independent of the latency value
	// (so this runs before the latency sanity-reject below).
	var pongLegID uuid.UUID
	var pongLegOK bool
	rg.legLivenessMu.Lock()
	if legID, ok := rg.inflightPings[int64(sentAtMs)]; ok { //nolint: gosec
		rg.legPongSeen[legID] = true
		delete(rg.inflightPings, int64(sentAtMs)) //nolint: gosec
		pongLegID, pongLegOK = legID, true
	}
	rg.legLivenessMu.Unlock()

	ms := sentAtMs % 1000
	sentAt := time.Unix(int64(sentAtMs/1000), int64(ms)*int64(time.Millisecond)).UTC() //nolint: gosec

	// Use fractional milliseconds for sub-ms precision (e.g. 1.2 ms)
	latencyMs := float64(time.Now().UTC().Sub(sentAt).Microseconds()) / 1000.0

	rg.logger.WithField("func", "RouteGroup.handlePongPacket").Tracef("Latency is around %.1f ms", latencyMs)

	// A pong correlates to no specific ping (no in-flight tracking), so
	// a long-delayed pong arriving after the host woke / queue drained
	// produces a 30+ second sample. Reject up front so neither
	// networkStats, the synchronous MeasureLatency consumer, nor the
	// underlying transport see the bogus value.
	if latencyMs <= 0 || latencyMs > transport.MaxReasonableRTTMs {
		return nil
	}

	// Fold this leg's measured END-TO-END round trip into its EWMA (keyed by
	// transport ID). This is the whole-path latency the pong just traversed —
	// the right per-leg quality signal for the policy's slowest-leg eviction and
	// the fastest-leg retransmit pick, versus first-hop transport RTT.
	if pongLegOK {
		const alpha = 0.3
		rg.legLivenessMu.Lock()
		if prev, ok := rg.legE2ELatency[pongLegID]; ok {
			rg.legE2ELatency[pongLegID] = alpha*latencyMs + (1-alpha)*prev
		} else {
			rg.legE2ELatency[pongLegID] = latencyMs
		}
		// Fold the RAW sample (pre-EWMA) into this leg's OWD-variation window for
		// shared-bottleneck detection (RFC 8382). The window records the variation
		// signature the grouping keys on; the EWMA above records the smoothed level.
		w := rg.legOWD[pongLegID]
		if w == nil {
			w = newSBDWindow()
			rg.legOWD[pongLegID] = w
		}
		w.push(latencyMs)
		// Fold the same RAW sample into the leg's time-bounded window, whose
		// minimum is the load-robust latency the band judges on (the EWMA above
		// tracks a queue building on a busy leg and is not that).
		if rg.legRTTWin == nil {
			rg.legRTTWin = make(map[uuid.UUID]*legRTTWindow)
		}
		mw := rg.legRTTWin[pongLegID]
		if mw == nil {
			mw = &legRTTWindow{}
			rg.legRTTWin[pongLegID] = mw
		}
		mw.push(latencyMs, time.Now())
		e2e := rg.legE2ELatency[pongLegID]
		rg.legLivenessMu.Unlock()
		// Hand the smoothed end-to-end latency to the mux: it is the delay a
		// frame in flight on this leg actually has to survive, and the mux's own
		// per-leg numbers (first-hop transport RTT, send→ack delay) are not it.
		// Without this the loss detectors judged a slow-routed leg by its near
		// edge (see routeMux.legDelayBasisMs).
		if rg.mux != nil {
			rg.mux.setLegE2ERTT(pongLegID, e2e)
		}
	}

	rg.networkStats.SetLatency(uint32(latencyMs)) //nolint: gosec

	// If there's a pending synchronous measurement, send the result
	rg.pendingPongMu.Lock()
	if rg.pendingPongCh != nil {
		select {
		case rg.pendingPongCh <- latencyMs:
		default:
			// Channel full or closed, ignore
		}
	}
	rg.pendingPongMu.Unlock()

	// Propagate ping latency to the underlying transport so it gets
	// reported to TPD during re-registration.
	rg.mu.Lock()
	if len(rg.tps) > 0 && rg.tps[0] != nil {
		rg.tps[0].SetLatency(latencyMs)
	}
	rg.mu.Unlock()

	return nil
}
