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
		{PK: hv, Hypervisor: hv, Local: true, Online: true, Apps: []mapApp{
			{Name: "skysocks-client", Port: 13, Status: "running", Listen: "127.0.0.1:1080", Whitelist: "N/A"},
			{Name: "skysocks", Port: 3, Status: "stopped", Whitelist: "2"},
		}},
		{PK: srv, Hypervisor: hv, Online: true, Hostname: "prod1", Version: "v1.3.100-129-g430687a40-430687a40cdb", DmsgServer: true,
			DmsgServerPK: srv, DmsgServerAddr: "[::]:30086", PublicIP: "139.162.160.227", Role: "dmsg-server",
			Services: []mapService{
				{Name: "transport-discovery", Type: "transport-discovery", PK: tpd.Hex(), OwnKey: true, State: "running", Listen: ":9094"},
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
	require.Equal(t, "hypervisor ──"+hv.Hex(), strings.TrimSpace(lines[0]))
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
	line("hostname", "│", "visor key", "roles")
	line("prod1 ──┼── "+srv.Hex(), "dmsg server")
	line("│", "139.162.160.227  v1.3.100-129-g430687a40")
	line("services", "name", "listens", "services-config.json")
	line(srv.Hex()+" (visor key)", "dmsg-server", ":30086", "listed")
	line(tpd.Hex(), "transport-discovery", ":9094", "listed")
	line("apps", "skynet port", "listens", "whitelist")
	line("forwarded ports", "to", "over", "label")
	line(":80", "landing page", "skynet dmsg")
	line("vhost *", "127.0.0.1:80")
	line("skysocks-client", "13", "127.0.0.1:1080", "N/A")
	line("skysocks (stopped)", "3", "2")
	require.NotContains(t, out, " running")
	line(leaf.Hex() + "  sbc")
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
	for i, l := range lines {
		if strings.Contains(l, srv.Hex()+" ") && strings.Contains(l, "prod1") {
			require.Contains(t, lines[i-1], "v1.3.100-129-g430687a40")
		}
		if strings.Contains(l, leaf.Hex()+"  sbc") {
			require.Contains(t, lines[i-1], "offline")
		}
	}
	require.Contains(t, out, "offline")
}

func TestHVMapOnly(t *testing.T) {
	hv, _ := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	tree := visorapi.HVVisorTree{Sections: []visorapi.HVVisorTreeNode{
		{HypervisorPK: hv, Visors: []visorapi.HVVisorEntry{{PK: hv, IsLocal: true}, {PK: a}, {PK: b}}},
	}}
	m := collectHVMap(nil, tree, map[cipher.PubKey]bool{a: true})
	require.Equal(t, hv, m.Hypervisor)
	require.Len(t, m.Visors, 1)
	require.Equal(t, a, m.Visors[0].PK)
	require.Len(t, collectHVMap(nil, tree, nil).Visors, 3)
}

func TestAppListenAndWhitelist(t *testing.T) {
	require.Equal(t, ":8051", appListen("skydex-client", []string{"--addr", ":8051", "--market-port", "8050"}))
	require.Equal(t, "127.0.0.1:8002", appListen("skycoin-web", []string{"--host", "127.0.0.1", "--port", "8002"}))
	require.Equal(t, ":1080", appListen("skysocks-client", []string{"--addr=:1080"}))
	require.Equal(t, "127.0.0.1:1080", appListen("skysocks-client", nil))
	require.Equal(t, "", appListen("skychat", []string{"--portless", "--pair-enable"}))
	require.Equal(t, "", appListen("vpn-client", []string{"--dns", "1.1.1.1"}))

	k1, _ := cipher.GenerateKeyPair()
	k2, _ := cipher.GenerateKeyPair()
	require.Equal(t, "2", appWhitelist("skysocks", []string{"--whitelist", k1.Hex() + "," + k2.Hex()}))
	require.Equal(t, "open", appWhitelist("vpn-server", nil))
	require.Equal(t, "N/A", appWhitelist("skychat", nil))
	require.Equal(t, ":30086", shortAddr("[::]:30086"))
}
