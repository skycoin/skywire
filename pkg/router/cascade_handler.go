// Package router pkg/router/cascade_handler.go
//
// CascadeHandler processes cascade protocol messages on a visor.
// It handles CascadeSetupPacket and CascadeAckPacket arriving on
// route ID 0 of any transport.
package router

import (
	"fmt"
	"sync"
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

	// pendingAcks tracks in-flight relay operations awaiting ACK from next hop.
	pendingAcks   map[uint64]chan routing.Packet
	pendingAcksMu sync.Mutex

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
		pendingAcks:    make(map[uint64]chan routing.Packet),
		defaultTimeout: 10 * time.Second,
	}
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
	// Reserve local route IDs.
	ids, err := ch.rt.ReserveKeys(int(msg.ReserveN))
	if err != nil {
		ch.log.WithError(err).Warn("Cascade reserve: failed to reserve route IDs")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, fmt.Sprintf("reserve IDs: %v", err))
		return
	}

	if msg.IsTerminal() {
		// Terminal hop — send ACK with our reserved IDs back.
		ack := &routing.CascadeAck{
			SessionID: msg.SessionID,
			Phase:     msg.Phase,
			RouteIDs:  ids,
		}
		ch.sendAck(sourceTp, ack)
		return
	}

	// Relay to next hop and wait for ACK.
	downstreamAck, err := ch.relayAndWait(msg.RelayTpID, msg.SessionID, msg.Payload)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade reserve: relay failed")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, fmt.Sprintf("relay: %v", err))
		return
	}

	// Prepend our IDs to the downstream ACK.
	downstreamAck.PrependRouteIDs(ids)
	ch.sendAck(sourceTp, downstreamAck)
}

func (ch *CascadeHandler) handleInstall(msg *routing.CascadeSetup, sourceTp *transport.ManagedTransport) {
	// Deserialize and install rules.
	rules, err := routing.DeserializeRules(msg.RuleData)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade install: failed to deserialize rules")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, fmt.Sprintf("deserialize rules: %v", err))
		return
	}

	for _, rule := range rules {
		if err := ch.rt.SaveRule(rule); err != nil {
			ch.log.WithError(err).Warn("Cascade install: failed to save rule")
			ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, fmt.Sprintf("save rule: %v", err))
			return
		}
	}

	if msg.IsTerminal() {
		ack := &routing.CascadeAck{
			SessionID: msg.SessionID,
			Phase:     msg.Phase,
		}
		ch.sendAck(sourceTp, ack)
		return
	}

	// Relay to next hop.
	downstreamAck, err := ch.relayAndWait(msg.RelayTpID, msg.SessionID, msg.Payload)
	if err != nil {
		ch.log.WithError(err).Warn("Cascade install: relay failed")
		ch.sendErrorAck(sourceTp, msg.SessionID, msg.Phase, fmt.Sprintf("relay: %v", err))
		return
	}

	ch.sendAck(sourceTp, downstreamAck)
}

func (ch *CascadeHandler) handleAck(p routing.Packet) {
	ack, err := routing.UnmarshalCascadeAck(p.Payload())
	if err != nil {
		ch.log.WithError(err).Warn("Cascade: failed to unmarshal ACK")
		return
	}

	ch.pendingAcksMu.Lock()
	waitCh, ok := ch.pendingAcks[ack.SessionID]
	if ok {
		delete(ch.pendingAcks, ack.SessionID)
	}
	ch.pendingAcksMu.Unlock()

	if ok {
		waitCh <- p
	}
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
	waitCh := make(chan routing.Packet, 1)
	ch.pendingAcksMu.Lock()
	ch.pendingAcks[sessionID] = waitCh
	ch.pendingAcksMu.Unlock()

	// Send the relay.
	if err := tp.WriteRawPacket(pkt); err != nil {
		ch.pendingAcksMu.Lock()
		delete(ch.pendingAcks, sessionID)
		ch.pendingAcksMu.Unlock()
		return nil, fmt.Errorf("write relay packet: %w", err)
	}

	// Wait for ACK with timeout.
	select {
	case ackPkt := <-waitCh:
		return routing.UnmarshalCascadeAck(ackPkt.Payload())
	case <-time.After(ch.defaultTimeout):
		ch.pendingAcksMu.Lock()
		delete(ch.pendingAcks, sessionID)
		ch.pendingAcksMu.Unlock()
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
