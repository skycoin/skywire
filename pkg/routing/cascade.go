// Package routing pkg/routing/cascade.go
//
// CascadeSetup and CascadeAck define the wire format for the cascade
// route setup protocol. Messages are nested (Russian-doll style) so
// that each hop only sees its own layer.
package routing

import (
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// Cascade protocol phases.
const (
	CascadePhaseReserve uint8 = 0
	CascadePhaseInstall uint8 = 1
)

// CascadeSetup header sizes.
const (
	// Fixed header: Phase(1) + SessionID(8) + RSNPK(33) + Nonce(8) + RSNSig(65) + ReserveN(1) + RelayTpID(16) = 132
	cascadeFixedSize = 1 + 8 + 33 + 8 + 65 + 1 + 16
	// After fixed header: RuleDataLen(2) + RuleData + PayloadLen(2) + Payload
)

var (
	ErrCascadeTooShort     = errors.New("cascade: message too short")
	ErrCascadeInvalidPhase = errors.New("cascade: invalid phase")
	ErrCascadeSigInvalid   = errors.New("cascade: RSN signature verification failed")
	ErrCascadeUntrustedRSN = errors.New("cascade: RSN public key not trusted")
)

// CascadeSetup is the per-hop cascade setup message.
type CascadeSetup struct {
	Phase     uint8         // 0=Reserve, 1=Install
	SessionID uint64        // correlates reserve/install phases
	RSNPK     cipher.PubKey // RSN that authorized this setup (33 bytes)
	Nonce     uint64        // anti-replay, unique per session+hop
	RSNSig    cipher.Sig    // signature over (Phase||SessionID||RSNPK||Nonce||TargetPK||RuleData)
	RuleData  []byte        // serialized routing rules for this hop (empty in Reserve phase)
	ReserveN  uint8         // number of route IDs to reserve (Reserve phase only)
	RelayTpID uuid.UUID     // transport to forward Payload on (zero = terminal hop)
	Payload   []byte        // next hop's CascadeSetup (opaque to this hop)
}

// IsTerminal returns true if this is the last hop (no further relay).
func (cs *CascadeSetup) IsTerminal() bool {
	return cs.RelayTpID == uuid.Nil
}

// SignablePayload returns the bytes that are signed/verified.
func (cs *CascadeSetup) SignablePayload(targetPK cipher.PubKey) []byte {
	// Phase(1) + SessionID(8) + RSNPK(33) + Nonce(8) + TargetPK(33) + RuleData
	buf := make([]byte, 1+8+33+8+33+len(cs.RuleData))
	buf[0] = cs.Phase
	binary.BigEndian.PutUint64(buf[1:9], cs.SessionID)
	copy(buf[9:42], cs.RSNPK[:])
	binary.BigEndian.PutUint64(buf[42:50], cs.Nonce)
	copy(buf[50:83], targetPK[:])
	copy(buf[83:], cs.RuleData)
	return buf
}

// Sign signs the message for a specific target visor PK using the RSN's secret key.
func (cs *CascadeSetup) Sign(targetPK cipher.PubKey, rsnSK cipher.SecKey) error {
	hash := cipher.SumSHA256(cs.SignablePayload(targetPK))
	sig, err := cipher.SignPayload(hash[:], rsnSK)
	if err != nil {
		return fmt.Errorf("cascade sign: %w", err)
	}
	cs.RSNSig = sig
	return nil
}

// Verify checks the RSN signature against the target visor's PK.
func (cs *CascadeSetup) Verify(targetPK cipher.PubKey) error {
	hash := cipher.SumSHA256(cs.SignablePayload(targetPK))
	if err := cipher.VerifyPubKeySignedPayload(cs.RSNPK, cs.RSNSig, hash[:]); err != nil {
		return ErrCascadeSigInvalid
	}
	return nil
}

// Marshal serializes CascadeSetup to binary.
//
// Wire format:
//
//	[Phase:1][SessionID:8][RSNPK:33][Nonce:8][RSNSig:65][ReserveN:1][RelayTpID:16]
//	[RuleDataLen:2][RuleData:...][PayloadLen:2][Payload:...]
func (cs *CascadeSetup) Marshal() ([]byte, error) {
	ruleLen := len(cs.RuleData)
	payloadLen := len(cs.Payload)
	totalLen := cascadeFixedSize + 2 + ruleLen + 2 + payloadLen

	buf := make([]byte, totalLen)
	off := 0

	buf[off] = cs.Phase
	off++
	binary.BigEndian.PutUint64(buf[off:], cs.SessionID)
	off += 8
	copy(buf[off:], cs.RSNPK[:])
	off += 33
	binary.BigEndian.PutUint64(buf[off:], cs.Nonce)
	off += 8
	copy(buf[off:], cs.RSNSig[:])
	off += 65
	buf[off] = cs.ReserveN
	off++
	copy(buf[off:], cs.RelayTpID[:])
	off += 16

	binary.BigEndian.PutUint16(buf[off:], uint16(ruleLen)) //nolint:gosec
	off += 2
	copy(buf[off:], cs.RuleData)
	off += ruleLen

	binary.BigEndian.PutUint16(buf[off:], uint16(payloadLen)) //nolint:gosec
	off += 2
	copy(buf[off:], cs.Payload)

	return buf, nil
}

// UnmarshalCascadeSetup deserializes a CascadeSetup from binary.
func UnmarshalCascadeSetup(data []byte) (*CascadeSetup, error) {
	if len(data) < cascadeFixedSize+4 { // +4 for two uint16 length prefixes
		return nil, ErrCascadeTooShort
	}

	cs := &CascadeSetup{}
	off := 0

	cs.Phase = data[off]
	off++
	if cs.Phase > CascadePhaseInstall {
		return nil, ErrCascadeInvalidPhase
	}

	cs.SessionID = binary.BigEndian.Uint64(data[off:])
	off += 8
	copy(cs.RSNPK[:], data[off:off+33])
	off += 33
	cs.Nonce = binary.BigEndian.Uint64(data[off:])
	off += 8
	copy(cs.RSNSig[:], data[off:off+65])
	off += 65
	cs.ReserveN = data[off]
	off++
	copy(cs.RelayTpID[:], data[off:off+16])
	off += 16

	if off+2 > len(data) {
		return nil, ErrCascadeTooShort
	}
	ruleLen := int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	if off+ruleLen+2 > len(data) {
		return nil, ErrCascadeTooShort
	}
	cs.RuleData = make([]byte, ruleLen)
	copy(cs.RuleData, data[off:off+ruleLen])
	off += ruleLen

	payloadLen := int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	if off+payloadLen > len(data) {
		return nil, ErrCascadeTooShort
	}
	cs.Payload = make([]byte, payloadLen)
	copy(cs.Payload, data[off:off+payloadLen])

	return cs, nil
}

// CascadeAck is the per-hop cascade acknowledgment.
type CascadeAck struct {
	SessionID uint64
	Phase     uint8
	RouteIDs  []RouteID // reserved IDs (Reserve phase) or empty (Install phase)
	Error     string    // non-empty on failure
}

// PrependRouteIDs adds this hop's reserved IDs to the front of the ACK.
func (ca *CascadeAck) PrependRouteIDs(ids []RouteID) {
	ca.RouteIDs = append(ids, ca.RouteIDs...)
}

// Marshal serializes CascadeAck to binary.
//
// Wire format:
//
//	[SessionID:8][Phase:1][RouteIDCount:1][RouteIDs:4*N][ErrorLen:2][Error:...]
func (ca *CascadeAck) Marshal() ([]byte, error) {
	idCount := len(ca.RouteIDs)
	if idCount > 255 {
		return nil, errors.New("cascade ack: too many route IDs")
	}
	errBytes := []byte(ca.Error)
	totalLen := 8 + 1 + 1 + 4*idCount + 2 + len(errBytes)

	buf := make([]byte, totalLen)
	off := 0

	binary.BigEndian.PutUint64(buf[off:], ca.SessionID)
	off += 8
	buf[off] = ca.Phase
	off++
	buf[off] = uint8(idCount) //nolint:gosec
	off++
	for _, id := range ca.RouteIDs {
		binary.BigEndian.PutUint32(buf[off:], uint32(id))
		off += 4
	}
	binary.BigEndian.PutUint16(buf[off:], uint16(len(errBytes))) //nolint:gosec
	off += 2
	copy(buf[off:], errBytes)

	return buf, nil
}

// UnmarshalCascadeAck deserializes a CascadeAck from binary.
func UnmarshalCascadeAck(data []byte) (*CascadeAck, error) {
	if len(data) < 10+2 { // SessionID(8)+Phase(1)+Count(1)+ErrorLen(2)
		return nil, ErrCascadeTooShort
	}

	ca := &CascadeAck{}
	off := 0

	ca.SessionID = binary.BigEndian.Uint64(data[off:])
	off += 8
	ca.Phase = data[off]
	off++
	idCount := int(data[off])
	off++

	if off+4*idCount+2 > len(data) {
		return nil, ErrCascadeTooShort
	}
	ca.RouteIDs = make([]RouteID, idCount)
	for i := range ca.RouteIDs {
		ca.RouteIDs[i] = RouteID(binary.BigEndian.Uint32(data[off:]))
		off += 4
	}

	errLen := int(binary.BigEndian.Uint16(data[off:]))
	off += 2
	if off+errLen > len(data) {
		return nil, ErrCascadeTooShort
	}
	ca.Error = string(data[off : off+errLen])

	return ca, nil
}

// SerializeRules serializes multiple routing rules into a single byte slice
// with length prefixes: [count:1][len:2][rule:...]...
func SerializeRules(rules []Rule) []byte {
	totalLen := 1 // count byte
	for _, r := range rules {
		totalLen += 2 + len(r)
	}
	buf := make([]byte, totalLen)
	buf[0] = uint8(len(rules)) //nolint:gosec
	off := 1
	for _, r := range rules {
		binary.BigEndian.PutUint16(buf[off:], uint16(len(r))) //nolint:gosec
		off += 2
		copy(buf[off:], r)
		off += len(r)
	}
	return buf
}

// DeserializeRules deserializes rules from the format produced by SerializeRules.
func DeserializeRules(data []byte) ([]Rule, error) {
	if len(data) < 1 {
		return nil, errors.New("cascade: empty rule data")
	}
	count := int(data[0])
	off := 1
	rules := make([]Rule, 0, count)
	for i := 0; i < count; i++ {
		if off+2 > len(data) {
			return nil, fmt.Errorf("cascade: truncated rule %d", i)
		}
		ruleLen := int(binary.BigEndian.Uint16(data[off:]))
		off += 2
		if off+ruleLen > len(data) {
			return nil, fmt.Errorf("cascade: truncated rule %d data", i)
		}
		rule := make(Rule, ruleLen)
		copy(rule, data[off:off+ruleLen])
		rules = append(rules, rule)
		off += ruleLen
	}
	return rules, nil
}
