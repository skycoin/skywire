// Package visor pkg/visor/peer_whitelist.go
//
// peerWhitelist is the visor's single source of truth for "which peer
// PKs may manage this visor". Every management surface consults it
// live, per-connection: the dmsgpty host, dmsgscp, the /pty web
// terminal, and the transport-RPC / dmsg-gRPC / dmsg visor-RPC
// servers. Because they all hold the same mutable reference, a
// runtime addition propagates to all of them at once.
//
// Transitive trust: a visor configured with hypervisor H connects to
// H and serves its RPC to it. On accept, H pushes its OWN configured
// hypervisors to the visor via AddPtyWhitelist, so a super-hypervisor
// SH that manages H can also reach the visor — trust flows up the
// hypervisor chain without the operator re-listing SH on every visor.
package visor

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/pty"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// newPeerWhitelist builds the shared authorized-peer whitelist from
// static config: the visor's own PK (local self-management), its
// configured hypervisors, and any explicit pty whitelist.
func newPeerWhitelist(conf *visorconfig.V1) pty.Whitelist {
	wl := pty.NewMemoryWhitelist()
	pks := []cipher.PubKey{conf.PK}
	pks = append(pks, conf.Hypervisors...)
	if conf.Pty != nil {
		pks = append(pks, conf.Pty.Whitelist...)
	}
	_ = wl.Add(pks...) //nolint:errcheck,gosec // memoryWhitelist.Add never errors
	return wl
}

// AddPtyWhitelist merges PKs into the live shared whitelist, extending
// pty + RPC trust to peers a connected hypervisor vouches for (its own
// hypervisors). Idempotent. Every mutation is logged with full PKs for
// audit. Implements API.
func (v *Visor) AddPtyWhitelist(pks []cipher.PubKey) error {
	if v.peerWhitelist == nil || len(pks) == 0 {
		return nil
	}
	for _, pk := range pks {
		v.log.WithField("pk", pk.String()).
			Info("Adding PK to peer whitelist (transitive hypervisor trust)")
	}
	return v.peerWhitelist.Add(pks...)
}

// AddPtyWhitelistIn carries the PKs to merge into the visor's shared
// peer whitelist (a hypervisor pushing its own hypervisors).
type AddPtyWhitelistIn struct {
	PKs []cipher.PubKey
}

// AddPtyWhitelist is the RPC gateway for Visor.AddPtyWhitelist.
func (r *RPC) AddPtyWhitelist(in *AddPtyWhitelistIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddPtyWhitelist", in)(nil, &err)

	return r.visor.AddPtyWhitelist(in.PKs)
}
