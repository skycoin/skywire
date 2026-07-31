// Package skyenv pkg/skyenv/ports_test.go
//
// Guards the one invariant the port constants in skyenv.go have to
// hold and cannot express in the type system: every number is used
// once.
package skyenv

import "testing"

// Every visor port lives in ONE namespace as far as collisions go.
//
// The dmsg/skynet split in the naming is a lie for this purpose:
// goServeSkynetMirror binds each mirrored dmsg service on the SKYNET
// side at the same number, through the same appnet porter the app
// ports use. So a "Dmsg…Port" that happens to equal a "Sky…Port" is
// two listeners fighting over one porter entry, and the loser's error
// handling decides how bad it gets — sky_forward_conn's is a fatal
// module-init failure, i.e. the visor does not boot.
//
// That has now happened twice (44 = VPNServerPort, then 57 =
// SkyForwardingServerPort), both times found only when a visor died
// in CI. Hence this test rather than a comment.
func TestVisorPortsAreUnique(t *testing.T) {
	ports := map[string]uint16{
		"DmsgCtrlPort":                    DmsgCtrlPort,
		"DmsgPingPort":                    DmsgPingPort,
		"DmsgPtyPort":                     DmsgPtyPort,
		"DmsgSetupPort":                   DmsgSetupPort,
		"DmsgVisorRPCPort":                DmsgVisorRPCPort,
		"DmsgHypervisorPort":              DmsgHypervisorPort,
		"DmsgTransportSetupPort":          DmsgTransportSetupPort,
		"DmsgTransportSetupServicePort":   DmsgTransportSetupServicePort,
		"DmsgGRPCPort":                    DmsgGRPCPort,
		"DmsgCXOPort":                     DmsgCXOPort,
		"DmsgTPDMetricsCXOPort":           DmsgTPDMetricsCXOPort,
		"DmsgTPDUptimeCXOPort":            DmsgTPDUptimeCXOPort,
		"DmsgSDServicesCXOPort":           DmsgSDServicesCXOPort,
		"DmsgDMSGDClientsByServerCXOPort": DmsgDMSGDClientsByServerCXOPort,
		"DmsgTPDAllTransportsCXOPort":     DmsgTPDAllTransportsCXOPort,
		"DmsgWebRTCSignalPort":            DmsgWebRTCSignalPort,
		"SkychatVoiceSignalPort":          SkychatVoiceSignalPort,
		"SkychatVoiceMediaPort":           SkychatVoiceMediaPort,
		"SkychatFilePort":                 SkychatFilePort,
		"DmsgDHTPort":                     DmsgDHTPort,
		"DmsgAwaitSetupPort":              DmsgAwaitSetupPort,
		"TransportPort":                   TransportPort,
		"SkychatPort":                     SkychatPort,
		"SkysocksPort":                    SkysocksPort,
		"SkysocksClientPort":              SkysocksClientPort,
		"VPNServerPort":                   VPNServerPort,
		"VPNClientPort":                   VPNClientPort,
		"VPNRouterPort":                   VPNRouterPort,
		"ExampleServerPort":               ExampleServerPort,
		"SkyForwardingServerPort":         SkyForwardingServerPort,
		"SkyForwardingMuxPort":            SkyForwardingMuxPort,
		"SkyPingPort":                     SkyPingPort,
		"SkycoinDaemonPort":               SkycoinDaemonPort,
		"SkycoinWebPort":                  SkycoinWebPort,
	}

	// The one deliberate sharer. DmsgTPDAllTransportsCXOPort is a
	// port visors DIAL (the TPD's all-transports CXO feed) and only
	// the transport-discovery service ever binds; ExampleServerPort
	// is bound by a demo app on the visor. Two different processes,
	// so the number can be reused without either one losing a bind.
	allowed := map[string]bool{
		"DmsgTPDAllTransportsCXOPort+ExampleServerPort": true,
	}

	seen := make(map[uint16]string, len(ports))
	for _, name := range sortedKeys(ports) {
		port := ports[name]
		if prev, dup := seen[port]; dup {
			if allowed[prev+"+"+name] {
				continue
			}
			t.Errorf("port %d is claimed by both %s and %s — the skynet mirror binds "+
				"dmsg and app ports through the same appnet porter, so one of the two "+
				"listeners will fail to bind (sky_forward_conn's failure is fatal)", port, prev, name)
			continue
		}
		seen[port] = name
	}
}

// sortedKeys keeps the failure message deterministic across runs —
// map iteration order would otherwise decide which of a colliding pair
// is reported as "prev".
func sortedKeys(m map[string]uint16) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
