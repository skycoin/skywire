package registry

import (
	"fmt"
	"strconv"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/encoder"
)

// A Root represents root object of a feed
type Root struct {
	Refs []Dynamic // main branches

	Descriptor []byte // decriptor of the Root

	Reg RegistryRef // registry of the Root

	Pub   cipher.PubKey // feed of the Root
	Nonce uint64        // head of the feed

	Seq  uint64 // seq number
	Time int64  // timestamp (unix nano)

	// Both Sig and Hash are not parts of
	// a Root. But they used everywerer

	Sig  cipher.Sig    `enc:"-"` // signature
	Hash cipher.SHA256 `enc:"-"` // hash of this encoded Root

	// Prev is hash of previous Root, the Prev can
	// be blank is Seq of the Root is zero, that
	// means the Root is first in chain
	Prev cipher.SHA256

	// IsFull means that this Root object
	// has been successfully colelcted by this
	// machine. E.g. this field is not part
	// of a Root, and this field is machine
	// local
	IsFull bool `enc:"-"`
}

// Encode the Root
func (r *Root) Encode() []byte {
	return encoder.Serialize(r)
}

// Short return string like "1a2ef33/1234/2" (pub_key/nonce/seq),
// where the pub_key is hexadecimal encoded string trimmed to first
// seven symbols, and the nonce is first four numbers of the nonce.
// Thus, a nonce like 1100 is equal to 110057834738. Keep that in
// mind if somthing looks wrong and use the String method in critial
// parts and for debugging
func (r *Root) Short() string {

	var short = strconv.FormatUint(r.Nonce, 10) // short nonce

	if len(short) > 4 {
		short = short[:4]
	}

	return fmt.Sprintf("%s/%s/%d",
		r.Pub.Hex()[:7],
		short,
		r.Seq)
}

// String implements fmt.Stringer interface.
// The String method returns string like pub_key/nonce/seq:hash,
// where the pub_key and the hash trimmed to first seven symbols
// (hexadecimal encoding used)
func (r *Root) String() string {
	return fmt.Sprintf("%s/%d/%d:%s",
		r.Pub.Hex()[:7],
		r.Nonce,
		r.Seq,
		r.Hash.Hex()[:7])
}

// DecodeRoot decodes and encoded Root object
func DecodeRoot(val []byte) (r *Root, err error) {
	r = new(Root)
	if _, err = encoder.DeserializeRaw(val, r); err != nil {
		r = nil
	}
	return
}

// Walk through elements of the Root. Given WalkFunc will not
// be called with hash of the Root and with hash of Registry of
// the Root. The pack argument must have related registry.
// E.g. this preparation should be done before. Short wrods
// the Walk calls (*Dynamic).Walk for every Dynamic reference
// of the Root (see Refs field)
func (r *Root) Walk(pack Pack, walkFunc WalkFunc) (err error) {

	for _, dr := range r.Refs {
		if err = dr.Walk(pack, walkFunc); err != nil {
			if err == ErrStopIteration {
				err = nil
			}
			return
		}
	}

	return

}
