package cliconfig

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgc"
	svcblock "github.com/skycoin/skywire/pkg/services"
)

func TestDeploymentBlocks(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blocks, err := deploymentBlocks("203.0.113.7:18080", "", pk, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[cipher.PubKey]string{}
	for _, b := range blocks {
		if b.Type == "dmsg-server" {
			t.Fatalf("a new deployment folds its dmsg server into the visor, got a block: %s", b.Raw)
		}
		_, bpk, hasKey, err := svcblock.OwnKey(b.Raw)
		if err != nil {
			t.Fatalf("%s: %v", b.Type, err)
		}
		own := false
		for _, typ := range ownKeyTypes {
			own = own || typ == b.Type
		}
		if hasKey != own {
			t.Errorf("%s: own key %v, want %v", b.Type, hasKey, own)
		}
		// Every service with a dmsg config reaches the deployment through the
		// visor's own server.
		if (b.Type == "dmsg-discovery" || b.Type == "setup-node" || b.Type == "transport-setup") &&
			!strings.Contains(string(b.Raw), `"static":"`+pk.Hex()+`"`) {
			t.Errorf("%s: not pointed at the visor's dmsg server: %s", b.Type, b.Raw)
		}
		if !hasKey {
			continue
		}
		if bpk == pk {
			t.Errorf("%s has the visor's key", b.Type)
		}
		if other, dup := seen[bpk]; dup {
			t.Errorf("%s and %s share a key", b.Type, other)
		}
		seen[bpk] = b.Type
		if !strings.Contains(string(b.Raw), `"testing":true`) && (b.Type == "transport-discovery" || b.Type == "dmsg-discovery") {
			t.Errorf("%s: no redis given, want an in-memory store: %s", b.Type, b.Raw)
		}
	}
	if len(blocks) != 7 {
		t.Fatalf("got %d blocks, want 7", len(blocks))
	}

	srv, port, err := deploymentDmsgServer("203.0.113.7:18080", "", blocks)
	if err != nil {
		t.Fatal(err)
	}
	if srv == nil || !srv.Enabled || srv.PublicAddress != "203.0.113.7:18080" || port != 18080 || srv.LocalAddress != "" {
		t.Fatalf("visor dmsg server %+v on port %d", srv, port)
	}
	svc, err := deploymentServices(pk, blocks, srv)
	if err != nil {
		t.Fatal(err)
	}
	for typ, got := range map[string]string{"transport-discovery": svc.TransportDiscoveryDmsg, "address-resolver": svc.AddressResolverDmsg,
		"route-finder": svc.RouteFinderDmsg, "service-discovery": svc.ServiceDiscoveryDmsg} {
		if want := "dmsg://" + pk.Hex() + ":80" + deploymentPrefix(t, blocks, typ); got != want {
			t.Errorf("%s at %q, want %q", typ, got, want)
		}
	}
	if len(svc.DmsgServers) != 1 || svc.DmsgServers[0].Static != pk.Hex() || svc.DmsgServers[0].Server.Address != "203.0.113.7:18080" {
		t.Errorf("dmsg servers %+v, want the visor's key at 203.0.113.7:18080", svc.DmsgServers)
	}
	if svc.BrowseOriginSuffix == "" || svc.BrowseOriginSuffix != deployment.Prod.BrowseOriginSuffix {
		t.Errorf("browse origin %q, want prod's %q", svc.BrowseOriginSuffix, deployment.Prod.BrowseOriginSuffix)
	}
	if len(svc.RouteSetupNodes) != 1 || len(svc.TransportSetupPKs) != 1 {
		t.Errorf("setup nodes %v, transport setup %v", svc.RouteSetupNodes, svc.TransportSetupPKs)
	}
	if _, err := deploymentServices(pk, blocks, nil); err == nil {
		t.Error("export with no dmsg server at all: want an error")
	}

	// A regenerate keeps every block, and so every key, and adds no dmsg-server.
	again, err := deploymentBlocks("203.0.113.7:18080", "", pk, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != len(blocks) {
		t.Fatalf("regen: %d blocks, want %d", len(again), len(blocks))
	}
	for i := range blocks {
		if string(again[i].Raw) != string(blocks[i].Raw) {
			t.Errorf("regen changed %s", blocks[i].Type)
		}
	}
}

// A deployment generated before the dmsg server moved onto the visor key keeps
// its dmsg-server block, its key and its address on a regenerate.
func TestDeploymentBlocksOldDmsgServerBlock(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	spk, ssk := cipher.GenerateKeyPair()
	var old svcblock.Block
	if err := old.UnmarshalJSON([]byte(`{"type":"dmsg-server","name":"dmsgs","public_key":"` + spk.Hex() + `","secret_key":"` + ssk.Hex() +
		`","public_address":"203.0.113.7:18080","local_address":":18080","wss_domain_suffix":"dmsg.example.net"}`)); err != nil {
		t.Fatal(err)
	}
	blocks, err := deploymentBlocks("203.0.113.7:18080", "", pk, []svcblock.Block{old})
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 8 || string(blocks[0].Raw) != string(old.Raw) {
		t.Fatalf("got %d blocks, want the old dmsg-server block kept first and 7 more", len(blocks))
	}
	for _, b := range blocks {
		if (b.Type == "dmsg-discovery" || b.Type == "setup-node" || b.Type == "transport-setup") &&
			!strings.Contains(string(b.Raw), `"static":"`+spk.Hex()+`"`) {
			t.Errorf("%s: not pointed at the dmsg-server block's key: %s", b.Type, b.Raw)
		}
	}
	srv, _, err := deploymentDmsgServer("203.0.113.7:18080", "other.example.net", blocks)
	if err != nil || srv != nil {
		t.Fatalf("old layout: want no visor dmsg server, got %+v %v", srv, err)
	}
	svc, err := deploymentServices(pk, blocks, srv)
	if err != nil {
		t.Fatal(err)
	}
	e := svc.DmsgServers[0]
	if e.Static != spk.Hex() || e.Server.Address != "203.0.113.7:18080" ||
		e.Server.AddressWS != "wss://"+spk.DNSLabel()+".dmsg.example.net/dmsg" || svc.WSSDomainSuffix != "dmsg.example.net" {
		t.Errorf("old layout export %+v, suffix %q", e, svc.WSSDomainSuffix)
	}
}

func TestDeploymentBlocksRedis(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blocks, err := deploymentBlocks("203.0.113.7", "127.0.0.1:6379", pk, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"dmsg-discovery":      "redis://127.0.0.1:6379/0",
		"transport-discovery": "redis://127.0.0.1:6379/1",
		"route-finder":        "redis://127.0.0.1:6379/1",
		"service-discovery":   "redis://127.0.0.1:6379/2",
		"address-resolver":    "redis://127.0.0.1:6379/3",
	}
	for _, b := range blocks {
		if w, ok := want[b.Type]; ok && !strings.Contains(string(b.Raw), `"redis":"`+w+`"`) {
			t.Errorf("%s: want redis %s in %s", b.Type, w, b.Raw)
		}
	}
}

func TestRedisDB(t *testing.T) {
	for in, want := range map[string]string{
		"":                             "redis://localhost:6379/2",
		"redis://10.0.0.1:6379/":       "redis://10.0.0.1:6379/2",
		"/run/redis/redis.sock":        "unix:///run/redis/redis.sock?db=2",
		"unix:///run/redis/redis.sock": "unix:///run/redis/redis.sock?db=2",
	} {
		if got := redisDB(in, 2); got != want {
			t.Errorf("redisDB(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDeploymentAddr(t *testing.T) {
	if h, p, err := deploymentAddr("203.0.113.7"); err != nil || h != "203.0.113.7" || p != deployDmsgPort {
		t.Errorf("bare host: %q %d %v", h, p, err)
	}
	if h, p, err := deploymentAddr("example.net:18080"); err != nil || h != "example.net" || p != 18080 {
		t.Errorf("host:port: %q %d %v", h, p, err)
	}
	for _, bad := range []string{"", "203.0.113.7:x", "203.0.113.7:65530"} {
		if _, _, err := deploymentAddr(bad); err == nil {
			t.Errorf("%q: want an error", bad)
		}
	}
}

func deploymentPrefix(t *testing.T, blocks []svcblock.Block, typ string) string {
	t.Helper()
	for _, b := range blocks {
		if b.Type == typ {
			return b.Prefix()
		}
	}
	t.Fatalf("no %s block", typ)
	return ""
}

// A setup node added beside a transport discovery under its own key, as on
// prod, is pointed at that key, not at the visor's.
func TestDeploymentBlocksOwnKeyTPD(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	tpk, tsk := cipher.GenerateKeyPair()
	var old svcblock.Block
	if err := old.UnmarshalJSON([]byte(`{"type":"transport-discovery","name":"tpd","secret_key":"` + tsk.Hex() + `"}`)); err != nil {
		t.Fatal(err)
	}
	blocks, err := deploymentBlocks("203.0.113.7", "", pk, []svcblock.Block{old})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range blocks {
		if b.Type == "setup-node" && !strings.Contains(string(b.Raw), `"transport_discovery_dmsg":"dmsg://`+tpk.Hex()+`:80"`) {
			t.Errorf("setup node not pointed at the own-key TPD: %s", b.Raw)
		}
	}
	svc, err := deploymentServices(pk, blocks, &dmsgc.DmsgServerConfig{Enabled: true, PublicAddress: "203.0.113.7:8080"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "dmsg://" + tpk.Hex() + ":80"; svc.TransportDiscoveryDmsg != want {
		t.Errorf("tpd at %q, want %q", svc.TransportDiscoveryDmsg, want)
	}
}

// The visor's dmsg server given a wss suffix is exported with the wss address
// a browser visor bootstraps from, and without one with none.
func TestDeploymentServicesWSS(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blocks, err := deploymentBlocks("203.0.113.7", "", pk, nil)
	if err != nil {
		t.Fatal(err)
	}
	for suffix, want := range map[string]string{"": "", ".example.net": "example.net", "example.net": "example.net"} {
		srv, _, err := deploymentDmsgServer("203.0.113.7", suffix, blocks)
		if err != nil {
			t.Fatal(err)
		}
		svc, err := deploymentServices(pk, blocks, srv)
		if err != nil {
			t.Fatal(err)
		}
		e := svc.DmsgServers[0]
		wantWS := ""
		if want != "" {
			wantWS = "wss://" + pk.DNSLabel() + "." + want + "/dmsg"
		}
		if e.Server.AddressWS != wantWS || svc.WSSDomainSuffix != want || srv.WSSDomainSuffix != want {
			t.Errorf("suffix %q: address_ws %q, wss_domain_suffix %q, server %q; want %q and %q",
				suffix, e.Server.AddressWS, svc.WSSDomainSuffix, srv.WSSDomainSuffix, wantWS, want)
		}
		if e.Server.Address != "203.0.113.7:8080" {
			t.Errorf("address %q", e.Server.Address)
		}
	}
}

func TestStatusAddr(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.7": "127.0.0.1:8082", "203.0.113.7:18080": "127.0.0.1:18082", "203.0.113.7:x": ""} {
		if got := statusAddr(in); got != want {
			t.Errorf("statusAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
