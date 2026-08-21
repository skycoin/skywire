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
	"fmt"
	"io"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	store "github.com/skycoin/skywire/pkg/transport-discovery/store"
	"github.com/skycoin/skywire/pkg/transport/network"
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

	// RemoteIP is the direct remote IP of the underlying connection (empty
	// for dmsg, which relays through a server). RemoteCountry is the ISO
	// country the embedded geoip db maps it to. Both come straight from the
	// visor (TransportSummary), independent of the --more service-discovery
	// enrichment that fills Country above.
	RemoteIP      string `json:"remote_ip,omitempty"`
	RemoteCountry string `json:"remote_country,omitempty"`

	// Endpoint is the per-transport-type low-level connection metadata: the
	// direct IP:port for stcp/stcpr/sudph/squicr, the relaying dmsg server
	// PK for dmsg, the QUIC TLS fingerprint/ALPN for squicr, the selected
	// ICE candidate for webrtc. Secrets-free. nil when the visor reports
	// none. Carried verbatim from visor.TransportSummary.Endpoint.
	Endpoint *network.ConnDetails `json:"endpoint,omitempty"`
}

// Transports is the list form, which is what every tp listing emits — a JSON
// array, never an object wrapping one. The e2e suite unmarshals into a slice
// and indexes it.
type Transports []Transport

// Metric is one transport in `tp metrics`: its byte counters and, when the
// store has one, its latency sample.
type Metric struct {
	ID        string                  `json:"id"`
	Type      string                  `json:"type"`
	EdgeA     string                  `json:"edge_a"`
	EdgeB     string                  `json:"edge_b"`
	Sent      uint64                  `json:"sent"`
	Recv      uint64                  `json:"recv"`
	Bandwidth uint64                  `json:"bandwidth"`
	Latency   *store.TransportLatency `json:"latency,omitempty"`
}

// VisorBandwidth aggregates one visor's transports. The latency accumulators
// are json:"-" because they are the means of computing the average, not part
// of the answer.
type VisorBandwidth struct {
	Sent         uint64 `json:"sent"`
	Recv         uint64 `json:"recv"`
	Bandwidth    uint64 `json:"bandwidth"`
	Transports   int    `json:"transports"`
	LatencySumUS int64  `json:"-"`
	LatencyN     int    `json:"-"`
	// LatencyAvgMS is the average of the samples above, in milliseconds.
	LatencyAvgMS int64 `json:"latency_avg_ms,omitempty"`
}

// VisorMetric pairs a visor with its aggregate, for the by-visor listing.
type VisorMetric struct {
	PK string         `json:"pk"`
	BW VisorBandwidth `json:"bandwidth"`
}

// TransportInfo is one transport under a visor in the tree listing.
type TransportInfo struct {
	ID        string                  `json:"id"`
	Type      string                  `json:"type"`
	Remote    string                  `json:"remote"`
	Sent      uint64                  `json:"sent"`
	Recv      uint64                  `json:"recv"`
	Bandwidth uint64                  `json:"bandwidth"`
	Latency   *store.TransportLatency `json:"latency,omitempty"`
}

// VisorTree is a visor and the transports beneath it.
type VisorTree struct {
	Sent       uint64          `json:"sent"`
	Recv       uint64          `json:"recv"`
	Bandwidth  uint64          `json:"bandwidth"`
	Transports []TransportInfo `json:"transports"`
}

// DiscEntry is one transport-discovery record from `tp disc`.
type DiscEntry struct {
	ID    uuid.UUID     `json:"id"`
	Type  types.Type    `json:"type"`
	Edge1 cipher.PubKey `json:"edge1"`
	Edge2 cipher.PubKey `json:"edge2"`
}

// ID is the deterministic transport ID computed from two public keys by
// `tp id`. A bare string in text mode; an object in JSON so the field has a
// name a caller can select. It replaces the last use of the old
// `{"output": …}` envelope, which forced consumers through jq '.output'.
type ID struct {
	ID string `json:"id"`
}

// Human prints the identifier alone, as before.
func (i ID) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, i.ID)
	return err
}

// RouteAddr is the dot-joined route address `tp route-addr` builds.
type RouteAddr struct {
	Address string `json:"address"`
	// Labels are the hops it was assembled from, which the joined form loses.
	Labels []string `json:"labels,omitempty"`
}

// Human prints the address alone, as before.
func (r RouteAddr) Human(w io.Writer) error {
	_, err := fmt.Fprintln(w, r.Address)
	return err
}

// EdgeDial is one attempt to reach a peer's transport edge.
type EdgeDial struct {
	PK   string `json:"pk"`
	Type string `json:"type,omitempty"`
	OK   bool   `json:"ok"`
	Err  string `json:"error,omitempty"`
}

// EdgeConnect is the result of `tp add-edge`: who was tried and what happened.
type EdgeConnect struct {
	Target    string     `json:"target"`
	Attempted int        `json:"attempted"`
	Connected int        `json:"connected"`
	Edges     []EdgeDial `json:"edges,omitempty"`
	// Note explains an empty run — no transports, or no edges but self.
	Note string `json:"note,omitempty"`
}

// Human writes the per-edge ticks and the summary the command printed.
func (e EdgeConnect) Human(w io.Writer) error {
	if e.Note != "" {
		_, err := fmt.Fprintf(w, "%s\n", e.Note)
		return err
	}
	for _, d := range e.Edges {
		var err error
		if d.OK {
			_, err = fmt.Fprintf(w, "✓ %s (%s)\n", d.PK, d.Type)
		} else {
			_, err = fmt.Fprintf(w, "✗ %s: %s\n", d.PK, d.Err)
		}
		if err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nConnected to %d/%d edges of %s\n", e.Connected, e.Attempted, e.Target)
	return err
}
