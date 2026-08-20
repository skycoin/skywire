//go:build !tinygo || (js && wasm)

package router

import (
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func trustedSet(pks ...cipher.PubKey) map[cipher.PubKey]struct{} {
	m := make(map[cipher.PubKey]struct{}, len(pks))
	for _, pk := range pks {
		m[pk] = struct{}{}
	}
	return m
}

func TestTransportQuery_SignVerify_RoundTrip(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 42}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}

	// Marshal/unmarshal must preserve the signature and fields.
	b, err := q.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	q2, err := UnmarshalTransportQuery(b)
	if err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// D verifies against its own PK + trusted RSN → OK.
	if err := q2.Verify(dstPK, trustedSet(rsnPK)); err != nil {
		t.Fatalf("verify (valid) failed: %v", err)
	}
}

func TestTransportQuery_Verify_UntrustedRSN(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 1}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// RSN signed correctly, but D does not trust it.
	if err := q.Verify(dstPK, trustedSet(otherPK)); err != ErrTransportQueryUntrustedRSN {
		t.Fatalf("want ErrTransportQueryUntrustedRSN, got %v", err)
	}
}

func TestTransportQuery_Verify_WrongTarget(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	otherDstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 7}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// A visor that is NOT the query's target must refuse.
	if err := q.Verify(otherDstPK, trustedSet(rsnPK)); err != ErrTransportQueryWrongTarget {
		t.Fatalf("want ErrTransportQueryWrongTarget, got %v", err)
	}
}

func TestTransportQuery_Verify_TamperedSignature(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 9}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// Tamper with the signed content after signing.
	q.Nonce = 10
	if err := q.Verify(dstPK, trustedSet(rsnPK)); err != ErrTransportQuerySigInvalid {
		t.Fatalf("want ErrTransportQuerySigInvalid, got %v", err)
	}
}

func TestBuildTransportQueryResponse_RejectsUntrusted(t *testing.T) {
	rsnPK, rsnSK := cipher.GenerateKeyPair()
	otherRSN, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 3}
	if err := q.Sign(rsnSK); err != nil {
		t.Fatalf("sign: %v", err)
	}
	// D trusts a DIFFERENT RSN → response builder must refuse (nil tm is fine,
	// the verify happens first).
	if _, err := BuildTransportQueryResponse(q, dstPK, trustedSet(otherRSN), nil); err != ErrTransportQueryUntrustedRSN {
		t.Fatalf("want ErrTransportQueryUntrustedRSN, got %v", err)
	}
}
