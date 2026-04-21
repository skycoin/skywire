package visor

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/dht"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

// DmsgPorterStatus contains DMSG porter diagnostics.
type DmsgPorterStatus struct {
	MainPorts int `json:"main_ports"`
	RSNPorts  int `json:"rsn_ports,omitempty"`
	MainFreed int `json:"main_freed,omitempty"`
	RSNFreed  int `json:"rsn_freed,omitempty"`
}

// DmsgPorterStats returns the current ephemeral port reservation
// counts for the main and embedded RSN DMSG clients.
func (v *Visor) DmsgPorterStats() (*DmsgPorterStatus, error) {
	s := &DmsgPorterStatus{}
	if v.dmsgC != nil {
		s.MainPorts = v.dmsgC.PorterCount()
	}
	if v.embeddedRouteSetup != nil && v.embeddedRouteSetup.DmsgClient() != nil {
		s.RSNPorts = v.embeddedRouteSetup.DmsgClient().PorterCount()
	}
	return s, nil
}

// DmsgPorterReset frees all ephemeral port reservations on the main
// and embedded RSN DMSG clients. Returns the number of ports freed.
func (v *Visor) DmsgPorterReset() (*DmsgPorterStatus, error) {
	s := &DmsgPorterStatus{}
	if v.dmsgC != nil {
		s.MainFreed = v.dmsgC.ResetPorter()
		s.MainPorts = v.dmsgC.PorterCount()
	}
	if v.embeddedRouteSetup != nil && v.embeddedRouteSetup.DmsgClient() != nil {
		s.RSNFreed = v.embeddedRouteSetup.DmsgClient().ResetPorter()
		s.RSNPorts = v.embeddedRouteSetup.DmsgClient().PorterCount()
	}
	return s, nil
}

// DmsgSetMinSessions updates the minimum DMSG session count at runtime.
func (v *Visor) DmsgSetMinSessions(n int) error {
	if v.dmsgC == nil {
		return fmt.Errorf("DMSG client not running")
	}
	v.dmsgC.SetMinSessions(n)
	return nil
}

// DmsgReconnect forces all DMSG sessions to close and reconnect.
func (v *Visor) DmsgReconnect() (int, error) {
	if v.dmsgC == nil {
		return 0, fmt.Errorf("DMSG client not running")
	}
	return v.dmsgC.ForceReconnect(), nil
}

// DHTSync fetches items from a DHT full node and stores them locally.
// If remotePK is empty, uses the first available bootstrap peer.
func (v *Visor) DHTSync(remotePK string, salt string) (int, error) {
	if v.dhtNode == nil {
		return 0, fmt.Errorf("DHT node not running")
	}

	var targetPK cipher.PubKey
	if remotePK != "" {
		if err := targetPK.Set(remotePK); err != nil {
			return 0, fmt.Errorf("invalid PK: %w", err)
		}
	} else {
		// Use first available bootstrap peer.
		fullNodes := dht.FindFullNodes(context.Background(), v.dhtNode)
		if len(fullNodes) == 0 {
			return 0, fmt.Errorf("no full nodes available")
		}
		targetPK = fullNodes[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := v.dhtNode.GetItemsFrom(ctx, targetPK, salt, 0, 0)
	if err != nil {
		return 0, fmt.Errorf("sync from %s: %w", targetPK.String()[:8], err)
	}

	stored := 0
	for _, item := range resp.Items {
		if putErr := v.dhtNode.Store().Put(item); putErr == nil {
			stored++
		}
	}

	return stored, nil
}

// DmsgPorterDiag returns detailed diagnostic information about ephemeral
// port reservations in the embedded RSN's DMSG client porter.
func (v *Visor) DmsgPorterDiag() (*netutil.EphemeralDiagResult, error) {
	if v.embeddedRouteSetup == nil || v.embeddedRouteSetup.DmsgClient() == nil {
		return nil, fmt.Errorf("embedded RSN not running")
	}
	diag := v.embeddedRouteSetup.DmsgClient().PorterDiag()
	return &diag, nil
}
