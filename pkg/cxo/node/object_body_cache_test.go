package node

import (
	"bytes"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
)

func TestObjectBodyCacheSharesLargeObjects(t *testing.T) {
	var oc objectBodyCache
	big := bytes.Repeat([]byte{7}, sharedObjectMinBytes)
	k := cipher.SumSHA256(big)
	a, b := oc.body(k, big), oc.body(k, big)
	if &a[0] != &b[0] {
		t.Fatal("two requests for one large object got separate encodings")
	}
	if !bytes.Equal(a, (&msg.Object{Value: big}).Encode()) {
		t.Fatal("shared body differs from the per-request encoding")
	}

	small := []byte("small")
	ks := cipher.SumSHA256(small)
	if s1, s2 := oc.body(ks, small), oc.body(ks, small); &s1[0] == &s2[0] {
		t.Fatal("small objects should not be cached")
	}
}

func TestObjectBodyCacheIsBounded(t *testing.T) {
	var oc objectBodyCache
	val := make([]byte, 8<<20)
	for i := 0; i < 20; i++ {
		val[0] = byte(i)
		oc.body(cipher.SumSHA256(val), val)
	}
	if oc.bytes > sharedObjectMaxBytes {
		t.Fatalf("cache holds %d bytes, cap %d", oc.bytes, sharedObjectMaxBytes)
	}
	if len(oc.items) != len(oc.order) {
		t.Fatalf("items %d != order %d", len(oc.items), len(oc.order))
	}
}
