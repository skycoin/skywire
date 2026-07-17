// Package tptypes pkg/transport/types/types.go c2-net-transport
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
	// QUIC (wire name "squicr") is a transport over QUIC/UDP that resolves the
	// peer address via the address-resolver. The name follows the taxonomy
	// s(kywire-Noise)+protocol+suffix: the Noise+yamux layer runs over the QUIC
	// stream exactly as for every other transport (the "s"), it is QUIC (the
	// "quic"), and "r" = address-resolver (matching stcp→stcpr). QUIC's own TLS
	// is additionally bound to the skywire static key (#2607). The pre-rename
	// names "squic" and "quic" are still accepted on input (NormalizeType / the
	// preference parser) for back-compat; only "squicr" is emitted.
	QUIC Type = "squicr"
	// QUICLegacy / QUICLegacy2 are the pre-rename wire names for QUIC ("quic" →
	// "squic" → "squicr"), accepted on input so older configs / CLI invocations /
	// discovery entries keep working. Not emitted.
	QUICLegacy  Type = "quic"
	QUICLegacy2 Type = "squic"
	// WS (wire name "swsr") is a transport that works visor-to-visor over a direct
	// WebSocket. The name follows the taxonomy s(kywire-Noise)+protocol+suffix: the
	// Noise+yamux layer runs over the WebSocket exactly as for every other
	// transport (the "s"), it is WebSocket (the "ws"), and "r" = it resolves the
	// peer's wss:// endpoint via the address-resolver when not pinned in the static
	// ws_table (matching stcp→stcpr). Unlike the dmsg WebSocket carrier (which
	// reaches a dmsg server), this is a first-class skywire transport between two
	// visors. A browser visor can DIAL it (the browser's WebSocket API) but not
	// accept it (no server) — server-visors run the WS listener; see
	// pkg/transport/network/ws_native.go / ws_tinygo.go. The pre-rename name "ws"
	// is still accepted on input (NormalizeType / the preference parser); only
	// "swsr" is emitted.
	WS Type = "swsr"
	// WSLegacy is the pre-rename wire name for WS ("ws" → "swsr"), accepted on
	// input so older configs / discovery entries keep working. Not emitted.
	WSLegacy Type = "ws"
	// WEBRTC is a type of a transport that works visor-to-visor over a direct
	// WebRTC DataChannel (DTLS+SCTP, NAT-traversed via ICE). It is the genuinely
	// peer-to-peer transport: the payload rides a direct encrypted pipe between two
	// leaves, no relay. dmsg is the signaling side-channel (SDP offer/answer + ICE
	// candidates over a dmsg stream on a fixed port); once the DataChannel opens it
	// is adapted to a net.Conn carrying the usual Noise+yamux session. Both ends can
	// be browsers (syscall/js) or native (pion); see pkg/transport/network/webrtc*.
	WEBRTC Type = "webrtc"
	// WT (wire name "swtr") is a transport that works visor-to-visor over a direct
	// WebTransport (HTTP/3 over QUIC) link. The name follows the taxonomy
	// s(kywire-Noise)+protocol+suffix: the Noise+yamux layer runs over the
	// WebTransport stream (the "s"), it is WebTransport (the "wt"), and "r" = it
	// resolves the peer's https:// endpoint + pinned cert hash via the
	// address-resolver when not pinned in the static wt_table. Like the dmsg
	// WebTransport carrier, the server presents a short-lived self-signed cert
	// pinned by SHA-256 (no CA, no domain) and the client→server PK is
	// authenticated in the Noise handshake. A browser visor can DIAL it (the
	// browser WebTransport API, serverCertificateHashes) but not accept it (no
	// server) — server-visors run the WT listener; see
	// pkg/transport/network/wt_native.go / wt_tinygo.go. The pre-rename name "wt"
	// is still accepted on input; only "swtr" is emitted.
	WT Type = "swtr"
	// WTLegacy is the pre-rename wire name for WT ("wt" → "swtr"), accepted on
	// input so older configs / discovery entries keep working. Not emitted.
	WTLegacy Type = "wt"
)

// NormalizeType maps a legacy transport-type wire name to its canonical Type, so
// older configs / CLI invocations / discovery entries keep working after a
// rename. Currently "quic"/"squic" → "squicr" (QUIC), "ws" → "swsr" (WS), and
// "wt" → "swtr" (WT). Canonical or unknown values pass through unchanged.
func NormalizeType(t Type) Type {
	switch t {
	case QUICLegacy, QUICLegacy2:
		return QUIC
	case WSLegacy:
		return WS
	case WTLegacy:
		return WT
	}
	return t
}

// Known returns every recognized transport type, in canonical form. This is the
// single source of truth callers (e.g. the CLI's `tp add` validation) should use
// instead of hard-coding a subset that drifts as new types are added.
func Known() []Type {
	return []Type{STCPR, QUIC, SUDPH, STCP, WEBRTC, WS, WT, DMSG}
}

// Valid reports whether t names a recognized transport type. Alias-aware (the
// legacy "quic" name normalizes to "squic"), so older invocations keep working.
func Valid(t Type) bool {
	switch NormalizeType(t) {
	case STCPR, QUIC, SUDPH, STCP, WEBRTC, WS, WT, DMSG:
		return true
	default:
		return false
	}
}
