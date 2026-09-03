// Package api pkg/deployment/ar/api/bind_ingest.go c4-net-discovery
//
// AR-bind-over-CXO ingest: the server side of the fan-in path where visors
// publish their AR bindings as a CXO feed instead of re-registering over a
// fresh dmsg stream on a timer (each with a full Noise handshake — the
// secp256k1 handshakeResponder that dominates AR CPU). The CXO aggregator
// (pkg/deployment/ar/regcxo) calls IngestBindFromCXO for each bind leaf it
// replicates.
//
// This is purely ADDITIVE and dual-write: the HTTP POST /bind and the SUDPH
// UDP registration remain the authoritative path and keep writing the same
// store. The CXO ingest therefore never clobbers a fresh HTTP/UDP bind:
//
//   - When a record already exists (the common case — HTTP/UDP bound first),
//     the CXO ingest is a keepalive: it re-writes the STORED VisorData to
//     refresh its TTL, off a warm CXO connection instead of a fresh handshake.
//     It writes back exactly what is there, so it can never overwrite a newer
//     HTTP/UDP address (mirrors dmsg-discovery's equal-sequence keepalive,
//     which refreshes off the stored entry, not the incoming one).
//   - When no record exists yet, only the address-POST types (STCPR/QUIC/WT)
//     take a fresh insert, reconstructed from the visor's DECLARED addresses
//     exactly as the HTTP bind handler does in the production dmsg-routed case
//     (where the observed source is the dmsg bridge and the handler falls back
//     to the declared PublicIP). SUDPH is keepalive-only: its stored address is
//     the UDP-observed NAT-mapped endpoint, which the declared payload cannot
//     reproduce, so a fresh SUDPH record is left to the UDP path.
package api

import (
	"context"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// supportedCXOBindType reports whether tpType is a transport type the AR-bind
// CXO feed carries. Anything else is dropped by the aggregator before it
// reaches ingest, but the guard is kept here too so the ingest is safe to call
// directly.
func supportedCXOBindType(tpType types.Type) bool {
	switch tpType {
	case types.STCPR, types.SUDPH, types.QUIC, types.WT:
		return true
	default:
		return false
	}
}

// IngestBindFromCXO applies an AR binding received over the AR-bind-over-CXO
// feed. reporter is the feed's publisher PK (the visor); the binding is always
// stored UNDER reporter, so a visor can only ever register its OWN address —
// the ownership check the HTTP path enforces via httpauth is structural here.
//
// See the package doc for the dual-write / no-clobber semantics.
func (a *API) IngestBindFromCXO(ctx context.Context, reporter cipher.PubKey, tpType types.Type, la addrresolver.LocalAddresses) {
	if reporter == (cipher.PubKey{}) {
		return
	}
	if !supportedCXOBindType(tpType) {
		a.log.WithField("reporter", reporter).WithField("type", tpType).
			Debug("ar-bind-cxo: unsupported transport type; dropping")
		return
	}

	// Keepalive-first: if a record already exists, refresh its TTL off the
	// STORED data. This is the common case (HTTP/UDP bound first) and the whole
	// CPU win — a warm CXO Root keeps the binding alive with no fresh Noise
	// handshake. Writing back the stored value can never clobber a newer
	// HTTP/UDP bind.
	stored, err := a.store.Resolve(ctx, tpType, reporter)
	if err == nil {
		if bErr := a.store.Bind(ctx, tpType, reporter, stored); bErr != nil {
			a.log.WithError(bErr).WithField("reporter", reporter).WithField("type", tpType).
				Debug("ar-bind-cxo: TTL keepalive refresh failed")
			return
		}
		a.mirrorForType(tpType, reporter, &stored)
		return
	}
	if err != store.ErrNoEntry && err != store.ErrUnknownTransportType {
		a.log.WithError(err).WithField("reporter", reporter).WithField("type", tpType).
			Debug("ar-bind-cxo: store lookup failed")
		return
	}

	// No record yet. SUDPH's stored endpoint is the UDP-observed NAT-mapped
	// address, which the declared payload can't reproduce — leave a fresh
	// SUDPH record to the authoritative UDP path.
	if tpType == types.SUDPH {
		return
	}

	// Fresh insert for the address-POST types, reconstructed from the declared
	// addresses exactly as the HTTP bind handler does when the observed source
	// is non-public (the production dmsg-routed default): remoteAddr = declared
	// PublicIP. Only when the declaration is a usable public address; otherwise
	// leave it to the HTTP path (which can also use the observed source IP).
	remoteAddr := la.PublicIP
	if !netutil.IsPublicIP(net.ParseIP(remoteAddr)) {
		a.log.WithField("reporter", reporter).WithField("type", tpType).
			Debug("ar-bind-cxo: no usable declared public IP; deferring fresh bind to HTTP")
		return
	}
	if !a.hasAddress(remoteAddr, la) {
		a.log.WithField("reporter", reporter).WithField("type", tpType).
			Debug("ar-bind-cxo: declared public IP not in addresses list; dropping")
		return
	}
	v4Addr, v6Addr := splitFamilyAddr(remoteAddr)
	if la.PublicIPv6 != "" && isPublicIPv6(net.ParseIP(la.PublicIPv6)) {
		v6Addr = la.PublicIPv6
	}
	visorData := addrresolver.VisorData{
		RemoteAddr:     v4Addr,
		RemoteAddrV6:   v6Addr,
		LocalAddresses: la,
	}
	if bErr := a.store.Bind(ctx, tpType, reporter, visorData); bErr != nil {
		a.log.WithError(bErr).WithField("reporter", reporter).WithField("type", tpType).
			Debug("ar-bind-cxo: fresh Bind failed")
		return
	}
	a.mirrorForType(tpType, reporter, &visorData)
	a.log.WithField("reporter", reporter).WithField("type", tpType).
		Debug("ar-bind-cxo: fresh binding ingested")
}

// mirrorForType fans the per-transport DHT mirror the HTTP bind path runs
// inline. Only STCPR and SUDPH mirror (matching bindForType / bindSUDPH);
// QUIC/WT have no DHT salt. Best-effort and nil-safe (the mirror methods
// early-return when unconfigured).
func (a *API) mirrorForType(tpType types.Type, pk cipher.PubKey, data *addrresolver.VisorData) {
	switch tpType {
	case types.STCPR:
		a.mirrorSTCPR(pk, data)
	case types.SUDPH:
		a.mirrorSUDPH(pk, data)
	}
}
