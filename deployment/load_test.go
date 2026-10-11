package deployment

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFile(t *testing.T) {
	prod, test, prodConf, testConf, raw := Prod, Test, ProdConf, TestConf, ServicesJSON
	t.Cleanup(func() { Prod, Test, ProdConf, TestConf, ServicesJSON = prod, test, prodConf, testConf, raw })

	const pk = "02a2d4c346dabd165fd555dfdba4a7f4d18786fe7e055e562397cd5102bdd7f8dd"
	conf := `{"prod":{"dmsg_discovery_dmsg":"dmsg://` + pk + `:80",
		"dmsg_servers":[{"static":"` + pk + `","server":{"address":"203.0.113.7:8080","address_ws":"wss://x.example.net/dmsg"}}]},
		"test":{}}`
	path := filepath.Join(t.TempDir(), "services-config.json")
	if err := os.WriteFile(path, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := LoadFile(path); err != nil {
		t.Fatal(err)
	}
	if Prod.DmsgDiscoveryDmsg != "dmsg://"+pk+":80" || len(Prod.DmsgServers) != 1 ||
		Prod.DmsgServers[0].Server.AddressWS != "wss://x.example.net/dmsg" {
		t.Fatalf("prod not replaced: %+v", Prod)
	}
	if Prod.TransportDiscoveryDmsg != "" || Prod.BrowseOriginSuffix != "" {
		t.Errorf("fields from the old deployment survived: %+v", Prod)
	}
	if Prod.DmsgDiscovery == "" {
		t.Error("HTTP-named dmsg discovery not backfilled")
	}
	if err := LoadFile(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("missing file: want an error")
	}
}

func TestWSSAliasHosts(t *testing.T) {
	s := Services{WSSDomainAliases: []string{"theskywirenetwork.net", ".old.example", ""}}
	got := s.WSSAliasHosts("abc")
	if len(got) != 2 || got[0] != "abc.theskywirenetwork.net" || got[1] != "abc.old.example" {
		t.Fatalf("WSSAliasHosts = %v", got)
	}
	if hosts := (&Services{}).WSSAliasHosts("abc"); hosts != nil {
		t.Errorf("no aliases: %v", hosts)
	}
}
