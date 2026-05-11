package skynetca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"testing"
	"time"
)

const testPK = "027087fe40d97f7f0be4a0dc768462ddbb371d4b9e7679d4f11f117d757b9856ed"

func TestMinter_RejectsForbiddenSuffix(t *testing.T) {
	ca, key, _ := GenerateCA(CAOptions{}) //nolint:errcheck,gosec
	m := NewMinter(ca, key, LeafOptions{})

	if _, err := m.For("foo.example.com"); err == nil {
		t.Errorf("minter accepted disallowed host")
	}
}

func TestMinter_AcceptsSkynetAndDmsg(t *testing.T) {
	ca, key, _ := GenerateCA(CAOptions{}) //nolint:errcheck,gosec
	m := NewMinter(ca, key, LeafOptions{})

	for _, host := range []string{testPK + ".skynet", testPK + ".dmsg"} {
		leaf, err := m.For(host)
		if err != nil {
			t.Fatalf("For(%q): %v", host, err)
		}
		if leaf.Leaf == nil || leaf.Leaf.DNSNames[0] != host {
			t.Errorf("DNSNames = %v, want [%s]", leaf.Leaf.DNSNames, host)
		}
		pool := x509.NewCertPool()
		pool.AddCert(ca)
		if _, err := leaf.Leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); err != nil {
			t.Errorf("chain failed to verify for %s: %v", host, err)
		}
	}
}

func TestMinter_Cache_ReturnsSameLeaf(t *testing.T) {
	ca, key, _ := GenerateCA(CAOptions{}) //nolint:errcheck,gosec
	m := NewMinter(ca, key, LeafOptions{})
	a, err := m.For(testPK + ".skynet")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.For(testPK + ".skynet")
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("cache miss for repeated host: %p != %p", a, b)
	}
}

func TestMinter_RenewBefore_TriggersFreshMint(t *testing.T) {
	ca, key, _ := GenerateCA(CAOptions{}) //nolint:errcheck,gosec
	// Validity barely above RenewBefore so the cached cert is
	// already considered stale immediately.
	m := NewMinter(ca, key, LeafOptions{Validity: 2 * time.Hour, RenewBefore: 3 * time.Hour})
	a, err := m.For(testPK + ".skynet")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.For(testPK + ".skynet")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("expected fresh mint when RenewBefore exceeds validity")
	}
}

// TestNameConstraints_VerifierRejectsForbiddenLeaf bypasses the
// minter's own permitted-suffix check and signs a leaf for a host
// outside the CA's permitted set, then asks Go's x509 verifier to
// validate it. This regression-tests Go's name-constraint
// enforcement, since browser TLS validation depends on it.
func TestNameConstraints_VerifierRejectsForbiddenLeaf(t *testing.T) {
	ca, caKey, _ := GenerateCA(CAOptions{PermittedDomains: []string{".skynet"}}) //nolint:errcheck,gosec

	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)            //nolint:errcheck,gosec
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 159)) //nolint:errcheck,gosec
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "evil.example.com"},
		DNSNames:     []string{"evil.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(7 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, _ := x509.ParseCertificate(der) //nolint:errcheck,gosec

	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: "evil.example.com"}); err == nil {
		t.Errorf("expected verification to fail (name constraints), got nil")
	}
}

func TestStaticMinter_NilLeafErrors(t *testing.T) {
	if _, err := NewStaticMinter(nil).For("anything"); err == nil {
		t.Errorf("expected error for nil leaf")
	}
}
