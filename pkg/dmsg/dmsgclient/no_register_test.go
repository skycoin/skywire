// no_register_test.go: the outbound-only CLI tools must not publish a dmsg
// discovery entry.
//
// An entry is an INBOUND-reachability advertisement — "these are the servers
// you can reach me through". A tool that only dials out opens no dmsg listener,
// so its entry names servers for a destination that accepts nothing: a write on
// the discovery, a row in its store and a TTL to expire, for a keypair most of
// these tools generate per run and discard seconds later.
//
// Registration and resolution are independent axes (see dmsg.Config.NoRegister):
// suppressing the entry does not affect dialing, or resolving anyone else.
package dmsgclient

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Every helper in this package builds its client config through clientConfig,
// so this is the one place the package global has to reach. The failure mode it
// guards is silent — a tool that registers when it should not still works, it
// just litters the discovery — so nothing else would catch a dropped field.
func TestClientConfigCarriesNoRegister(t *testing.T) {
	t.Cleanup(func() { NoRegister = false })

	NoRegister = true
	require.True(t, clientConfig(1).NoRegister,
		"an outbound-only tool must not publish an entry")

	NoRegister = false
	require.False(t, clientConfig(1).NoRegister,
		"a listening tool keeps its entry: that is what makes it dialable")

	require.Equal(t, 3, clientConfig(3).MinSessions, "session count still plumbs through")
}

// And the config the helpers build is honored by the client it is handed to —
// Unpublished is what `visor state` reports and what the serve loop consults
// before its initial post.
func TestNoRegisterSuppressesTheEntry(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	require.True(t, dmsg.NewClient(pk, sk, nil, &dmsg.Config{MinSessions: 1, NoRegister: true}).Unpublished())
	require.False(t, dmsg.NewClient(pk, sk, nil, &dmsg.Config{MinSessions: 1}).Unpublished())
}
