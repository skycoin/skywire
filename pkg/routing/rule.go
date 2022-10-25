// Package routing pkg/routing/rule.go
package routing

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// RuleHeaderSize represents the base size of a rule.
// All rules should be at-least this size.
// TODO(evanlinjin): Document the format of rules in comments.
const (
	RuleHeaderSize      = 8 + 1 + 4
	pkSize              = len(cipher.PubKey{})
	uuidSize            = len(uuid.UUID{})
	routeDescriptorSize = pkSize*2 + 2*2
)

// RuleType defines type of a routing rule
type RuleType byte

func (rt RuleType) String() string {
	switch rt {
	case RuleReverse:
		return "Consume"
	case RuleForward:
		return "Forward"
	case RuleIntermediary:
		return "IntermediaryForward"
	}

	return fmt.Sprintf("Unknown(%d)", rt)
}

const (
	// RuleReverse represents a hop to the route's destination visor.
	// A packet referencing this rule is to be consumed locally.
	RuleReverse = RuleType(0)

	// RuleForward represents a hop from the route's source visor.
	// A packet referencing this rule is to be sent to a remote visor.
	RuleForward = RuleType(1)

	// RuleIntermediary represents a hop which is not from the route's source,
	// nor to the route's destination.
	RuleIntermediary = RuleType(2)
)

// Rule represents a routing rule.
// There are two types of routing rules; App and Forward.
type Rule []byte

func (r Rule) assertLen(l int) {
	if len(r) < l {
		panic("bad rule length")
	}
}

// KeepAlive returns rule's keep-alive timeout.
func (r Rule) KeepAlive() time.Duration {
	r.assertLen(RuleHeaderSize)
	return time.Duration(binary.BigEndian.Uint64(r[0:8]))
}

// setKeepAlive sets rule's keep-alive timeout.
func (r Rule) setKeepAlive(keepAlive time.Duration) {
	r.assertLen(RuleHeaderSize)

	if keepAlive < 0 {
		keepAlive = 0
	}

	binary.BigEndian.PutUint64(r[0:8], uint64(keepAlive))
}

// Type returns type of a rule.
func (r Rule) Type() RuleType {
	r.assertLen(RuleHeaderSize)
	return RuleType(r[8])
}

// setType sets type of a rule.
func (r Rule) setType(t RuleType) {
	r.assertLen(RuleHeaderSize)
	r[8] = byte(t)
}

// KeyRouteID returns KeyRouteID from the rule: it is used as the key to retrieve the rule.
func (r Rule) KeyRouteID() RouteID {
	r.assertLen(RuleHeaderSize)
	return RouteID(binary.BigEndian.Uint32(r[8+1 : 8+1+4]))
}

// SetKeyRouteID sets KeyRouteID of a rule.
func (r Rule) SetKeyRouteID(id RouteID) {
	r.assertLen(RuleHeaderSize)
	binary.BigEndian.PutUint32(r[8+1:8+1+4], uint32(id))
}

// Body returns Body from the rule.
func (r Rule) Body() []byte {
	r.assertLen(RuleHeaderSize)
	return append(r[:0:0], r[RuleHeaderSize:]...)
}

