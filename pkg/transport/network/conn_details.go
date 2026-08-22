// Package network pkg/transport/network/conn_details.go c2-net-transport
package network

import (
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// ConnDetails is a curated, secrets-free snapshot of the low-level
// connection metadata that is meaningful for a given transport kind.
// It is surfaced through pkg/transport.ManagedTransport.ConnDetails and,
// from there, onto the visor's TransportSummary so `skywire cli visor
// state` / `skywire cli tp` can show what a transport actually rides on
// (the direct IP:port, the relaying dmsg server, the TLS identity, …)
// without leaking any secret key.
//
// Every field is optional (omitempty): each transport type populates
// only what it can expose cleanly. A field left empty is a deliberate
// "not applicable / not available for this kind" — e.g. dmsg leaves
// RemoteAddr empty (it relays through a server, there is no direct IP)
// and instead fills DmsgServerPK.
type ConnDetails struct {
	// RemoteAddr is the underlying raw remote network address
	// ("host:port"): the direct TCP peer for stcp/stcpr, the
	// hole-punched UDP endpoint for sudph, the QUIC peer for squicr,
	// the selected ICE candidate for webrtc. Empty for dmsg — an
	// empty RemoteAddr is itself the signal "relayed, not direct".
	RemoteAddr string `json:"remote_addr,omitempty"`
	// LocalAddr is the underlying raw local network address, when the
	// carrier exposes one.
	LocalAddr string `json:"local_addr,omitempty"`
	// ARBackedType is true for the address-resolver-backed carriers
	// (stcpr/sudph/squicr): their endpoint was discovered via the
	// address resolver (a static PK-table dial is the rare exception).
	// Direct carriers (stcp) and relayed dmsg leave it false.
	ARBackedType bool `json:"ar_backed_type,omitempty"`
	// DmsgServerPK is the hex public key of the dmsg.Server this
	// (dmsg) transport relays its frames through. dmsg-only field; it
	// is a public key, never a secret. Empty for every other type.
	DmsgServerPK string `json:"dmsg_server_pk,omitempty"`
	// TLSCertSHA256 is the hex SHA-256 fingerprint of the remote
	// peer's TLS certificate (the skywire-PK-bound QUIC identity).
	// squicr-only. Empty for every other type.
	TLSCertSHA256 string `json:"tls_cert_sha256,omitempty"`
	// ALPN is the negotiated TLS application protocol (e.g.
	// "skywire-quic-1"). squicr-only.
	ALPN string `json:"alpn,omitempty"`
}

// ConnDetailer is optionally implemented by a network.Transport to
// expose its curated ConnDetails. pkg/transport.ManagedTransport
// consumes it via a type assertion, so transports/mocks that do not
// implement it simply report no details.
type ConnDetailer interface {
	ConnDetails() ConnDetails
}

// rawConnDetailer is optionally implemented by an underlying raw
// net.Conn (captured before the noise wrapper replaces it) to
// contribute type-specific fields — the QUIC TLS identity, the webrtc
// ICE candidate — that are only reachable on the concrete pre-noise
// connection. Kept as an unexported interface so each concrete conn
// type populates it from its own (possibly build-tagged) file without
// dragging quic-go / pion into the shared connection.go.
type rawConnDetailer interface {
	rawConnDetails(d *ConnDetails)
}

// arBackedType reports whether a transport type discovers its remote
// endpoint through the address resolver.
func arBackedType(t types.Type) bool {
	switch t {
	case types.STCPR, types.SUDPH, types.QUIC:
		return true
	default:
		return false
	}
}
