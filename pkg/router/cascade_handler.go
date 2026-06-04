// Package router pkg/router/cascade_handler.go
//
// CascadeHandler processes cascade protocol messages on a visor.
// It handles CascadeSetupPacket and CascadeAckPacket arriving on
// route ID 0 of any transport.
package router

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// CascadeHandler processes cascade protocol messages arriving on any transport.
type CascadeHandler struct {
	log         *logging.Logger
	localPK     cipher.PubKey
	trustedRSNs map[cipher.PubKey]struct{}
	rt          routing.Table // for ReserveKeys and SaveRule
	tm          *transport.Manager

	// acks tracks in-flight relay operations (and, on the source visor,
	// source-driven sends) awaiting an ACK. Shared with the source-side
	// CascadeBuilder so its injected cascades' ACKs are delivered here.
	acks *ackRegistry

	// defaultTimeout is the per-hop timeout for waiting for a cascade ACK.
	defaultTimeout time.Duration
}

// NewCascadeHandler creates a new cascade handler.
func NewCascadeHandler(
	log *logging.Logger,
	localPK cipher.PubKey,
	trustedRSNPKs []cipher.PubKey,
	rt routing.Table,
	tm *transport.Manager,
) *CascadeHandler {
	trusted := make(map[cipher.PubKey]struct{}, len(trustedRSNPKs))
	for _, pk := range trustedRSNPKs {
		trusted[pk] = struct{}{}
	}
	return &CascadeHandler{
		log:            log,
		localPK:        localPK,
		trustedRSNs:    trusted,
		rt:             rt,
		tm:             tm,
		acks:           newAckRegistry(),
		defaultTimeout: 10 * time.Second,
	}
}

// AckRegistry returns the handler's shared ack registry. The source-side
// CascadeBuilder is constructed against this registry so ACKs arriving on
// the visor's single registered cascade handler reach the builder's sends.
func (ch *CascadeHandler) AckRegistry() *ackRegistry {
	return ch.acks
}

// HandlePacket is the callback registered with the transport manager.
// It dispatches cascade packets to the appropriate handler.
func (ch *CascadeHandler) HandlePacket(p routing.Packet, sourceTp *transport.ManagedTransport) {
	switch p.Type() {
	case routing.CascadeSetupPacket:
		ch.handleSetup(p, sourceTp)
	case routing.CascadeAckPacket:
		ch.handleAck(p)
	}
}

func (ch *CascadeHandler) handleSetup(p routing.Packet, sourceTp *transport.ManagedTransport) {
	msg, err := routing.UnmarshalCascadeSetup(p.Payload())
	if err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to unmarshal setup message")
		return
	}

	// Verify RSN is trusted.
	if _, ok := ch.trustedRSNs[msg.RSNPK]; !ok {
		ch.log.WithField("rsn_pk", msg.RSNPK).Warn("Cascade: untrusted RSN, rejecting")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, routing.ErrCascadeUntrustedRSN.Error())
		return
	}

	// Verify signature.
	if err := msg.Verify(ch.localPK); err != nil {
		ch.log.WithError(err).Warn("Cascade: signature verification failed")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, routing.ErrCascadeSigInvalid.Error())
		return
	}

	switch msg.Phase {
	case routing.CascadePhaseReserve:
		ch.handleReserve(msg, sourceTp)
	case routing.CascadePhaseInstall:
		ch.handleInstall(msg, sourceTp)
	default:
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, "unknown phase")
	}
}

func (ch *CascadeHandler) handleReserve(msg *routing.CascadeSetup, sourceTp *transport.ManagedTransport) {
	ack, err := ch.processReserve(msg)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade reserve failed")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, err.Error())
		return
	}
	ch.sendAck(sourceTp, ack)
}

// processReserve reserves this hop's route IDs and, for non-terminal hops,
// relays the inner payload to the next hop and prepends our IDs to the
// downstream ACK. It returns the ACK to send upstream. Shared by the wire path
// (handleReserve) and the source-origin path (ProcessLocalOrigin).
func (ch *CascadeHandler) processReserve(msg *routing.CascadeSetup) (*routing.CascadeAck, error) {
	// Reserve local route IDs.
	ids, err := ch.rt.ReserveKeys(int(msg.ReserveN))
	if err != nil {
		return nil, fmt.Errorf("reserve IDs: %w", err)
	}

	if msg.IsTerminal() {
		// Terminal hop — ACK with our reserved IDs.
		return &routing.CascadeAck{
			SessionID: msg.SessionID,
			Phase:     msg.Phase,
			RouteIDs:  ids,
		}, nil
	}

	// Relay to next hop and wait for ACK.
	downstreamAck, err := ch.relayAndWait(msg.RelayTpID, msg.SessionID, msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	// Prepend our IDs to the downstream ACK.
	downstreamAck.PrependRouteIDs(ids)
	return downstreamAck, nil
}

func (ch *CascadeHandler) handleInstall(msg *routing.CascadeSetup, sourceTp *transport.ManagedTransport) {
	ack, err := ch.processInstall(msg)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade install failed")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, err.Error())
		return
	}
	ch.sendAck(sourceTp, ack)
}

