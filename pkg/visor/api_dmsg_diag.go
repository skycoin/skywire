package visor

import (
	"fmt"

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

// DmsgPorterDiag returns detailed diagnostic information about ephemeral
// port reservations in the embedded RSN's DMSG client porter.
func (v *Visor) DmsgPorterDiag() (*netutil.EphemeralDiagResult, error) {
	if v.embeddedRouteSetup == nil || v.embeddedRouteSetup.DmsgClient() == nil {
		return nil, fmt.Errorf("embedded RSN not running")
	}
	diag := v.embeddedRouteSetup.DmsgClient().PorterDiag()
	return &diag, nil
}
