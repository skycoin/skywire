// Package clitp is the output shape of the `skywire cli tp` commands.
//
// It exists because the shape was previously written down three times: twice
// as a function-local type inside cmd/skywire-cli/commands/tp/tp.go, and again
// in internal/integration as stcpTpView so the e2e tests could unmarshal it.
// Three copies of one contract, none of which the compiler compared — and two
// of them had already drifted, one gaining an `inactive` field the other
// lacked, so the same command emitted different JSON depending on which code
// path produced it.
//
// A command's output is an API. Callers pipe it into jq, tests unmarshal it,
// and other programs depend on the field names. Declaring it inside a function
// body means nothing can import it, which guarantees the copies.
//
// # Flags change the payload, not the type
//
// Several flags add columns: --bw fills the byte counters, --more the version,
// country and services, --logs the latency. Rather than a different type per
// flag combination, the optional parts are omitempty and simply absent when
// not requested. A consumer therefore checks for a field's presence, never for
// a different document shape, and the type stays one thing.
package clitp

import (
	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// Transport is one transport as the CLI reports it.
//
// Field order here is the column order of the human table, so the two
// renderings stay legible against each other.
type Transport struct {
	Type   types.Type      `json:"type"`
	ID     uuid.UUID       `json:"id"`
	Remote cipher.PubKey   `json:"remote_pk"`
	TpMode string          `json:"mode"`
	Label  transport.Label `json:"label"`

	// Version, Country and Services are filled by --more, which costs an
	// uptime-tracker and service-discovery lookup per peer.
	Version  string `json:"version,omitempty"`
	Country  string `json:"country,omitempty"`
	Services string `json:"services,omitempty"`

	// LatencyMS is the smoothed inter-visor round-trip. Zero means no
	// measurement yet, which is not the same as zero latency — hence
	// omitempty, so a consumer can tell "not measured" from "measured fast".
	LatencyMS float64 `json:"latency_ms,omitempty"`

	// RecvBytes and SentBytes come from --bw. The e2e suite reads exactly
	// these two to prove data crossed a specific link.
	RecvBytes uint64 `json:"recv_bytes,omitempty"`
	SentBytes uint64 `json:"sent_bytes,omitempty"`

	// Inactive marks a transport known to discovery but not currently up.
	// Only the listing that reconciles against the transport discovery sets
	// it; it was the field the two former copies disagreed about.
	Inactive bool `json:"inactive,omitempty"`
}

// Transports is the list form, which is what every tp listing emits — a JSON
// array, never an object wrapping one. The e2e suite unmarshals into a slice
// and indexes it.
type Transports []Transport
