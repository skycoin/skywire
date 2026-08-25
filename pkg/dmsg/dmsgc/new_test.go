// Package dmsgc pkg/dmsg/dmsgc/new_test.go: unit tests for the New constructor,
// exercising the discovery-wrapping branches (hypervisor proxy, LAN
// priority, direct/direct-only, multi-deployment seeding).
package dmsgc

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func testBroadcaster() *appevent.Broadcaster {
	return appevent.NewBroadcaster(logging.MustGetLogger("dmsgc-test"), time.Second)
}

func serverEntry(addr string) *disc.Entry {
	pk, _ := cipher.GenerateKeyPair()
	return &disc.Entry{Static: pk, Server: &disc.Server{Address: addr}}
}

// newClient is a small wrapper to keep each test's New(...) call short.
func newClient(t *testing.T, conf *DmsgConfig, direct disc.APIClient, directOnly bool) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	mLog := logging.NewMasterLogger()
	c := New(pk, sk, testBroadcaster(), conf, &http.Client{}, direct, directOnly, mLog)
	require.NotNil(t, c)
	require.Equal(t, pk, c.LocalPK())
}

func TestNew_Minimal(t *testing.T) {
	// Empty config → New falls back to a single empty deployment.
	newClient(t, &DmsgConfig{}, nil, false)
}

func TestNew_BasicDiscovery(t *testing.T) {
	conf := &DmsgConfig{
		Discovery:     "http://dmsgd.local",
		SessionsCount: 1,
		Servers:       []*disc.Entry{serverEntry("1.2.3.4:8080")},
	}
	newClient(t, conf, nil, false)
}

func TestNew_DmsgOnlyDiscoveryFallback(t *testing.T) {
	// No plain Discovery URL → should fall back to the dmsg:// URL.
	conf := &DmsgConfig{
		DiscoveryDmsg: "dmsg://024d.../dmsg-discovery",
		SessionsCount: 1,
	}
	newClient(t, conf, nil, false)
}

func TestNew_HypervisorDiscovery(t *testing.T) {
	conf := &DmsgConfig{
		Discovery:           "http://dmsgd.local",
		HypervisorDiscovery: "http://hyp.local",
		SessionsCount:       1,
	}
	newClient(t, conf, nil, false)
}

func TestNew_LANServers(t *testing.T) {
	conf := &DmsgConfig{
		Discovery:     "http://dmsgd.local",
		SessionsCount: 1,
		LANServers:    []*disc.Entry{serverEntry("192.168.1.1:8085")},
	}
	newClient(t, conf, nil, false)
}

func TestNew_DirectFallback(t *testing.T) {
	conf := &DmsgConfig{Discovery: "http://dmsgd.local", SessionsCount: 1}
	newClient(t, conf, &mockDiscClient{}, false)
}

func TestNew_DirectOnly(t *testing.T) {
	conf := &DmsgConfig{Discovery: "http://dmsgd.local", SessionsCount: 1}
	newClient(t, conf, &mockDiscClient{}, true)
}

func TestNew_MultiDeployment(t *testing.T) {
	// Two deployments: the second is attached via AddDiscovery, and
	// each deployment's servers are seeded (with one duplicate PK shared
	// to exercise the dedup path).
	shared := serverEntry("5.5.5.5:8080")
	conf := &DmsgConfig{
		Deployments: []Deployment{
			{
				Discovery:     "http://dmsgd-a.local",
				SessionsCount: 1,
				Servers:       []*disc.Entry{shared, serverEntry("1.1.1.1:8080")},
			},
			{
				Discovery:     "http://dmsgd-b.local",
				DiscoveryDmsg: "dmsg://024d.../dmsg-discovery",
				SessionsCount: 1,
				Servers:       []*disc.Entry{shared, serverEntry("2.2.2.2:8080")}, // shared is a dup
			},
		},
	}
	newClient(t, conf, nil, false)
}

func TestNew_SkipsInvalidSeedEntries(t *testing.T) {
	// nil entry, null-PK entry, and nil-Server entry must all be skipped
	// by the seed loop without panicking.
	nullPK := &disc.Entry{Static: cipher.PubKey{}, Server: &disc.Server{Address: "x"}}
	noServer := &disc.Entry{Static: serverEntry("").Static}
	conf := &DmsgConfig{
		Discovery:     "http://dmsgd.local",
		SessionsCount: 1,
		Servers:       []*disc.Entry{nil, nullPK, noServer, serverEntry("9.9.9.9:8080")},
	}
	newClient(t, conf, nil, false)
}
