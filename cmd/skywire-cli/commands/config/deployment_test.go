package cliconfig

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
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
		if !hasKey {
			continue
		}
		if other, dup := seen[bpk]; dup {
			t.Errorf("%s and %s share a key", b.Type, other)
		}
		seen[bpk] = b.Type
		if !strings.Contains(string(b.Raw), `"testing":true`) && (b.Type == "transport-discovery" || b.Type == "dmsg-discovery") {
			t.Errorf("%s: no redis given, want an in-memory store: %s", b.Type, b.Raw)
		}
	}
	if len(blocks) != 8 {
		t.Fatalf("got %d blocks, want 8", len(blocks))
	}

	svc, err := deploymentServices(pk, blocks)
	if err != nil {
		t.Fatal(err)
	}
	for typ, got := range map[string]string{"transport-discovery": svc.TransportDiscoveryDmsg, "address-resolver": svc.AddressResolverDmsg,
		"route-finder": svc.RouteFinderDmsg, "service-discovery": svc.ServiceDiscoveryDmsg} {
		if want := "dmsg://" + pk.Hex() + ":80" + deploymentPrefix(t, blocks, typ); got != want {
			t.Errorf("%s at %q, want %q", typ, got, want)
		}
	}
	if len(svc.DmsgServers) != 1 || svc.DmsgServers[0].Server.Address != "203.0.113.7:18080" {
		t.Errorf("dmsg servers %+v", svc.DmsgServers)
	}
	if svc.BrowseOriginSuffix == "" || svc.BrowseOriginSuffix != deployment.Prod.BrowseOriginSuffix {
		t.Errorf("browse origin %q, want prod's %q", svc.BrowseOriginSuffix, deployment.Prod.BrowseOriginSuffix)
	}
	if len(svc.RouteSetupNodes) != 1 || len(svc.TransportSetupPKs) != 1 {
		t.Errorf("setup nodes %v, transport setup %v", svc.RouteSetupNodes, svc.TransportSetupPKs)
	}

	// A regenerate keeps every block, and so every key.
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
	svc, err := deploymentServices(pk, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if want := "dmsg://" + tpk.Hex() + ":80"; svc.TransportDiscoveryDmsg != want {
		t.Errorf("tpd at %q, want %q", svc.TransportDiscoveryDmsg, want)
	}
}

// A dmsg server given a wss_domain_suffix is exported with the wss address a
// browser visor bootstraps from.
func TestDeploymentServicesWSS(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blocks, err := deploymentBlocks("203.0.113.7", "", pk, nil)
	if err != nil {
		t.Fatal(err)
	}
	var srvPK cipher.PubKey
	for i, b := range blocks {
		if b.Type != "dmsg-server" {
			continue
		}
		_, srvPK, _, _ = svcblock.OwnKey(b.Raw) //nolint:errcheck
		raw := strings.Replace(string(b.Raw), "{", `{"wss_domain_suffix":".example.net",`, 1)
		if err := blocks[i].UnmarshalJSON([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	svc, err := deploymentServices(pk, blocks)
	if err != nil {
		t.Fatal(err)
	}
	if want := "wss://" + srvPK.DNSLabel() + ".example.net/dmsg"; svc.DmsgServers[0].Server.AddressWS != want {
		t.Errorf("address_ws %q, want %q", svc.DmsgServers[0].Server.AddressWS, want)
	}
	if svc.WSSDomainSuffix != "example.net" {
		t.Errorf("wss_domain_suffix %q", svc.WSSDomainSuffix)
	}
}

func TestStatusAddr(t *testing.T) {
	for in, want := range map[string]string{"203.0.113.7": "127.0.0.1:8082", "203.0.113.7:18080": "127.0.0.1:18082", "203.0.113.7:x": ""} {
		if got := statusAddr(in); got != want {
			t.Errorf("statusAddr(%q) = %q, want %q", in, got, want)
		}
	}
}
