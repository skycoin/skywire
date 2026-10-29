// Package visor pkg/visor/netpolicy.go c3-vis-core
//
// The network policy, transport.network: which interfaces and addresses the
// visor offers to peers. It filters the address resolver registrations, the
// reach card and the WebRTC ICE candidates, and applies without a restart.
package visor

import (
	"slices"

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

// networkPolicy is a copy of transport.network, the zero policy when unset.
func (v *Visor) networkPolicy() visorconfig.NetworkPolicy {
	if v.conf == nil || v.conf.Transport == nil || v.conf.Transport.Network == nil {
		return visorconfig.NetworkPolicy{}
	}
	p := *v.conf.Transport.Network
	p.Allow = slices.Clone(p.Allow)
	p.Deny = slices.Clone(p.Deny)
	p.MeteredInterfaces = slices.Clone(p.MeteredInterfaces)
	return p
}

func validateNetworkPolicy(p visorconfig.NetworkPolicy) error {
	if err := addrPolicyOf(p).Validate(); err != nil {
		return err
	}
	for _, e := range p.MeteredInterfaces {
		if err := netutil.ValidatePolicyEntry(e); err != nil {
			return err
		}
	}
	return validMeteredMode(p.Metered)
}

// setNetworkPolicy validates, installs and saves a policy. A change to the
// advertised set registers the addresses it leaves, and the metered mode is
// evaluated again.
func (v *Visor) setNetworkPolicy(p visorconfig.NetworkPolicy) error {
	if err := validateNetworkPolicy(p); err != nil {
		return err
	}
	old := v.networkPolicy()
	if v.conf != nil {
		if err := v.conf.UpdateNetworkPolicy(p); err != nil {
			return err
		}
	}
	netutil.SetAdvertisePolicy(addrPolicyOf(p))
	if !slices.Equal(old.Allow, p.Allow) || !slices.Equal(old.Deny, p.Deny) {
		go v.readvertise()
	}
	v.meterEvaluate()
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
	if in.Metered != nil {
		if err := v.setMeteredOverride(*in.Metered); err != nil {
			return err
		}
	}
	if in.Allow == nil && in.Deny == nil && in.MeteredMode == nil && in.MeteredInterfaces == nil {
		v.applyMeterMode()
		return nil
	}
	p := v.networkPolicy()
	if in.Allow != nil {
		p.Allow = nonEmpty(*in.Allow)
	}
	if in.Deny != nil {
		p.Deny = nonEmpty(*in.Deny)
	}
	if in.MeteredMode != nil {
		p.Metered = *in.MeteredMode
	}
	if in.MeteredInterfaces != nil {
		p.MeteredInterfaces = nonEmpty(*in.MeteredInterfaces)
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
