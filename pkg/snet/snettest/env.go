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
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/arclient"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/transport/pktable"
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

	table := pktable.NewTable(tableEntries)

	var hasDmsg, hasStcp, hasStcpr, hasStcph, hasSudp, hasSudpr, hasSudph bool

	for _, network := range networks {
		switch network {
		case dmsg.Type:
			hasDmsg = true
		case transport.STCPType:
			hasStcp = true
		case transport.STCPRType:
			hasStcpr = true
		case transport.STCPHType:
			hasStcph = true
		case transport.SUDPType:
			hasSudp = true
		case transport.SUDPRType:
			hasSudpr = true
		case transport.SUDPHType:
			hasSudph = true
		}
	}

	// Prepare `snets`.
	ns := make([]*snet.Network, len(keys))

	const stcpBasePort = 7033
	const sudpBasePort = 7533

	for i, pairs := range keys {
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
			SUDPR: &snet.SUDPRConfig{
				LocalAddr:       "127.0.0.1:" + strconv.Itoa(sudpBasePort+i+1000),
				AddressResolver: skyenv.TestAddressResolverAddr,
			},
			SUDPH: &snet.SUDPHConfig{
				AddressResolver: skyenv.TestAddressResolverAddr,
			},
		}

		clients := snet.NetworkClients{
			Direct: make(map[string]transport.Client),
		}

		if hasDmsg {
			clients.DmsgC = dmsg.NewClient(pairs.PK, pairs.SK, dmsgD, nil)
			go clients.DmsgC.Serve()
		}

		addressResolver := new(arclient.MockAPIClient)

		if hasStcp {
			conf := transport.ClientConfig{
				Type:      transport.STCPType,
				PK:        pairs.PK,
				SK:        pairs.SK,
				Table:     table,
				LocalAddr: networkConfigs.STCP.LocalAddr,
			}

			clients.Direct[transport.STCPType] = transport.NewClient(conf)
		}

		if hasStcpr {
			conf := transport.ClientConfig{
				Type:            transport.STCPRType,
				PK:              pairs.PK,
				SK:              pairs.SK,
				AddressResolver: addressResolver,
				LocalAddr:       networkConfigs.STCPR.LocalAddr,
			}

			clients.Direct[transport.STCPRType] = transport.NewClient(conf)
		}

		if hasStcph {
			conf := transport.ClientConfig{
				Type:            transport.STCPHType,
				PK:              pairs.PK,
				SK:              pairs.SK,
				AddressResolver: addressResolver,
			}

			clients.Direct[transport.STCPHType] = transport.NewClient(conf)
		}

		if hasSudp {
			conf := transport.ClientConfig{
				Type:      transport.SUDPType,
				PK:        pairs.PK,
				SK:        pairs.SK,
				Table:     table,
				LocalAddr: networkConfigs.SUDP.LocalAddr,
			}
			clients.Direct[transport.SUDPType] = transport.NewClient(conf)
		}

		if hasSudpr {
			conf := transport.ClientConfig{
				Type:            transport.SUDPRType,
				PK:              pairs.PK,
				SK:              pairs.SK,
				AddressResolver: addressResolver,
				LocalAddr:       networkConfigs.SUDPR.LocalAddr,
			}

			clients.Direct[transport.SUDPRType] = transport.NewClient(conf)
		}

		if hasSudph {
			conf := transport.ClientConfig{
				Type:            transport.SUDPHType,
				PK:              pairs.PK,
				SK:              pairs.SK,
				AddressResolver: addressResolver,
			}

			clients.Direct[transport.SUDPHType] = transport.NewClient(conf)
		}

		snetConfig := snet.Config{
			PubKey:         pairs.PK,
			SecKey:         pairs.SK,
			NetworkConfigs: networkConfigs,
		}

		n := snet.NewRaw(snetConfig, clients)
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
