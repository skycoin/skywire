package visor

import (
	"encoding/json"
	"net"
	"os"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func resetAdvertisePolicy(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { netutil.SetAdvertisePolicy(netutil.AddrPolicy{}) })
}

func TestNetworkSetPersistsAndInstalls(t *testing.T) {
	resetAdvertisePolicy(t)
	conf, path := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}

	deny := []string{"docker*", "10.8.0.0/16"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Deny: &deny}))
	require.Equal(t, deny, netutil.AdvertisePolicy().Deny)

	raw, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	var saved visorconfig.V1
	require.NoError(t, json.Unmarshal(raw, &saved))
	require.NotNil(t, saved.Transport.Network)
	require.Equal(t, deny, saved.Transport.Network.Deny)

	// A nil field keeps its list.
	allow := []string{"eth*"}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Allow: &allow}))
	p := netutil.AdvertisePolicy()
	require.Equal(t, allow, p.Allow)
	require.Equal(t, deny, p.Deny)

	st := v.NetworkState()
	require.Equal(t, allow, st.Allow)
	require.Equal(t, deny, st.Deny)

	// Empty lists clear the policy and the config block.
	none := []string{""}
	require.NoError(t, v.NetworkSet(visorapi.NetworkSettings{Allow: &none, Deny: &none}))
	require.True(t, netutil.AdvertisePolicy().IsZero())
	require.Nil(t, conf.Transport.Network)
}

func TestNetworkSetRejectsBadEntry(t *testing.T) {
	resetAdvertisePolicy(t)
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	bad := []string{"10.0.0.0/40"}
	require.Error(t, v.NetworkSet(visorapi.NetworkSettings{Deny: &bad}))
	require.True(t, netutil.AdvertisePolicy().IsZero())
	require.Nil(t, conf.Transport.Network)
}

func TestConfigSetNetworkPolicyIsLive(t *testing.T) {
	resetAdvertisePolicy(t)
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	changes, err := v.SetConfigFields(map[string]json.RawMessage{
		"transport.network": json.RawMessage(`{"deny":["wlan*"]}`),
	})
	require.NoError(t, err)
	require.Len(t, changes, 1)
	require.True(t, changes[0].Live, "transport.network applies without a restart")
	require.Equal(t, []string{"wlan*"}, netutil.AdvertisePolicy().Deny)
}

func TestInstallNetworkPolicyFromConfig(t *testing.T) {
	resetAdvertisePolicy(t)
	conf, _ := newTestConfig(t)
	conf.Transport.Network = &visorconfig.NetworkPolicy{Allow: []string{"eth*"}}
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	v.installNetworkPolicy()
	require.Equal(t, []string{"eth*"}, netutil.AdvertisePolicy().Allow)

	// A bad entry in the file leaves every interface advertised.
	netutil.SetAdvertisePolicy(netutil.AddrPolicy{})
	conf.Transport.Network = &visorconfig.NetworkPolicy{Deny: []string{"eth["}}
	v.installNetworkPolicy()
	require.True(t, netutil.AdvertisePolicy().IsZero())
}

// Over the CLI's RPC client an empty list clears the setting. gob drops empty
// slices, so without care the clear arrived as "keep".
func TestNetworkSetOverRPC(t *testing.T) {
	resetAdvertisePolicy(t)
	conf, _ := newTestConfig(t)
	v := &Visor{conf: conf, log: logging.MustGetLogger("test")}
	srv := rpc.NewServer()
	require.NoError(t, registerVisorRPC(srv, visorapi.RPCPrefix, &RPC{visor: v, log: logrus.New()}))
	sc, cc := net.Pipe()
	go srv.ServeConn(sc)
	api := visorapi.NewRPCClient(nil, cc, visorapi.RPCPrefix, 30*time.Second)

	deny := []string{"eno1"}
	require.NoError(t, api.NetworkSet(visorapi.NetworkSettings{Deny: &deny}))
	require.Equal(t, deny, netutil.AdvertisePolicy().Deny)

	cleared := []string{}
	require.NoError(t, api.NetworkSet(visorapi.NetworkSettings{Deny: &cleared}))
	require.True(t, netutil.AdvertisePolicy().IsZero())
	require.Nil(t, conf.Transport.Network)
}
