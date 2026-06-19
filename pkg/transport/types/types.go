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
)
