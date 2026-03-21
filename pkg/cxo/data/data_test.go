package data

import (
	"bytes"
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

func testRoot(s string) (r *Root) {

	_, sk, _ := cipher.GenerateDeterministicKeyPair([]byte("test")) //nolint:errcheck

	r = new(Root)
	r.Access = 996
	r.Create = 998
	r.Time = 995

	r.Seq = 0
	r.Prev = cipher.SHA256{}

	r.Hash = cipher.SumSHA256([]byte(s))
	r.Sig, _ = cipher.SignHash(r.Hash, sk) //nolint:errcheck
	return
}

func TestRoot_Encode(t *testing.T) {
	// Encode() (p []byte)

	r := testRoot("ha-ha")
	if bytes.Compare(r.Encode(), encoder.Serialize(r)) != 0 {
		t.Error("wrong")
	}
}

func TestRoot_Decode(t *testing.T) {
	// Decode(p []byte) (err error)

	r := testRoot("ha-ha")

	p := encoder.Serialize(r)

	x := new(Root)
	if err := x.Decode(p); err != nil {
		t.Fatal(err)
	}

	if *x != *r {
		t.Error("wrong")
	}

	if err := x.Decode(p[:12]); err == nil {
		t.Error("missing error")
	}

}

func TestRoot_Validate(t *testing.T) {

	r := testRoot("seed")

	if err := r.Validate(); err != nil {
		t.Error(err)
	}

	// empty hash
	var hash cipher.SHA256
	hash, r.Hash = r.Hash, cipher.SHA256{}
	if err := r.Validate(); err == nil {
		t.Error("missing error")
	}
	r.Hash = hash

	// unexpected prev
	r.Prev = cipher.SumSHA256([]byte("random"))

	if err := r.Validate(); err == nil {
		t.Error("missing error")
	}

	// misisng prev
	r.Seq, r.Prev = 1, cipher.SHA256{}
	if err := r.Validate(); err == nil {
		t.Error("missing error")
	}

	r.Seq = 0
	r.Sig = (cipher.Sig{})

	if err := r.Validate(); err == nil {
		t.Error("missing error")
	}

	// zero Time
	r.Time = 0
	if err := r.Validate(); err == nil {
		t.Error("missing error")
	}

	// valid

	r = testRoot("seed")

	if err := r.Validate(); err != nil {
		t.Error(err)
	}

}
