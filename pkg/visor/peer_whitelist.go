// Package visor pkg/visor/peer_whitelist.go c3-vis-core
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
	"github.com/skycoin/skywire/pkg/visor/visorapi"
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
	if err := v.peerWhitelist.Add(pks...); err != nil {
		return err
	}
	// The peer whitelist gates the visor's service-consumed CXO feeds
	// (stats, tp-list, registration). Recompute + re-apply their allowlists
	// so a newly-trusted hypervisor can immediately subscribe to them.
	v.refreshGatedCXOAllowlists()
	return nil
}

// AddPtyWhitelist is the RPC gateway for Visor.AddPtyWhitelist.
func (r *RPC) AddPtyWhitelist(in *visorapi.AddPtyWhitelistIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddPtyWhitelist", in)(nil, &err)

	return r.visor.AddPtyWhitelist(in.PKs)
}

// setPtyWhitelistLive replaces the configured pty whitelist with want. Added
// keys are admitted at once; removed keys lose trust unless they are this
// visor or a configured hypervisor.
func (v *Visor) setPtyWhitelistLive(want []cipher.PubKey) error {
	var had []cipher.PubKey
	if v.conf.Pty != nil {
		had = append(had, v.conf.Pty.Whitelist...)
	}
	if err := v.conf.UpdatePtyWhitelist(want); err != nil {
		return err
	}
	if v.peerWhitelist == nil {
		return nil
	}
	if len(want) > 0 {
		if err := v.peerWhitelist.Add(want...); err != nil {
			return err
		}
	}
	for _, pk := range had {
		if pkIn(want, pk) || pkIn(v.configuredHypervisors(), pk) {
			continue
		}
		v.dropPeerTrust(pk)
	}
	v.refreshGatedCXOAllowlists()
	return nil
}

func pkIn(set []cipher.PubKey, pk cipher.PubKey) bool {
	for _, p := range set {
		if p == pk {
			return true
		}
	}
	return false
}