// processInstall saves this hop's rules and, for non-terminal hops, relays the
// inner payload to the next hop and returns the downstream ACK. Shared by the
// wire path (handleInstall) and the source-origin path (ProcessLocalOrigin).
func (ch *CascadeHandler) processInstall(msg *routing.CascadeSetup) (*routing.CascadeAck, error) {
	// Deserialize and install rules.
	rules, err := routing.DeserializeRules(msg.RuleData)
	if err != nil {
		return nil, fmt.Errorf("deserialize rules: %w", err)
	}

	for _, rule := range rules {
		if err := ch.rt.SaveRule(rule); err != nil {
			return nil, fmt.Errorf("save rule: %w", err)
		}
	}

	if msg.IsTerminal() {
		return &routing.CascadeAck{
			SessionID: msg.SessionID,
			Phase:     msg.Phase,
		}, nil
	}

	// Relay to next hop.
	downstreamAck, err := ch.relayAndWait(msg.RelayTpID, msg.SessionID, msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("relay: %w", err)
	}

	return downstreamAck, nil
}

// ProcessLocalOrigin processes a cascade layer addressed to THIS visor as the
// route's origin (source). Unlike handleSetup, the origin has no upstream
// transport to ACK back on: it verifies the RSN signature against our own PK,
// performs the reserve/install locally, relays the inner payload to the first
// hop over our own transport, and RETURNS the collected ACK to the caller
// (runSourceCascade) instead of writing it to a sourceTp.
//
// This is the crux of source-driven cascade: the RSN builds the nested cascade
// with the source as the OUTERMOST layer (signed for the source's PK). The
// source must consume that layer itself — shipping it verbatim to the first hop
// made the hop reject it ("RSN signature verification failed"), since it was
// signed for the source's PK, not the hop's.
func (ch *CascadeHandler) ProcessLocalOrigin(payload []byte) (*routing.CascadeAck, error) {
	msg, err := routing.UnmarshalCascadeSetup(payload)
	if err != nil {
		return nil, fmt.Errorf("cascade origin: unmarshal: %w", err)
	}
	if _, ok := ch.trustedRSNs[msg.RSNPK]; !ok {
		return nil, routing.ErrCascadeUntrustedRSN
	}
	if err := msg.Verify(ch.localPK); err != nil {
		return nil, err
	}
	switch msg.Phase {
	case routing.CascadePhaseReserve:
		return ch.processReserve(msg)
	case routing.CascadePhaseInstall:
		return ch.processInstall(msg)
	default:
		return nil, fmt.Errorf("cascade origin: unknown phase %d", msg.Phase)
	}
}

func (ch *CascadeHandler) handleAck(p routing.Packet) {
	ack, err := routing.UnmarshalCascadeAck(p.Payload())
	if err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to unmarshal ACK")
		return
	}

	ch.acks.dispatch(ack.SessionID, p)
}

// relayAndWait sends the cascade payload to the next hop transport and waits for an ACK.
func (ch *CascadeHandler) relayAndWait(relayTpID uuid.UUID, sessionID uint64, payload []byte) (*routing.CascadeAck, error) {
	// Find the relay transport.
	tp, err := ch.tm.GetTransportByID(relayTpID)
	if err != nil {
		return nil, fmt.Errorf("relay transport %s not found: %w", relayTpID, err)
	}

	// Build the packet.
	pkt, err := routing.MakeCascadeSetupPacket(payload)
	if err != nil {
		return nil, fmt.Errorf("make relay packet: %w", err)
	}

	// Register for ACK.
	waitCh := ch.acks.register(sessionID)

	// Send the relay.
	if err := tp.WriteRawPacket(pkt); err != nil {
		ch.acks.unregister(sessionID)
		return nil, fmt.Errorf("write relay packet: %w", err)
	}

	// Wait for ACK with timeout.
	select {
	case ackPkt := <-waitCh:
		return routing.UnmarshalCascadeAck(ackPkt.Payload())
	case <-time.After(ch.defaultTimeout):
		ch.acks.unregister(sessionID)
		return nil, fmt.Errorf("cascade ACK timeout (session %d)", sessionID)
	}
}

func (ch *CascadeHandler) sendAck(tp *transport.ManagedTransport, ack *routing.CascadeAck) {
	data, err := ack.Marshal()
	if err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to marshal ACK")
		return
	}
	pkt, err := routing.MakeCascadeAckPacket(data)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to make ACK packet")
		return
	}
	if err := tp.WriteRawPacket(pkt); err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to send ACK")
	}
}

func (ch *CascadeHandler) sendErrorAck(tp *transport.ManagedTransport, sessionID uint64, phase uint8, errMsg string) {
	ack := &routing.CascadeAck{
		SessionID: sessionID,
		Phase:     phase,
		Error:     errMsg,
	}
	ch.sendAck(tp, ack)
}
