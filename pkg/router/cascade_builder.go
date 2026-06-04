// Package router pkg/router/cascade_builder.go
//
// CascadeBuilder constructs nested cascade messages for the RSN.
// It builds the Russian-doll structure from last hop to first, signs
// each layer for its target visor, and sends the cascade to the first hop.
package router

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// CascadeBuilder constructs and sends cascade messages.
//
// It serves two roles depending on how it is constructed:
//
//   - RSN-side (NewCascadeBuilder): holds the RSN's rsnPK/rsnSK and SIGNS
//     each per-hop cascade layer. The RSN-side builder is a pure signing
//     oracle — it builds bytes and never sends them itself.
//   - Source-side (NewSourceCascadeBuilder): holds a zero rsnSK and never
//     signs; it only INJECTS pre-signed cascade bytes (received from the
//     RSN over RPC) into the source's own transports and collects ACKs.
type CascadeBuilder struct {
	log   *logging.Logger
	rsnPK cipher.PubKey
	rsnSK cipher.SecKey
	tm    *transport.Manager

	// acks correlates sessionIDs to waiting senders. On the source visor
	// it is shared with the CascadeHandler so ACKs arriving on the single
	// registered cascade handler are delivered to this builder.
	acks *ackRegistry

	sessionCounter uint64
	sessionMu      sync.Mutex

	reserveTimeout time.Duration
	installTimeout time.Duration
}

// NewCascadeBuilder creates a signing builder for the RSN.
func NewCascadeBuilder(
	log *logging.Logger,
	rsnPK cipher.PubKey,
	rsnSK cipher.SecKey,
	tm *transport.Manager,
) *CascadeBuilder {
	return &CascadeBuilder{
		log:            log,
		rsnPK:          rsnPK,
		rsnSK:          rsnSK,
		tm:             tm,
		acks:           newAckRegistry(),
		reserveTimeout: 10 * time.Second,
		installTimeout: 10 * time.Second,
	}
}

// NewSourceCascadeBuilder creates a send-only builder for the SOURCE visor.
//
// It carries no RSN secret key (it never signs) and shares the ack registry
// with the visor's CascadeHandler so that ACKs arriving on the visor's
// single registered cascade handler are routed back to in-flight sends. The
// source-side builder injects pre-signed cascade bytes — produced by the RSN
// via the CascadeSign* RPCs — into the source's own transports.
func NewSourceCascadeBuilder(
	log *logging.Logger,
	tm *transport.Manager,
	acks *ackRegistry,
) *CascadeBuilder {
	if acks == nil {
		acks = newAckRegistry()
	}
	return &CascadeBuilder{
		log:            log,
		tm:             tm,
		acks:           acks,
		reserveTimeout: 10 * time.Second,
		installTimeout: 10 * time.Second,
	}
}

// SetTimeouts configures per-phase timeouts.
func (cb *CascadeBuilder) SetTimeouts(reserve, install time.Duration) {
	cb.reserveTimeout = reserve
	cb.installTimeout = install
}

func (cb *CascadeBuilder) nextSessionID() uint64 {
	cb.sessionMu.Lock()
	defer cb.sessionMu.Unlock()
	cb.sessionCounter++
	return cb.sessionCounter
}

// BuildReserveMessage constructs a nested reserve cascade from last hop to first.
// hops is the route's hop list: [{From:A, To:C, TpID:tp1}, {From:C, To:D, TpID:tp2}, ...].
// The target visors are: hops[0].From (source), hops[i].From (intermediaries), hops[last].To (destination).
func (cb *CascadeBuilder) BuildReserveMessage(hops []routing.Hop, reserveN uint8) (uint64, []byte, error) {
	if len(hops) == 0 {
		return 0, nil, fmt.Errorf("cascade: no hops")
	}

	sessionID := cb.nextSessionID()
	var nonce uint64

	// Build targets list: [A, C, D, ..., B] (source + intermediaries + destination)
	type hopTarget struct {
		pk        cipher.PubKey
		relayTpID uuid.UUID // transport to relay Payload on (zero for terminal)
	}
	targets := make([]hopTarget, 0, len(hops)+1)
	targets = append(targets, hopTarget{pk: hops[0].From, relayTpID: hops[0].TpID})
	for i := 0; i < len(hops); i++ {
		var relayID uuid.UUID
		if i < len(hops)-1 {
			relayID = hops[i+1].TpID
		}
		targets = append(targets, hopTarget{pk: hops[i].To, relayTpID: relayID})
	}

	// Build nested from last to first.
	var innerPayload []byte
	for i := len(targets) - 1; i >= 0; i-- {
		nonce++
		msg := &routing.CascadeSetup{
			Phase:     routing.CascadePhaseReserve,
			SessionID: sessionID,
			RSNPK:     cb.rsnPK,
			Nonce:     nonce,
			ReserveN:  reserveN,
			RelayTpID: targets[i].relayTpID,
			Payload:   innerPayload,
		}
		if err := msg.Sign(targets[i].pk, cb.rsnSK); err != nil {
			return 0, nil, fmt.Errorf("cascade sign hop %d: %w", i, err)
		}
		data, err := msg.Marshal()
		if err != nil {
			return 0, nil, fmt.Errorf("cascade marshal hop %d: %w", i, err)
		}
		innerPayload = data
	}

	return sessionID, innerPayload, nil
}

