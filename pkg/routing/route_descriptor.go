// Package routing pkg/routing/route_descriptor.go
package routing

import (
	"encoding/binary"
	"fmt"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// RouteDescriptor describes a route (from the perspective of the source and destination edges).
type RouteDescriptor [routeDescriptorSize]byte

// NewRouteDescriptor returns a new RouteDescriptor.
func NewRouteDescriptor(srcPK, dstPK cipher.PubKey, srcPort, dstPort Port) RouteDescriptor {
	var desc RouteDescriptor

	desc.setSrcPK(srcPK)
	desc.setDstPK(dstPK)
	desc.setSrcPort(srcPort)
	desc.setDstPort(dstPort)

	return desc
}

// Src returns source Addr from RouteDescriptor.
func (rd *RouteDescriptor) Src() Addr {
	return Addr{
		PubKey: rd.SrcPK(),
		Port:   rd.SrcPort(),
	}
}

// Dst returns destination Addr from RouteDescriptor.
func (rd *RouteDescriptor) Dst() Addr {
	return Addr{
		PubKey: rd.DstPK(),
		Port:   rd.DstPort(),
	}
}

// SrcPK returns source public key from RouteDescriptor.
func (rd *RouteDescriptor) SrcPK() cipher.PubKey {
	var pk cipher.PubKey

	copy(pk[:], rd[0:pkSize])

	return pk
}

// setSrcPK sets source public key of a rule.
func (rd *RouteDescriptor) setSrcPK(pk cipher.PubKey) {
	copy(rd[:pkSize], pk[:])
}

// DstPK returns destination public key from RouteDescriptor.
func (rd *RouteDescriptor) DstPK() cipher.PubKey {
	var pk cipher.PubKey

	copy(pk[:], rd[pkSize:pkSize*2])

	return pk
}

// setDstPK sets destination public key of a rule.
func (rd *RouteDescriptor) setDstPK(pk cipher.PubKey) {
	copy(rd[pkSize:pkSize*2], pk[:])
}

// SrcPort returns source port from RouteDescriptor.
func (rd *RouteDescriptor) SrcPort() Port {
	return Port(binary.BigEndian.Uint16(rd[pkSize*2 : pkSize*2+2]))
}

// setSrcPort sets source port of a rule.
func (rd *RouteDescriptor) setSrcPort(port Port) {
	binary.BigEndian.PutUint16(rd[pkSize*2:pkSize*2+2], uint16(port))
}

// DstPort returns destination port from RouteDescriptor.
func (rd *RouteDescriptor) DstPort() Port {
	return Port(binary.BigEndian.Uint16(rd[pkSize*2+2 : pkSize*2+2*2]))
}

// setDstPort sets destination port of a rule.
func (rd *RouteDescriptor) setDstPort(port Port) {
	binary.BigEndian.PutUint16(rd[pkSize*2+2:pkSize*2+2*2], uint16(port))
}

// Invert inverts source and destination.
func (rd *RouteDescriptor) Invert() RouteDescriptor {
	return NewRouteDescriptor(rd.DstPK(), rd.SrcPK(), rd.DstPort(), rd.SrcPort())
}

func (rd *RouteDescriptor) String() string {
	return fmt.Sprintf("rAddr:%s, lAddr:%s", rd.Dst().String(), rd.Src().String())
}
