package skynetca

import (
	"bytes"
	"crypto/x509"
	"net"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenerateCA_Defaults(t *testing.T) {
	cert, key, err := GenerateCA(CAOptions{})
	if err != nil {
		t.Fatalf("GenerateCA: %v", err)
	}
	if !cert.IsCA {
		t.Errorf("IsCA = false, want true")
	}
	if !cert.MaxPathLenZero || cert.MaxPathLen != 0 {
		t.Errorf("MaxPathLenZero=%v MaxPathLen=%d, want pathlen:0", cert.MaxPathLenZero, cert.MaxPathLen)
	}
	if cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("CertSign usage missing")
	}
	if !cert.PermittedDNSDomainsCritical {
		t.Errorf("PermittedDNSDomainsCritical = false, want true")
	}
	want := []string{".skynet", ".dmsg", ".skysocks"}
	got := cert.PermittedDNSDomains
	if len(got) != len(want) {
		t.Fatalf("PermittedDNSDomains = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("PermittedDNSDomains[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if v := cert.NotAfter.Sub(cert.NotBefore); v < 10*365*24*time.Hour {
		t.Errorf("validity %v shorter than 10y", v)
	}
	if key == nil {
		t.Errorf("key is nil")
	}
}

func TestGenerateCA_CustomDomains(t *testing.T) {
	cert, _, err := GenerateCA(CAOptions{PermittedDomains: []string{".test"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := cert.PermittedDNSDomains; len(got) != 1 || got[0] != ".test" {
		t.Errorf("PermittedDNSDomains = %v, want [.test]", got)
	}
}

func TestSaveLoadCA_Roundtrip(t *testing.T) {
	cert, key, err := GenerateCA(CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certPath := dir + "/ca.crt"
	keyPath := dir + "/ca.key"
	if err := SaveCA(cert, key, certPath, keyPath); err != nil {
		t.Fatal(err)
	}
	loadedCert, loadedKey, err := LoadCA(certPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if loadedCert.SerialNumber.Cmp(cert.SerialNumber) != 0 {
		t.Errorf("serial mismatch")
	}
	// Compare keys via DER round-trip rather than the deprecated
	// ecdsa.PrivateKey.D field (Go 1.26 deprecates direct big.Int
	// access on crypto values).
	gotDER, err := x509.MarshalECPrivateKey(loadedKey)
	if err != nil {
		t.Fatalf("marshal loaded key: %v", err)
	}
	wantDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal original key: %v", err)
	}
	if !bytes.Equal(gotDER, wantDER) {
		t.Errorf("key mismatch after SaveCA/LoadCA round-trip")
	}
}

func TestLoadCA_RejectsNonCA(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/junk.crt", []byte("not pem"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadCA(dir+"/junk.crt", dir+"/junk.key"); err == nil {
		t.Errorf("expected error loading non-PEM")
	}
}

func TestFingerprint_StableFormat(t *testing.T) {
	cert, _, err := GenerateCA(CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fp := Fingerprint(cert)
	// 64 hex digits + 31 colons = 95.
	if len(fp) != 95 {
		t.Errorf("fingerprint length %d != 95: %q", len(fp), fp)
	}
	if strings.Count(fp, ":") != 31 {
		t.Errorf("colons = %d, want 31", strings.Count(fp, ":"))
	}
}

// A constrained CA can name only SkyDNS's range by IP, so its key cannot sign
// for a LAN or internet address even outside the minter.
func TestCAConstrainsIPAddresses(t *testing.T) {
	ca, key, err := GenerateCA(CAOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := NewMinter(ca, key, LeafOptions{})
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	verify := func(ip string) error {
		c, err := m.mint(&x509.Certificate{IPAddresses: []net.IP{net.ParseIP(ip)}})
		if err != nil {
			return err
		}
		leaf, err := x509.ParseCertificate(c.Certificate[0])
		if err != nil {
			return err
		}
		_, err = leaf.Verify(x509.VerifyOptions{Roots: roots})
		return err
	}
	if err := verify("198.19.0.53"); err != nil {
		t.Fatalf("resolver address: %v", err)
	}
	for _, ip := range []string{"192.168.1.1", "1.1.1.1"} {
		if verify(ip) == nil {
			t.Fatalf("%s verified under the constrained CA", ip)
		}
		if _, err := m.ForIP(netip.MustParseAddr(ip)); err == nil {
			t.Fatalf("ForIP minted a leaf for %s", ip)
		}
	}
}
