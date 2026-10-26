package disc

import (
	"testing"

	jsoniter "github.com/json-iterator/go"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Go builds signed entries over jsoniter.ConfigFastest bytes. Those entries
// must still verify, and entries signed now must verify under the old codec.
func TestSignedPayloadMatchesJsoniter(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	ds, _ := cipher.GenerateKeyPair()
	entries := []*Entry{
		{Version: "0.0.1", Sequence: 7, Timestamp: 1761234567890123456, Static: pk, ClientType: "visor<&>",
			Client: &Client{DelegatedServers: []cipher.PubKey{ds}}, Protocol: "smux"},
		{Version: "0.0.1", Static: pk, Server: &Server{Address: "1.2.3.4:80", AddressWS: "wss://h/p?a=1&b=<2>",
			AvailableSessions: 2048, ServerType: "official", DHTBootstrap: true}},
		{Version: "0.0.1", Static: pk, Client: &Client{}},
	}
	for _, e := range entries {
		e.Signature = ""
		old, err := jsoniter.ConfigFastest.Marshal(e)
		require.NoError(t, err)
		now, err := signedPayload(e)
		require.NoError(t, err)
		require.Equal(t, string(old), string(now))

		sig, err := cipher.SignPayload(old, sk)
		require.NoError(t, err)
		e.Signature = sig.Hex()
		require.NoError(t, e.VerifySignature(), "entry signed over jsoniter bytes")

		require.NoError(t, e.Sign(sk))
		sig2 := cipher.Sig{}
		require.NoError(t, sig2.UnmarshalText([]byte(e.Signature)))
		require.NoError(t, cipher.VerifyPubKeySignedPayload(pk, sig2, old), "new signature over jsoniter bytes")

		// What a discovery server stores and serves is decoded and checked again.
		wire, err := json.Marshal(e)
		require.NoError(t, err)
		var got Entry
		require.NoError(t, jsoniter.ConfigFastest.Unmarshal(wire, &got))
		require.NoError(t, got.VerifySignature())
	}
}
