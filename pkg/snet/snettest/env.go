package snettest

import (
	"strconv"
	"testing"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcp"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcph"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcpr"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/sudp"
)

// KeyPair holds a public/private key pair.
type KeyPair struct {
	PK cipher.PubKey
	SK cipher.SecKey
}

// GenKeyPairs generates 'n' number of key pairs.
func GenKeyPairs(n int) []KeyPair {
	pairs := make([]KeyPair, n)
	for i := range pairs {
		pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte{byte(i)})
		if err != nil {
			panic(err)
		}

		pairs[i] = KeyPair{PK: pk, SK: sk}
	}

	return pairs
}

// Env contains a network test environment.
type Env struct {
	DmsgD    disc.APIClient
	DmsgS    *dmsg.Server
	Keys     []KeyPair
	Nets     []*snet.Network
	teardown func()
}

// NewEnv creates a `network.Network` test environment.
// `nPairs` is the public/private key pairs of all the `network.Network`s to be created.
func NewEnv(t *testing.T, keys []KeyPair, networks []string) *Env {

	// Prepare `dmsg`.
	dmsgD := disc.NewMock()
	dmsgS, dmsgSErr := createDmsgSrv(t, dmsgD)

	const baseSTCPPort = 7033

	tableEntries := make(map[cipher.PubKey]string)
	for i, pair := range keys {
		tableEntries[pair.PK] = "127.0.0.1:" + strconv.Itoa(baseSTCPPort+i)
	}

	table := stcp.NewTable(tableEntries)

	var hasDmsg, hasStcp, hasStcpr, hasStcph, hasSudp bool

	for _, network := range networks {
		switch network {
		case dmsg.Type:
			hasDmsg = true
		case stcp.Type:
			hasStcp = true
		case stcpr.Type:
			hasStcpr = true
		case stcph.Type:
			hasStcph = true
		case sudp.Type:
			hasSudp = true
		}
	}

	// Prepare `snets`.
	ns := make([]*snet.Network, len(keys))

	const stcpBasePort = 7033
	const sudpBasePort = 7533

	for i, pairs := range keys {
		var clients snet.NetworkClients

		if hasDmsg {
			clients.DmsgC = dmsg.NewClient(pairs.PK, pairs.SK, dmsgD, nil)
			go clients.DmsgC.Serve()
		}

		// TODO: https://github.com/SkycoinProject/skywire-mainnet/issues/395
		// addr := "127.0.0.1:" + strconv.Itoa(stcpBasePort+i)
		//
		// addressResolver, err := arclient.NewHTTP(skyenv.TestAddressResolverAddr, pairs.PK, pairs.SK)
		// if err != nil {
		// 	panic(err)
		// }

		if hasStcp {
			clients.StcpC = stcp.NewClient(pairs.PK, pairs.SK, table)
		}

		if hasStcpr {
			// TODO: https://github.com/SkycoinProject/skywire-mainnet/issues/395
			// clients.StcprC = stcpr.NewClient(pairs.PK, pairs.SK, addressResolver, addr)
		}
		//
		if hasStcph {
			// 	clients.StcphC = stcph.NewClient(pairs.PK, pairs.SK, addressResolver)
		}

		if hasSudp {
			clients.SudpC = sudp.NewClient(pairs.PK, pairs.SK, table)
		}

		networkConfigs := snet.NetworkConfigs{
			Dmsg: &snet.DmsgConfig{
				SessionsCount: 1,
			},
			STCP: &snet.STCPConfig{
				LocalAddr: "127.0.0.1:" + strconv.Itoa(stcpBasePort+i),
			},
			STCPR: &snet.STCPRConfig{
				LocalAddr:       "127.0.0.1:" + strconv.Itoa(stcpBasePort+i+1000),
				AddressResolver: skyenv.TestAddressResolverAddr,
			},
			STCPH: &snet.STCPHConfig{
				AddressResolver: skyenv.TestAddressResolverAddr,
			},
			SUDP: &snet.SUDPConfig{
				LocalAddr: "127.0.0.1:" + strconv.Itoa(sudpBasePort+i),
			},
		}

		snetConfig := snet.Config{
			PubKey:         pairs.PK,
			SecKey:         pairs.SK,
			NetworkConfigs: networkConfigs,
		}

		n := snet.NewRaw(snetConfig, &clients)
		require.NoError(t, n.Init())
		ns[i] = n
	}

	// Prepare teardown closure.
	teardown := func() {
		for _, n := range ns {
			assert.NoError(t, n.Close())
		}
		assert.NoError(t, dmsgS.Close())
		for err := range dmsgSErr {
			assert.NoError(t, err)
		}
	}

	return &Env{
		DmsgD:    dmsgD,
		DmsgS:    dmsgS,
		Keys:     keys,
		Nets:     ns,
		teardown: teardown,
	}
}

// Teardown shutdowns the Env.
func (e *Env) Teardown() { e.teardown() }

func createDmsgSrv(t *testing.T, dc disc.APIClient) (srv *dmsg.Server, srvErr <-chan error) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte("s"))
	require.NoError(t, err)
	l, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	srv = dmsg.NewServer(pk, sk, dc, 100)
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Serve(l, "")
		close(errCh)
	}()
	<-srv.Ready()
	return srv, errCh
}