// RouteDescriptor returns RouteDescriptor from the rule.
func (r Rule) RouteDescriptor() RouteDescriptor {
	switch t := r.Type(); t {
	case RuleReverse, RuleForward:
		r.assertLen(RuleHeaderSize + routeDescriptorSize)

		var desc RouteDescriptor

		copy(desc[:], r[RuleHeaderSize:])

		return desc
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// NextRouteID returns NextRouteID from the rule.
func (r Rule) NextRouteID() RouteID {
	offset := RuleHeaderSize

	switch t := r.Type(); t {
	case RuleForward:
		offset += routeDescriptorSize
		fallthrough
	case RuleIntermediary:
		r.assertLen(offset + 4)
		return RouteID(binary.BigEndian.Uint32(r[offset : offset+4]))
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setNextRouteID sets setNextRouteID of a rule.
func (r Rule) setNextRouteID(id RouteID) {
	offset := RuleHeaderSize

	switch t := r.Type(); t {
	case RuleForward:
		offset += routeDescriptorSize
		fallthrough
	case RuleIntermediary:
		r.assertLen(offset + 4)
		binary.BigEndian.PutUint32(r[offset:offset+4], uint32(id))
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// NextTransportID returns next transport ID for a forward rule.
func (r Rule) NextTransportID() uuid.UUID {
	offset := RuleHeaderSize + 4

	switch t := r.Type(); t {
	case RuleForward:
		offset += routeDescriptorSize
		fallthrough
	case RuleIntermediary:
		r.assertLen(offset + 4)

		return uuid.Must(uuid.FromBytes(r[offset : offset+uuidSize]))
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setNextTransportID sets setNextTransportID of a rule.
func (r Rule) setNextTransportID(id uuid.UUID) {
	offset := RuleHeaderSize + 4

	switch t := r.Type(); t {
	case RuleForward:
		offset += routeDescriptorSize
		fallthrough
	case RuleIntermediary:
		r.assertLen(offset + 4)
		copy(r[offset:offset+uuidSize], id[:])
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setSrcPK sets source public key of a rule.
func (r Rule) setSrcPK(pk cipher.PubKey) {
	switch t := r.Type(); t {
	case RuleReverse, RuleForward:
		r.assertLen(RuleHeaderSize + pkSize)
		copy(r[RuleHeaderSize:RuleHeaderSize+pkSize], pk[:])
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setDstPK sets destination public key of a rule.
func (r Rule) setDstPK(pk cipher.PubKey) {
	switch t := r.Type(); t {
	case RuleReverse, RuleForward:
		r.assertLen(RuleHeaderSize + pkSize*2)
		copy(r[RuleHeaderSize+pkSize:RuleHeaderSize+pkSize*2], pk[:])
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setSrcPort sets source port of a rule.
func (r Rule) setSrcPort(port Port) {
	switch t := r.Type(); t {
	case RuleReverse, RuleForward:
		r.assertLen(RuleHeaderSize + pkSize*2 + 2)
		binary.BigEndian.PutUint16(r[RuleHeaderSize+pkSize*2:RuleHeaderSize+pkSize*2+2], uint16(port))
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// setDstPort sets destination port of a rule.
func (r Rule) setDstPort(port Port) {
	switch t := r.Type(); t {
	case RuleReverse, RuleForward:
		r.assertLen(RuleHeaderSize + pkSize*2 + 2*2)
		binary.BigEndian.PutUint16(r[RuleHeaderSize+pkSize*2+2:RuleHeaderSize+pkSize*2+2*2], uint16(port))
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// String returns rule's string representation.
func (r Rule) String() string {
	switch t := r.Type(); t {
	case RuleReverse:
		rd := r.RouteDescriptor()
		return fmt.Sprintf("REV(keyRtID:%d, %s)",
			r.KeyRouteID(), rd.String())
	case RuleForward:
		rd := r.RouteDescriptor()
		return fmt.Sprintf("FWD(keyRtID:%d, nxtRtID:%d, nxtTpID:%s, %s)",
			r.KeyRouteID(), r.NextRouteID(), r.NextTransportID(), rd.String())
	case RuleIntermediary:
		return fmt.Sprintf("INTER(keyRtID:%d, nxtRtID:%d, nxtTpID:%s)",
			r.KeyRouteID(), r.NextRouteID(), r.NextTransportID())
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}
}

// RouteDescriptorFields summarizes route descriptor fields of a RoutingRule.
type RouteDescriptorFields struct {
	DstPK   cipher.PubKey `json:"dst_pk"`
	SrcPK   cipher.PubKey `json:"src_pk"`
	DstPort Port          `json:"dst_port"`
	SrcPort Port          `json:"src_port"`
}

// RuleConsumeFields summarizes consume fields of a RoutingRule.
type RuleConsumeFields struct {
	RouteDescriptor RouteDescriptorFields `json:"route_descriptor"`
}

// RuleForwardFields summarizes Forward fields of a RoutingRule.
type RuleForwardFields struct {
	RouteDescriptor RouteDescriptorFields `json:"route_descriptor"`
	NextRID         RouteID               `json:"next_rid"`
	NextTID         uuid.UUID             `json:"next_tid"`
}

// RuleIntermediaryForwardFields summarizes IntermediaryForward fields of a RoutingRule.
type RuleIntermediaryForwardFields struct {
	NextRID RouteID   `json:"next_rid"`
	NextTID uuid.UUID `json:"next_tid"`
}

// RuleSummary provides a summary of a RoutingRule.
type RuleSummary struct {
	KeepAlive                 time.Duration                  `json:"keep_alive"`
	Type                      RuleType                       `json:"rule_type"`
	KeyRouteID                RouteID                        `json:"key_route_id"`
	ConsumeFields             *RuleConsumeFields             `json:"app_fields,omitempty"`
	ForwardFields             *RuleForwardFields             `json:"forward_fields,omitempty"`
	IntermediaryForwardFields *RuleIntermediaryForwardFields `json:"intermediary_forward_fields,omitempty"`
}

// ToRule converts RoutingRuleSummary to RoutingRule.
func (rs *RuleSummary) ToRule() (Rule, error) {
	switch {
	case rs.Type == RuleReverse:
		if rs.ConsumeFields == nil || rs.ForwardFields != nil || rs.IntermediaryForwardFields != nil {
			return nil, errors.New("invalid routing rule summary")
		}

		f := rs.ConsumeFields
		d := f.RouteDescriptor

		return ConsumeRule(rs.KeepAlive, rs.KeyRouteID, d.SrcPK, d.DstPK, d.SrcPort, d.DstPort), nil
	case rs.Type == RuleForward:
		if rs.ConsumeFields != nil || rs.ForwardFields == nil || rs.IntermediaryForwardFields != nil {
			return nil, errors.New("invalid routing rule summary")
		}

		f := rs.ForwardFields
		d := f.RouteDescriptor

		return ForwardRule(rs.KeepAlive, rs.KeyRouteID, f.NextRID, f.NextTID, d.SrcPK, d.DstPK, d.SrcPort, d.DstPort), nil
	case rs.Type == RuleIntermediary:
		if rs.ConsumeFields != nil || rs.ForwardFields != nil || rs.IntermediaryForwardFields == nil {
			return nil, errors.New("invalid routing rule summary")
		}

		f := rs.IntermediaryForwardFields

		return IntermediaryForwardRule(rs.KeepAlive, rs.KeyRouteID, f.NextRID, f.NextTID), nil
	default:
		return nil, errors.New("invalid routing rule summary")
	}
}

// Summary returns the RoutingRule's summary.
func (r Rule) Summary() *RuleSummary {
	summary := RuleSummary{
		KeepAlive:  r.KeepAlive(),
		Type:       r.Type(),
		KeyRouteID: r.KeyRouteID(),
	}

	switch t := summary.Type; t {
	case RuleReverse:
		rd := r.RouteDescriptor()

		summary.ConsumeFields = &RuleConsumeFields{
			RouteDescriptor: RouteDescriptorFields{
				DstPK:   rd.DstPK(),
				SrcPK:   rd.SrcPK(),
				DstPort: rd.DstPort(),
				SrcPort: rd.SrcPort(),
			},
		}
	case RuleForward:
		rd := r.RouteDescriptor()

		summary.ForwardFields = &RuleForwardFields{
			RouteDescriptor: RouteDescriptorFields{
				DstPK:   rd.DstPK(),
				SrcPK:   rd.SrcPK(),
				DstPort: rd.DstPort(),
				SrcPort: rd.SrcPort(),
			},
			NextRID: r.NextRouteID(),
			NextTID: r.NextTransportID(),
		}
	case RuleIntermediary:
		summary.IntermediaryForwardFields = &RuleIntermediaryForwardFields{
			NextRID: r.NextRouteID(),
			NextTID: r.NextTransportID(),
		}
	default:
		panic(fmt.Sprintf("invalid rule: %v", t.String()))
	}

	return &summary
}

// ConsumeRule constructs a new Consume rule.
func ConsumeRule(keepAlive time.Duration, key RouteID, lPK, rPK cipher.PubKey, lPort, rPort Port) Rule {
	rule := Rule(make([]byte, RuleHeaderSize+routeDescriptorSize))

	rule.setKeepAlive(keepAlive)
	rule.setType(RuleReverse)
	rule.SetKeyRouteID(key)

	rule.setSrcPK(lPK)
	rule.setDstPK(rPK)
	rule.setDstPort(rPort)
	rule.setSrcPort(lPort)

	return rule
}

// ForwardRule constructs a new Forward rule.
func ForwardRule(
	keepAlive time.Duration,
	key, nextRt RouteID,
	nextTp uuid.UUID,
	lPK, rPK cipher.PubKey,
	lPort, rPort Port,
) Rule {
	rule := Rule(make([]byte, RuleHeaderSize+routeDescriptorSize+4+pkSize))

	rule.setKeepAlive(keepAlive)
	rule.setType(RuleForward)
	rule.SetKeyRouteID(key)
	rule.setNextRouteID(nextRt)
	rule.setNextTransportID(nextTp)

	rule.setSrcPK(lPK)
	rule.setSrcPort(lPort)
	rule.setDstPK(rPK)
	rule.setDstPort(rPort)

	return rule
}

// IntermediaryForwardRule constructs a new IntermediaryForward rule.
func IntermediaryForwardRule(keepAlive time.Duration, key, nextRoute RouteID, nextTransport uuid.UUID) Rule {
	rule := Rule(make([]byte, RuleHeaderSize+4+pkSize))

	rule.setKeepAlive(keepAlive)
	rule.setType(RuleIntermediary)
	rule.SetKeyRouteID(key)
	rule.setNextRouteID(nextRoute)
	rule.setNextTransportID(nextTransport)

	return rule
}
