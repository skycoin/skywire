// Package clivisor cmd/skywire-cli/commands/visor/hv_map_test.go
package clivisor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func TestHVMap(t *testing.T) {
	hv, _ := cipher.GenerateKeyPair()
	srv, _ := cipher.GenerateKeyPair()
	sub, _ := cipher.GenerateKeyPair()
	leaf, _ := cipher.GenerateKeyPair()
	tpd, _ := cipher.GenerateKeyPair()
	ts, _ := cipher.GenerateKeyPair()
	idle, _ := cipher.GenerateKeyPair()

	var s deployment.Services
	s.TransportDiscoveryDmsg = "dmsg://" + tpd.Hex() + ":80"
	s.TransportSetupPKs = []cipher.PubKey{ts, idle}
	s.DmsgServers = []deployment.DmsgServerEntry{{Static: srv.Hex()}}
	roles := deploymentRoles(s)
	require.Equal(t, "transport-discovery", roles[tpd])
	require.Equal(t, "transport-setup", roles[idle])
	require.Equal(t, "dmsg-server", roles[srv])

	m := hvMap{Hypervisor: hv, Visors: []mapVisor{
		{PK: hv, Hypervisor: hv, Local: true, Online: true, Apps: []mapApp{{Name: "skysocks-client", Port: 13, Status: "running"}}},
		{PK: srv, Hypervisor: hv, Online: true, Hostname: "prod1", Version: "v1.3.100-129-g430687a40-430687a40cdb", DmsgServer: true,
			Services: []mapService{
				{Name: "transport-discovery", Type: "transport-discovery", PK: tpd.Hex(), OwnKey: true, State: "running"},
				{Name: "transport-setup-1", Type: "transport-setup", PK: ts.Hex(), OwnKey: true, State: "running"},
			},
			Forwards: []visorapi.ForwardedPort{{Port: 80, Skynet: true, DMSG: true, Vhosts: []visorapi.Vhost{{Name: "*", ProxyAddr: "127.0.0.1:80"}}}}},
		{PK: sub, Hypervisor: hv, Online: true, Hostname: "home"},
		{PK: leaf, Hypervisor: sub, Online: false, Hostname: "sbc"},
	}}
	m.applyRoles(roles)
	require.Equal(t, []mapKey{{PK: idle, Role: "transport-setup"}}, m.Unplaced)

	out := renderHVMap(m)
	lines := strings.Split(out, "\n")
	require.Equal(t, hv.Hex(), strings.TrimSpace(lines[0]))
	line := func(parts ...string) {
		t.Helper()
		for _, l := range lines {
			ok := true
			for _, p := range parts {
				ok = ok && strings.Contains(l, p)
			}
			if ok {
				return
			}
		}
		t.Fatalf("no line has %q in\n%s", parts, out)
	}
	line("prod1 ──┼── "+srv.Hex(), "v1.3.100-129-g430687a40 ", "dmsg server")
	line(tpd.Hex(), "transport-discovery", "running", "published")
	line(":80", "landing page", "skynet dmsg")
	line("vhost *", "127.0.0.1:80")
	line("skysocks-client", "running", "port 13")
	line(leaf.Hex()+"  sbc", "offline")
	line(idle.Hex(), "transport-setup")
	require.NotContains(t, out, "430687a40cdb")
	// A visor of a sub-hypervisor hangs under that hypervisor, one level down.
	var subCol, leafCol int
	for _, l := range lines {
		if i := strings.Index(l, sub.Hex()); i >= 0 {
			subCol = i
		}
		if i := strings.Index(l, leaf.Hex()); i >= 0 {
			leafCol = i
		}
	}
	require.Greater(t, leafCol, subCol)
	require.Contains(t, out, "offline")
}
