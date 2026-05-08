// Package visor pkg/visor/lan_dmsg_discovery.go
// Auto-discovery of LAN DMSG servers from hypervisors.
package visor

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
)

// dialLANTimeout bounds each address attempt when reaching the hypervisor's
// embedded dmsg server. Short enough that a wrong / unreachable LAN entry
// gives way to the public address quickly; long enough to ride out a small
// network blip.
const dialLANTimeout = 5 * time.Second

// discoverLANDmsgServer attempts to connect to a previously saved LAN DMSG
// server for the given hypervisor. Tries each saved entry in turn — usually
// the LAN entry first, then the operator-set public address, so a managed
// visor that's no longer on the same subnet as the hypervisor still gets a
// path through. If no saved entries exist, the hypervisor will push the
// info via the SetLANDmsgServer RPC when it connects.
func (v *Visor) discoverLANDmsgServer() {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if err := v.mustWaitDmsgReady(); err != nil {
		log.WithError(err).Warn("DMSG not ready, skipping LAN server discovery")
		return
	}

	if v.conf.Dmsg == nil || len(v.conf.Dmsg.LANServers) == 0 {
		log.Debug("No saved LAN DMSG server — waiting for hypervisor to push LAN server info")
		return
	}

	for _, entry := range v.conf.Dmsg.LANServers {
		log.Infof("Trying saved embedded DMSG server: %s @ %s", entry.Static.String(), entry.Server.Address)
		ctx, cancel := context.WithTimeout(context.Background(), dialLANTimeout*2)
		err := v.dmsgC.EnsureSession(ctx, entry)
		cancel()
		if err == nil {
			log.Infof("Connected to saved embedded DMSG server @ %s", entry.Server.Address)
			return
		}
		log.WithError(err).Debugf("Saved embedded DMSG server unreachable @ %s — trying next", entry.Server.Address)
	}

	log.Debug("No saved embedded DMSG server reachable — waiting for hypervisor to push LAN server info")
}

// SetLANDmsgServer is called by the hypervisor (via RPC) to inform this visor
// about the hypervisor's embedded DMSG server. The visor connects to it (LAN
// address first, public fallback if set) and saves both entries to config so
// future restarts can reach the server without depending on a fresh push.
func (v *Visor) SetLANDmsgServer(info LANDmsgServerInfo) error {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if !info.Enabled || info.PK.Null() {
		return nil
	}
	if info.Address == "" && info.PublicAddress == "" {
		return nil
	}

	log.Infof("Hypervisor pushed embedded DMSG server: PK=%s Address=%s PublicAddress=%s",
		info.PK.String(), info.Address, info.PublicAddress)

	addresses := candidateAddresses(&info)

	var connectedAddr string
	for _, addr := range addresses {
		entry := serverEntry(info.PK, addr)
		ctx, cancel := context.WithTimeout(context.Background(), dialLANTimeout)
		err := v.dmsgC.EnsureSession(ctx, entry)
		cancel()
		if err == nil {
			connectedAddr = addr
			break
		}
		log.WithError(err).Debugf("Pushed embedded DMSG server unreachable @ %s — trying next", addr)
	}

	if connectedAddr == "" {
		log.Warn("Failed to connect to any pushed embedded DMSG server address")
		// Still save what we got — a future restart may succeed even if this push didn't.
		v.saveLANServerToConfig(&info)
		v.saveHypervisorDiscoveryURL(info.DiscoveryURL)
		return nil
	}

	log.Infof("Connected to embedded DMSG server @ %s", connectedAddr)
	v.saveLANServerToConfig(&info)
	v.saveHypervisorDiscoveryURL(info.DiscoveryURL)
	return nil
}

// saveHypervisorDiscoveryURL persists the hypervisor-hosted dmsg-
// discovery proxy URL so the next visor restart wires it up as the
// primary discovery (with the existing public discovery as fallback).
// We do not reconfigure the running dmsg client — the visor uses the
// new URL on next start. Call with empty string to clear.
func (v *Visor) saveHypervisorDiscoveryURL(url string) {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")
	if v.conf == nil || v.conf.Dmsg == nil {
		return
	}
	if v.conf.Dmsg.HypervisorDiscovery == url {
		// No change — skip the disk write.
		return
	}
	v.conf.Dmsg.HypervisorDiscovery = url
	if len(v.conf.Dmsg.Deployments) > 0 {
		v.conf.Dmsg.Deployments[0].HypervisorDiscovery = url
	}
	if url != "" {
		log.Infof("Saved hypervisor dmsg-discovery URL to config (effective on next restart): %s", url)
	} else {
		log.Info("Cleared hypervisor dmsg-discovery URL from config")
	}
	if err := v.conf.Flush(); err != nil {
		log.WithError(err).Warn("Failed to persist hypervisor discovery URL to config")
	}
}

// candidateAddresses returns the addresses to try, in order: LAN first,
// public second. Empty slots are skipped; identical LAN/public collapse.
func candidateAddresses(info *LANDmsgServerInfo) []string {
	addresses := make([]string, 0, 2)
	if info.Address != "" {
		addresses = append(addresses, info.Address)
	}
	if info.PublicAddress != "" && info.PublicAddress != info.Address {
		addresses = append(addresses, info.PublicAddress)
	}
	return addresses
}

func serverEntry(pk cipher.PubKey, address string) *dmsgdisc.Entry {
	return &dmsgdisc.Entry{
		Static: pk,
		Server: &dmsgdisc.Server{
			Address:           address,
			AvailableSessions: 100,
		},
	}
}

// saveLANServerToConfig saves both LAN and public address entries to the
// visor's config so future startups can re-attempt either path.
func (v *Visor) saveLANServerToConfig(info *LANDmsgServerInfo) {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if v.conf == nil || v.conf.Dmsg == nil {
		return
	}

	addresses := candidateAddresses(info)
	if len(addresses) == 0 {
		return
	}

	entries := make([]*dmsgdisc.Entry, 0, len(addresses))
	for _, addr := range addresses {
		entries = append(entries, serverEntry(info.PK, addr))
	}

	v.conf.Dmsg.LANServers = entries
	log.Infof("Saved embedded DMSG server entries to config: PK=%s addresses=%v", info.PK.String(), addresses)

	if err := v.conf.Flush(); err != nil {
		log.WithError(err).Warn("Failed to persist config with embedded DMSG server entries")
	}
}
