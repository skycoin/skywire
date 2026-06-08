// Package skynetweb pkg/skynetweb/resolverhost.go
//
// Shared parser for resolver hostnames used by BOTH the skynet resolving
// proxy (.skynet) and the dmsg resolving proxy (.dmsg). It generalizes the
// existing "<vhost>.<pk>.<suffix>" form to carry an optional routing chain:
//
//	[<vhost>.]<r1>.<r2>…<rN>.<destPK>.<suffix>[:port]
//
// The destination PK is the label immediately before the suffix (unchanged
// from the existing convention, so single-PK hostnames parse identically).
// Labels to its LEFT that are PK-shaped or TpID-shaped form the routing chain,
// returned in SOURCE order (route[0] nearest the source, route[len-1] nearest
// the destination). Everything to the left of the first label that is neither a
// PK nor a TpID is the vhost, returned for Host-header rewriting.
//
// The suffix decides what the routing labels MEAN — the parser only extracts
// and classifies them:
//   - ".dmsg":   a single rendezvous dmsg SERVER (a PK) to dial the dest through.
//   - ".skynet": the route to the destination, where each hop is either a visor
//     PK (any/auto transport) or a specific transport ID (uuid).
//
// Classification is purely by shape — a PK label is 53-char base32 or 66-char
// hex; a TpID label is a 32-char hex or 36-char dashed uuid. An ordinary vhost
// label ("magnetosphere", "net") matches none of these. The one ambiguity left
// out of scope: a vhost label that is itself exactly PK- or TpID-shaped.
package skynetweb

import (
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// RouteLabel is one parsed routing-chain element: either a visor public key
// (auto/any transport) or a specific transport ID.
type RouteLabel struct {
	IsTpID bool
	PK     cipher.PubKey // valid when !IsTpID
	TpID   uuid.UUID     // valid when IsTpID
}

// classifyRouteLabel reports whether label is a routing element (PK or TpID)
// and, if so, which. A non-routing label (the vhost boundary) returns ok=false.
// PK and TpID shapes don't overlap (66/53 vs 32/36 chars), so order is safe.
func classifyRouteLabel(label string) (RouteLabel, bool) {
	if pk, err := parsePKLabel(label); err == nil {
		return RouteLabel{PK: pk}, true
	}
	if id, err := uuid.Parse(label); err == nil {
		return RouteLabel{IsTpID: true, TpID: id}, true
	}
	return RouteLabel{}, false
}

// ParseResolverHost parses host into its vhost, routing chain, destination PK
// and port. suffix must start with "." (e.g. ".dmsg", ".skynet"). It errors
// when the host doesn't end in suffix or the destination label isn't a valid PK.
func ParseResolverHost(host, suffix string) (vhost string, route []RouteLabel, dest cipher.PubKey, port uint16, err error) {
	hostPart, portStr, hasPort := splitHostPort(host)
	port = 80
	if hasPort {
		p, perr := parseUint16(portStr)
		if perr != nil {
			return "", nil, cipher.PubKey{}, 0, fmt.Errorf("invalid port %q: %w", portStr, perr)
		}
		port = p
	}

	if len(hostPart) <= len(suffix) || hostPart[len(hostPart)-len(suffix):] != suffix {
		return "", nil, cipher.PubKey{}, 0, fmt.Errorf("host %q has no suffix %q", hostPart, suffix)
	}
	stripped := hostPart[:len(hostPart)-len(suffix)]
	if stripped == "" {
		return "", nil, cipher.PubKey{}, 0, fmt.Errorf("host %q has no labels before %q", host, suffix)
	}

	labels := strings.Split(stripped, ".")
	last := len(labels) - 1

	// Destination = the label adjacent to the suffix (must be a PK — a route
	// always ends at a visor, never a transport).
	dest, err = parsePKLabel(labels[last])
	if err != nil {
		return "", nil, cipher.PubKey{}, 0, fmt.Errorf("invalid destination PK %q: %w", labels[last], err)
	}

	// Walk left while labels classify as routing elements (PK or TpID),
	// collecting nearest-the-dest first. The first non-routing label is the
	// vhost boundary.
	i := last - 1
	var nearestDestFirst []RouteLabel
	for ; i >= 0; i-- {
		rl, ok := classifyRouteLabel(labels[i])
		if !ok {
			break
		}
		nearestDestFirst = append(nearestDestFirst, rl)
	}

	// Reverse to source order (route[0] = nearest the source).
	route = make([]RouteLabel, 0, len(nearestDestFirst))
	for j := len(nearestDestFirst) - 1; j >= 0; j-- {
		route = append(route, nearestDestFirst[j])
	}

	if i >= 0 {
		vhost = strings.Join(labels[:i+1], ".")
	}
	return vhost, route, dest, port, nil
}
