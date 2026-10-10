package cliconfig

import (
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
	svcblock "github.com/skycoin/skywire/pkg/services"
)

func TestDeploymentBlocks(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	blocks, err := deploymentBlocks("203.0.113.7:18080", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[cipher.PubKey]string{}
	for _, b := range blocks {
		_, bpk, hasKey, err := svcblock.OwnKey(b.Raw)
		if err != nil {
			t.Fatalf("%s: %v", b.Type, err)
		}
		if !hasKey {
			t.Errorf("%s: no key of its own", b.Type)
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
		if want := "dmsg://" + keyOfType(t, blocks, typ).Hex() + ":80"; got != want {
			t.Errorf("%s at %q, want %q", typ, got, want)
		}
	}
	if len(svc.DmsgServers) != 1 || svc.DmsgServers[0].Server.Address != "203.0.113.7:18080" {
		t.Errorf("dmsg servers %+v", svc.DmsgServers)
	}
	if len(svc.RouteSetupNodes) != 1 || len(svc.TransportSetupPKs) != 1 {
		t.Errorf("setup nodes %v, transport setup %v", svc.RouteSetupNodes, svc.TransportSetupPKs)
	}

	// A regenerate keeps every block, and so every key.
	again, err := deploymentBlocks("203.0.113.7:18080", "", blocks)
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
	blocks, err := deploymentBlocks("203.0.113.7", "127.0.0.1:6379", nil)
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

func keyOfType(t *testing.T, blocks []svcblock.Block, typ string) cipher.PubKey {
	t.Helper()
	for _, b := range blocks {
		if b.Type == typ {
			_, pk, _, err := svcblock.OwnKey(b.Raw)
			if err != nil {
				t.Fatal(err)
			}
			return pk
		}
	}
	t.Fatalf("no %s block", typ)
	return cipher.PubKey{}
}
