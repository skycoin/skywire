// Package skynetweb pkg/skynetweb/resolverhost.go
//
// Shared parser for resolver hostnames used by BOTH the skynet resolving
// proxy (.skynet) and the dmsg resolving proxy (.dmsg). It generalises the
// existing "<vhost>.<pk>.<suffix>" form to carry an optional routing chain:
//
//	[<vhost>.]<r1>.<r2>…<rN>.<destPK>.<suffix>[:port]
//
// The destination PK is the label immediately before the suffix (unchanged
// from the existing convention, so single-PK hostnames parse identically).
// PK-shaped labels to its LEFT are the routing chain, returned in SOURCE order
// (route[0] is nearest the source, route[len-1] is nearest the destination).
// Everything to the left of the first non-PK label is the vhost, returned for
// Host-header rewriting.
//
// The suffix decides what the routing PKs MEAN — the parser only extracts them:
//   - ".dmsg":   a single rendezvous dmsg SERVER to dial the destination through.
//   - ".skynet": the visor route HOPS (source-routed path to the destination).
//
// A PK label is recognized purely by shape (53-char base32 DNS label or 66-char
// hex), so an ordinary vhost label like "magnetosphere" or "net" is never
// mistaken for a PK. The one ambiguity intentionally left out of scope: a vhost
// label that happens to be exactly PK-shaped.
package skynetweb

import (
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ParseResolverHost parses host (e.g. from a browser's SOCKS5 CONNECT) into its
// vhost, routing chain, destination PK and port. suffix must start with "."
// (e.g. ".dmsg", ".skynet"). It returns an error when the host doesn't end in
// suffix or the destination label isn't a valid PK.
func ParseResolverHost(host, suffix string) (vhost string, route []cipher.PubKey, dest cipher.PubKey, port uint16, err error) {
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

	// Destination = the label adjacent to the suffix (must be a PK).
	dest, err = parsePKLabel(labels[last])
	if err != nil {
		return "", nil, cipher.PubKey{}, 0, fmt.Errorf("invalid destination PK %q: %w", labels[last], err)
	}

	// Walk left while labels are PK-shaped — those are the routing chain,
	// collected nearest-the-dest first. The first non-PK label is the vhost
	// boundary.
	i := last - 1
	var nearestDestFirst []cipher.PubKey
	for ; i >= 0; i-- {
		pk, perr := parsePKLabel(labels[i])
		if perr != nil {
			break
		}
		nearestDestFirst = append(nearestDestFirst, pk)
	}

	// Reverse to source order (route[0] = nearest the source).
	route = make([]cipher.PubKey, 0, len(nearestDestFirst))
	for j := len(nearestDestFirst) - 1; j >= 0; j-- {
		route = append(route, nearestDestFirst[j])
	}

	if i >= 0 {
		vhost = strings.Join(labels[:i+1], ".")
	}
	return vhost, route, dest, port, nil
}