// BuildInstallMessage constructs a nested install cascade from last hop to first.
// rulesPerHop maps each visor PK to the rules that should be installed on it.
func (cb *CascadeBuilder) BuildInstallMessage(
	hops []routing.Hop,
	sessionID uint64,
	rulesPerHop map[cipher.PubKey][]routing.Rule,
) ([]byte, error) {
	if len(hops) == 0 {
		return nil, fmt.Errorf("cascade: no hops")
	}

	var nonce uint64

	// Same target list as BuildReserveMessage.
	type hopTarget struct {
		pk        cipher.PubKey
		relayTpID uuid.UUID
	}
	targets := make([]hopTarget, 0, len(hops)+1)
	targets = append(targets, hopTarget{pk: hops[0].From, relayTpID: hops[0].TpID})
	for i := 0; i < len(hops); i++ {
		var relayID uuid.UUID
		if i < len(hops)-1 {
			relayID = hops[i+1].TpID
		}
		targets = append(targets, hopTarget{pk: hops[i].To, relayTpID: relayID})
	}

	// Build nested from last to first.
	var innerPayload []byte
	for i := len(targets) - 1; i >= 0; i-- {
		nonce++
		rules := rulesPerHop[targets[i].pk]
		ruleData := routing.SerializeRules(rules)

		msg := &routing.CascadeSetup{
			Phase:     routing.CascadePhaseInstall,
			SessionID: sessionID,
			RSNPK:     cb.rsnPK,
			Nonce:     nonce,
			RuleData:  ruleData,
			RelayTpID: targets[i].relayTpID,
			Payload:   innerPayload,
		}
		if err := msg.Sign(targets[i].pk, cb.rsnSK); err != nil {
			return nil, fmt.Errorf("cascade sign hop %d: %w", i, err)
		}
		data, err := msg.Marshal()
		if err != nil {
			return nil, fmt.Errorf("cascade marshal hop %d: %w", i, err)
		}
		innerPayload = data
	}

	return innerPayload, nil
}

// SendCascade sends a cascade payload to the first hop and waits for the ACK.
// firstHopTpID is the transport connecting the RSN to the first hop.
func (cb *CascadeBuilder) SendCascade(ctx context.Context, firstHopTpID uuid.UUID, sessionID uint64, payload []byte, timeout time.Duration) (*routing.CascadeAck, error) {
	tp, err := cb.tm.GetTransportByID(firstHopTpID)
	if err != nil {
		return nil, fmt.Errorf("cascade: first hop transport %s not found: %w", firstHopTpID, err)
	}

	pkt, err := routing.MakeCascadeSetupPacket(payload)
	if err != nil {
		return nil, fmt.Errorf("cascade: make packet: %w", err)
	}

	// Register for ACK.
	waitCh := cb.acks.register(sessionID)

	if err := tp.WriteRawPacket(pkt); err != nil {
		cb.acks.unregister(sessionID)
		return nil, fmt.Errorf("cascade: write to first hop: %w", err)
	}

	select {
	case ackPkt := <-waitCh:
		return routing.UnmarshalCascadeAck(ackPkt.Payload())
	case <-ctx.Done():
		cb.acks.unregister(sessionID)
		return nil, ctx.Err()
	case <-time.After(timeout):
		cb.acks.unregister(sessionID)
		return nil, fmt.Errorf("cascade: ACK timeout (session %d)", sessionID)
	}
}

// HandleAck is called by the transport manager's cascade handler when an
// ACK arrives on one of the RSN's transports. Dispatches to the waiting sender.
func (cb *CascadeBuilder) HandleAck(p routing.Packet, _ *transport.ManagedTransport) {
	if p.Type() != routing.CascadeAckPacket {
		return
	}
	ack, err := routing.UnmarshalCascadeAck(p.Payload())
	if err != nil {
		return
	}
	cb.acks.dispatch(ack.SessionID, p)
}

// AckRegistry returns the builder's ack registry so a CascadeHandler on the
// same visor can share it (the source visor has one registered cascade
// handler that must deliver ACKs to this builder's in-flight sends).
func (cb *CascadeBuilder) AckRegistry() *ackRegistry {
	return cb.acks
}
