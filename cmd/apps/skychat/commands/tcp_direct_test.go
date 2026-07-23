// Package commands cmd/apps/skychat/commands/tcp_direct_test.go
//
// Unit coverage for the noise-TCP entry point's pure parsing +
// identity-resolution helpers: peer-spec parsing, whitelist-set
// construction, reading the SK from a config file, the flag/env/config
// identity precedence chain, and the appnet.Addr shim.
package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

func TestParseTCPPeerSpec(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	good := "tcp://" + pk.Hex() + "@1.2.3.4:8800"
	gotPK, gotAddr, err := parseTCPPeerSpec(good)
	if err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	if gotPK != pk || gotAddr != "1.2.3.4:8800" {
		t.Errorf("parsed pk/addr = %s / %q, want %s / 1.2.3.4:8800", gotPK.Hex(), gotAddr, pk.Hex())
	}

	bad := []string{
		"",
		"http://" + pk.Hex() + "@1.2.3.4:8800", // wrong scheme
		"tcp://" + pk.Hex(),                     // missing @
		"tcp://@1.2.3.4:8800",                   // empty pk (at<=0)
		"tcp://" + pk.Hex() + "@",               // trailing @ (at==len-1)
		"tcp://zzzz@1.2.3.4:8800",               // bad pk hex
		"tcp://" + pk.Hex() + "@not-host-port",  // bad host:port
	}
	for _, b := range bad {
		if _, _, err := parseTCPPeerSpec(b); err == nil {
			t.Errorf("spec %q should error", b)
		}
	}
}

func TestTCPWhitelistSet(t *testing.T) {
	// tcpWhitelistSet logs skipped invalid entries through appLog, which
	// is nil until RunSkychat wires it up.
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	if got := tcpWhitelistSet(""); len(got) != 0 {
		t.Errorf("empty string -> %d entries, want 0", len(got))
	}
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	// Whitespace-tolerant, invalid entries skipped.
	set := tcpWhitelistSet("  " + pk1.Hex() + " , zzz-invalid , " + pk2.Hex() + "  ")
	if len(set) != 2 {
		t.Fatalf("want 2 valid entries (invalid skipped), got %d", len(set))
	}
	if _, ok := set[pk1]; !ok {
		t.Error("pk1 missing from whitelist set")
	}
	if _, ok := set[pk2]; !ok {
		t.Error("pk2 missing from whitelist set")
	}
}

func TestReadSKFromConfig(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	dir := t.TempDir()

	good := filepath.Join(dir, "skywire.json")
	if err := os.WriteFile(good, []byte(`{"sk":"`+sk.Hex()+`","pk":"ignored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	gotSK, err := readSKFromConfig(good)
	if err != nil {
		t.Fatalf("valid config: %v", err)
	}
	if gotSK != sk {
		t.Error("sk mismatch on valid config")
	}

	cases := map[string]string{
		"bad.json":  "{not json",
		"nosk.json": `{"pk":"abc"}`,
		"badsk.json": `{"sk":"zzzz"}`,
	}
	for name, body := range cases {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSKFromConfig(p); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
	// Missing file.
	if _, err := readSKFromConfig(filepath.Join(dir, "nope.json")); err == nil {
		t.Error("missing file should error")
	}
}

func TestResolveTCPIdentity(t *testing.T) {
	// resolveTCPIdentity reads these package globals; save/restore them.
	origSK, origCfg := tcpSKFlag, tcpConfigPath
	t.Cleanup(func() { tcpSKFlag, tcpConfigPath = origSK, origCfg })

	_, sk := cipher.GenerateKeyPair()
	wantPK, err := sk.PubKey()
	if err != nil {
		t.Fatal(err)
	}

	// 1. --sk flag wins.
	tcpSKFlag = sk.Hex()
	tcpConfigPath = ""
	t.Setenv("DMSGCURL_SK", "")
	pk, gotSK, source, err := resolveTCPIdentity()
	if err != nil || source != "flag" || pk != wantPK || gotSK != sk {
		t.Fatalf("flag path: pk=%s source=%q err=%v", pk.Hex(), source, err)
	}

	// 2. Env used when the flag is empty.
	tcpSKFlag = ""
	t.Setenv("DMSGCURL_SK", sk.Hex())
	pk, _, source, err = resolveTCPIdentity()
	if err != nil || source != "env" || pk != wantPK {
		t.Fatalf("env path: pk=%s source=%q err=%v", pk.Hex(), source, err)
	}

	// 3. Config file used when flag+env are empty.
	tcpSKFlag = ""
	t.Setenv("DMSGCURL_SK", "")
	cfg := filepath.Join(t.TempDir(), "skywire.json")
	if err := os.WriteFile(cfg, []byte(`{"sk":"`+sk.Hex()+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	tcpConfigPath = cfg
	pk, _, source, err = resolveTCPIdentity()
	if err != nil || source != "config" || pk != wantPK {
		t.Fatalf("config path: pk=%s source=%q err=%v", pk.Hex(), source, err)
	}

	// 4. Nothing configured -> loud error.
	tcpSKFlag = ""
	tcpConfigPath = ""
	t.Setenv("DMSGCURL_SK", "")
	if _, _, _, err := resolveTCPIdentity(); err == nil {
		t.Fatal("no identity configured should error")
	}
}

func TestTCPDirectConn_RemoteAddr(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	c := &tcpDirectConn{rPK: pk}
	addr, ok := c.RemoteAddr().(appnet.Addr)
	if !ok {
		t.Fatalf("RemoteAddr type = %T, want appnet.Addr", c.RemoteAddr())
	}
	if addr.Net != appnet.TypeTCPDirect || addr.PubKey != pk {
		t.Errorf("RemoteAddr = %+v, want Net=%s PubKey=%s", addr, appnet.TypeTCPDirect, pk.Hex())
	}
}
