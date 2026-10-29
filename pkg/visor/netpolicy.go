// Package visor pkg/visor/netpolicy.go c3-vis-core
//
// The network policy, transport.network: which interfaces and addresses the
// visor offers to peers. It filters the address resolver registrations, the
// reach card and the WebRTC ICE candidates, and applies without a restart.
package visor

import (
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// installNetworkPolicy makes the configured policy the one in force. It runs
// before the first registration.
func (v *Visor) installNetworkPolicy() {
	if v.conf == nil || v.conf.Transport == nil || v.conf.Transport.Network == nil {
		return
	}
	p := addrPolicyOf(*v.conf.Transport.Network)
	if err := p.Validate(); err != nil {
		v.netMoveLog().WithError(err).Warn("Ignoring transport.network: the visor advertises every interface")
		return
	}
	netutil.SetAdvertisePolicy(p)
}

func addrPolicyOf(p visorconfig.NetworkPolicy) netutil.AddrPolicy {
	return netutil.AddrPolicy{Allow: p.Allow, Deny: p.Deny}
}

// setNetworkPolicy validates, installs and saves a policy, then registers
// the addresses it leaves.
func (v *Visor) setNetworkPolicy(p visorconfig.NetworkPolicy) error {
	ap := addrPolicyOf(p)
	if err := ap.Validate(); err != nil {
		return err
	}
	if v.conf != nil {
		if err := v.conf.UpdateNetworkPolicy(p); err != nil {
			return err
		}
	}
	netutil.SetAdvertisePolicy(ap)
	go v.readvertise()
	return nil
}

// readvertise drops the reach card and AR view and binds every transport
// type again, so the address resolver gets the current address set.
func (v *Visor) readvertise() {
	if v.initLock == nil {
		return
	}
	v.initLock.Lock()
	arClient, tpM := v.arClient, v.tpM
	v.initLock.Unlock()
	v.reach.mu.Lock()
	v.reach.own = nil
	v.reach.binds = nil
	v.reach.mu.Unlock()
	v.arSelf.invalidate()
	if m, ok := arClient.(addrresolver.NetworkMover); ok {
		m.NetworkMoved()
	}
	if tpM != nil {
		tpM.Rebind()
	}
}

// NetworkSet is the RPC entry point for `skywire cli visor network set`.
// Nil fields keep their value.
func (v *Visor) NetworkSet(in visorapi.NetworkSettings) error {
	if in.Allow == nil && in.Deny == nil {
		return nil
	}
	cur := netutil.AdvertisePolicy()
	p := visorconfig.NetworkPolicy{Allow: cur.Allow, Deny: cur.Deny}
	if in.Allow != nil {
		p.Allow = nonEmpty(*in.Allow)
	}
	if in.Deny != nil {
		p.Deny = nonEmpty(*in.Deny)
	}
	return v.setNetworkPolicy(p)
}

func nonEmpty(in []string) []string {
	var out []string
	for _, s := range in {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
