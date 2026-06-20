// Package msg pkg/cxo/node/msg/msg_test.go: unit tests for the CXO node
// message wire format — encode/decode round-trips, type stringification, and
// the decode error paths.
package msg

import (
	"testing"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRoundTrip encodes each message type and decodes it back, asserting the
// type byte, the decoded type, and value equality.
func TestRoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	var sig cipher.Sig
	key := cipher.SumSHA256([]byte("object"))

	msgs := []Msg{
		&Ping{},
		&Pong{},
		&Ok{},
		&Syn{Protocol: Version, NodeID: pk},
		&Ack{NodeID: pk},
		&Err{Err: "boom"},
		&Sub{Feed: pk},
		&Unsub{Feed: pk},
		&RqList{},
		&List{Feeds: []cipher.PubKey{pk, pk2}},
		&Root{Feed: pk, Nonce: 7, Seq: 3, Value: []byte{1, 2, 3}, Sig: sig},
		&RqObject{Key: key},
		&Object{Value: []byte{9, 9, 9}},
		&RqPreview{Feed: pk},
		&RqPeers{Feed: pk},
		&Peers{Feed: pk, List: []PeerInfo{{
			PubKey:   pk,
			Metadata: []byte("meta"),
			TCPAddr:  "1.2.3.4:5550",
			UDPAddr:  "1.2.3.4:5551",
		}}},
	}

	for _, m := range msgs {
		t.Run(m.Type().String(), func(t *testing.T) {
			enc := m.Encode()
			require.NotEmpty(t, enc)
			assert.Equal(t, byte(m.Type()), enc[0], "first byte must be the type")

			dec, err := Decode(enc)
			require.NoError(t, err)
			assert.Equal(t, m.Type(), dec.Type())
			assert.Equal(t, m, dec)
		})
	}
}

// TestTypeString covers the name mapping and the out-of-range fallback.
func TestTypeString(t *testing.T) {
	assert.Equal(t, "Ping", Type(PingType).String())
	assert.Equal(t, "Pong", Type(PongType).String())
	assert.Equal(t, "Peers", Type(PeersType).String())
	assert.Equal(t, "Type<0>", Type(0).String())
	assert.Equal(t, "Type<99>", Type(99).String())
}

// TestDecodeEmpty verifies an empty slice is rejected.
func TestDecodeEmpty(t *testing.T) {
	_, err := Decode(nil)
	assert.ErrorIs(t, err, ErrEmptyMessage)

	_, err = Decode([]byte{})
	assert.ErrorIs(t, err, ErrEmptyMessage)
}

// TestDecodeInvalidType verifies a type byte outside the registry is rejected
// with an InvalidTypeError, both below and above the valid range.
func TestDecodeInvalidType(t *testing.T) {
	for _, b := range []byte{0, 99} {
		_, err := Decode([]byte{b})
		require.Error(t, err)
		var ite InvalidTypeError
		require.ErrorAs(t, err, &ite)
		assert.Equal(t, Type(b), ite.Type())
		assert.Contains(t, ite.Error(), "invalid message type")
	}
}

// TestDecodeIncomplete verifies trailing bytes beyond the encoded message are
// rejected.
func TestDecodeIncomplete(t *testing.T) {
	// Ping encodes to a single type byte; an extra trailing byte is leftover.
	_, err := Decode([]byte{byte(PingType), 0xff})
	assert.ErrorIs(t, err, ErrIncomplieDecoding)
}

// TestDecodeTruncated verifies a message whose body is too short surfaces the
// decoder error.
func TestDecodeTruncated(t *testing.T) {
	// Sub needs a full public key after the type byte; supplying none fails.
	_, err := Decode([]byte{byte(SubType)})
	assert.Error(t, err)
}
