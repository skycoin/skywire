//go:build !tinygo || (js && wasm)

// Package router pkg/router/transport_query.go c2-net-routing
//
// RSN-oracle transport-list query protocol (phase-1).
//
// A source visor S that wants to build a SINGLE-INTERMEDIATE (2-hop) route
// S->I->D needs only two transport sets: its OWN (known locally) and the
// destination D's OWN. The intermediate set is their overlap in peer PKs. D's
// transports are authoritative and fresh from D itself, so a 2-hop route can be
// computed WITHOUT the transport-discovery service (TPD) — TPD is only needed
// for routes with >=2 intermediates.
//
// This file defines the query the source carries to the destination, reusing
// the cascade RSN-signature primitive (cipher.Sign/Verify against a per-visor
// trusted-RSN allowlist): the setup node (RSN) signs a query CAPABILITY
// targeting D, and D honors the query only if the RSN is on its trust list and
// the signature verifies for D's own PK — exactly the authorization model
// CascadeSetup.Verify already enforces (see cascade.go). The query is a
// one-shot control-plane message, so it marshals as JSON (the compact-entry
// response already carries json tags); the cascade's hand-rolled binary form is
// reserved for the per-hop data-plane-adjacent path.
package router

import (
	"encoding/binary"
	"encoding/json"
	"errors"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	// ErrTransportQueryUntrustedRSN is returned when the query's RSN public key
	// is not on the responding visor's trusted-RSN allowlist. Mirrors
	// routing.ErrCascadeUntrustedRSN for the query path.
	ErrTransportQueryUntrustedRSN = errors.New("transport-query: RSN public key not trusted")
	// ErrTransportQuerySigInvalid is returned when the RSN signature does not
	// verify against the responding visor's own PK.
	ErrTransportQuerySigInvalid = errors.New("transport-query: RSN signature verification failed")
	// ErrTransportQueryWrongTarget is returned when the query's TargetPK is not
	// the responding visor's own PK (a query signed for a different destination
	// must not be answered by this one).
	ErrTransportQueryWrongTarget = errors.New("transport-query: target PK mismatch")
)

// TransportQuery is an RSN-signed capability that authorizes the requester S to
// learn destination D's transport list. The RSN signs (RSNPK, TargetPK,
// RequesterPK, Nonce); D verifies the signature against its OWN PK and its
// trusted-RSN allowlist before responding. The signature binds the query to a
// single destination and requester, so it cannot be replayed against another
// visor or claimed by another requester.
type TransportQuery struct {
	RSNPK       cipher.PubKey `json:"rsn_pk"`       // RSN that authorized this query
	TargetPK    cipher.PubKey `json:"target_pk"`    // destination D whose transports are requested
	RequesterPK cipher.PubKey `json:"requester_pk"` // source S making the request
	Nonce       uint64        `json:"nonce"`        // anti-replay, unique per query
	RSNSig      cipher.Sig    `json:"rsn_sig"`      // RSN signature over SignablePayload
}

// SignablePayload returns the bytes signed/verified for this query: the RSN,
// target and requester PKs plus the nonce. The layout mirrors
// CascadeSetup.SignablePayload so the same cipher.Sign/Verify discipline applies.
func (q *TransportQuery) SignablePayload() []byte {
	// RSNPK(33) + TargetPK(33) + RequesterPK(33) + Nonce(8)
	buf := make([]byte, 33+33+33+8)
	off := 0
	copy(buf[off:], q.RSNPK[:])
	off += 33
	copy(buf[off:], q.TargetPK[:])
	off += 33
	copy(buf[off:], q.RequesterPK[:])
	off += 33
	binary.BigEndian.PutUint64(buf[off:], q.Nonce)
	return buf
}

// Sign signs the query with the RSN's secret key. Reuses the cascade signing
// primitive: SHA256 of the signable payload, signed by the RSN.
func (q *TransportQuery) Sign(rsnSK cipher.SecKey) error {
	hash := cipher.SumSHA256(q.SignablePayload())
	sig, err := cipher.SignPayload(hash[:], rsnSK)
	if err != nil {
		return err
	}
	q.RSNSig = sig
	return nil
}

// Verify checks the query against the responding visor's own PK and its
// trusted-RSN allowlist. It enforces, in order: (1) TargetPK is us, (2) the RSN
// is trusted, (3) the signature verifies. Same authorization model as
// CascadeHandler.handleSetup.
func (q *TransportQuery) Verify(localPK cipher.PubKey, trustedRSNs map[cipher.PubKey]struct{}) error {
	if q.TargetPK != localPK {
		return ErrTransportQueryWrongTarget
	}
	if _, ok := trustedRSNs[q.RSNPK]; !ok {
		return ErrTransportQueryUntrustedRSN
	}
	hash := cipher.SumSHA256(q.SignablePayload())
	if err := cipher.VerifyPubKeySignedPayload(q.RSNPK, q.RSNSig, hash[:]); err != nil {
		return ErrTransportQuerySigInvalid
	}
	return nil
}

// Marshal serializes the query as JSON.
func (q *TransportQuery) Marshal() ([]byte, error) { return json.Marshal(q) }

// UnmarshalTransportQuery deserializes a query from JSON.
func UnmarshalTransportQuery(data []byte) (*TransportQuery, error) {
	q := &TransportQuery{}
	if err := json.Unmarshal(data, q); err != nil {
		return nil, err
	}
	return q, nil
}

// TransportQueryResponse carries destination D's OWN transport list in the
// compact wire form (remote edge + type; see transport.CompactEntry). The
// requester reconstructs full entries with transport.EntryFromCompact(TargetPK,
// entry) — the reporter being D itself.
type TransportQueryResponse struct {
	TargetPK cipher.PubKey            `json:"target_pk"`
	Entries  []transport.CompactEntry `json:"entries"`
}

// Marshal serializes the response as JSON.
func (r *TransportQueryResponse) Marshal() ([]byte, error) { return json.Marshal(r) }

// UnmarshalTransportQueryResponse deserializes a response from JSON.
func UnmarshalTransportQueryResponse(data []byte) (*TransportQueryResponse, error) {
	r := &TransportQueryResponse{}
	if err := json.Unmarshal(data, r); err != nil {
		return nil, err
	}
	return r, nil
}

// BuildTransportQueryResponse is the DESTINATION-side (D) handler. Given a
// signed query, the responding visor's PK, its trusted-RSN allowlist and its
// transport manager, it verifies the RSN authorization and returns D's own live
// data-plane transports in compact form.
//
// It applies the same exclusions the local route calculation uses: closed /
// black-holed transports and LabelSetup (RSN control-plane) transports are
// never advertised as route-able edges. The response is built by walking D's
// transports and compacting each relative to D's own PK, so every entry
// reconstructs (via EntryFromCompact) to the canonical Entry both endpoints
// agree on.
func BuildTransportQueryResponse(
	q *TransportQuery,
	localPK cipher.PubKey,
	trustedRSNs map[cipher.PubKey]struct{},
	tm *transport.Manager,
) (*TransportQueryResponse, error) {
	if err := q.Verify(localPK, trustedRSNs); err != nil {
		return nil, err
	}
	resp := &TransportQueryResponse{TargetPK: localPK}
	if tm == nil {
		return resp, nil
	}
	tm.WalkTransports(func(tp *transport.ManagedTransport) bool {
		if tp == nil || tp.IsClosed() {
			return true
		}
		if tp.Entry.Label == transport.LabelSetup {
			return true
		}
		resp.Entries = append(resp.Entries, tp.Entry.ToCompact(localPK))
		return true
	})
	return resp, nil
}
