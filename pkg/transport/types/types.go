// Package tptypes pkg/transport/types/types.go
package tptypes

// Type is a type of network. Type affects the way connection is established
// and the way data is sent
type Type string

const (
	// STCPR is a type of a transport that works via TCP and resolves addresses using address-resolver service.
	STCPR Type = "stcpr"
	// SUDPH is a type of a transport that works via UDP, resolves addresses using address-resolver service,
	// and uses UDP hole punching.
	SUDPH Type = "sudph"
	// STCP is a type of a transport that works via TCP and resolves addresses using PK table.
	STCP Type = "stcp"
	// DMSG is a type of a transport that works through an intermediary service
	DMSG Type = "dmsg"
	// QUIC is a type of a transport that works via QUIC over UDP, resolving
	// addresses using the address-resolver service. Gives reliable streams
	// plus RFC 9221 unreliable datagrams; the TLS session is bound to the
	// skywire static key (#2607 QUIC follow-on).
	QUIC Type = "quic"
	// WS is a type of a transport that works visor-to-visor over a direct
	// WebSocket, resolving the peer's wss:// endpoint via a PK table. Unlike the
	// dmsg WebSocket carrier (which reaches a dmsg server), this is a first-class
	// skywire transport between two visors. A browser visor can DIAL it (the
	// browser's WebSocket API) but not accept it (no server) — server-visors run
	// the WS listener; see pkg/transport/network/ws_native.go / ws_tinygo.go.
	WS Type = "ws"
	// WT is a type of a transport that works visor-to-visor over a direct
	// WebTransport (HTTP/3 over QUIC) link, resolving the peer's https:// endpoint
	// and pinned cert hash via a table. Like the dmsg WebTransport carrier, the
	// server presents a short-lived self-signed cert pinned by SHA-256 (no CA, no
	// domain) and the client→server PK is authenticated in the Noise handshake. A
	// browser visor can DIAL it (the browser WebTransport API, serverCertificate-
	// Hashes) but not accept it (no server) — server-visors run the WT listener;
	// see pkg/transport/network/wt_native.go / wt_tinygo.go.
	WT Type = "wt"
)
