// Package visor pkg/visor/lan_dmsg_discovery.go
// Auto-discovery of LAN DMSG servers from hypervisors.
package visor

import (
	"context"
	"time"

	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
)

// discoverLANDmsgServer attempts to connect to a previously saved LAN DMSG server
// for the given hypervisor. If no saved entry exists, the hypervisor will push the
// LAN server info via the SetLANDmsgServer RPC when it connects.
func (v *Visor) discoverLANDmsgServer() {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if err := v.mustWaitDmsgReady(); err != nil {
		log.WithError(err).Warn("DMSG not ready, skipping LAN server discovery")
		return
	}

	// Try saved LAN servers from config
	if v.conf.Dmsg != nil && len(v.conf.Dmsg.LANServers) > 0 {
		for _, entry := range v.conf.Dmsg.LANServers {
			log.Infof("Trying saved LAN DMSG server: %s @ %s", entry.Static.String()[:16]+"...", entry.Server.Address)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			err := v.dmsgC.EnsureSession(ctx, entry)
			cancel()
			if err == nil {
				log.Info("Connected to saved LAN DMSG server")
				return
			}
			log.WithError(err).Debug("Saved LAN server unreachable — will wait for hypervisor push")
		}
	}

	// If no saved LAN server, the hypervisor will push the info when it connects.
	// Nothing more to do here.
	log.Debug("No saved LAN DMSG server — waiting for hypervisor to push LAN server info")
}

// SetLANDmsgServer is called by the hypervisor (via RPC) to inform this visor
// about the hypervisor's LAN DMSG server. The visor connects to it and saves
// the entry to config for future startups.
func (v *Visor) SetLANDmsgServer(info LANDmsgServerInfo) error {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if !info.Enabled || info.PK.Null() || info.Address == "" {
		return nil
	}

	log.Infof("Hypervisor pushed LAN DMSG server: PK=%s Address=%s", info.PK.String()[:16]+"...", info.Address)

	lanEntry := &dmsgdisc.Entry{
		Static: info.PK,
		Server: &dmsgdisc.Server{
			Address:           info.Address,
			AvailableSessions: 100,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := v.dmsgC.EnsureSession(ctx, lanEntry); err != nil {
		log.WithError(err).Warn("Failed to connect to pushed LAN DMSG server")
		return err
	}

	log.Info("Connected to LAN DMSG server — local traffic stays on LAN")

	// Save to config
	v.saveLANServerToConfig(&info)
	return nil
}

// saveLANServerToConfig saves the discovered LAN server to the visor's config.
func (v *Visor) saveLANServerToConfig(info *LANDmsgServerInfo) {
	log := v.MasterLogger().PackageLogger("lan_dmsg_discovery")

	if v.conf == nil || v.conf.Dmsg == nil {
		return
	}

	entry := &dmsgdisc.Entry{
		Static: info.PK,
		Server: &dmsgdisc.Server{
			Address:           info.Address,
			AvailableSessions: 100,
		},
	}

	v.conf.Dmsg.LANServers = []*dmsgdisc.Entry{entry}
	log.Infof("Saved LAN DMSG server to config: %s @ %s", info.PK.String()[:16]+"...", info.Address)

	if err := v.conf.Flush(); err != nil {
		log.WithError(err).Warn("Failed to persist config with LAN DMSG server")
	}
}
