package visor

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// Two rotations in a row both land, also across a restart: the config the
// visor last applied is saved under local_path and read back by a fresh
// visor, so the first rotation's values still count as the deployment's.
func TestApplyDeploymentServicesTwoRotationsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	_, sk := cipher.GenerateKeyPair()
	confPath := filepath.Join(dir, "skywire-config.json")
	common, err := visorconfig.NewCommon(nil, confPath, &sk)
	require.NoError(t, err)
	svcs := deployment.Prod
	conf := visorconfig.MakeBaseConfig(common, false, false, &svcs, nil)
	conf.LocalPath = dir
	log := logging.MustGetLogger("test")

	rotate := func() (*visorconfig.Services, string) {
		next := deployment.Prod
		pk, _ := cipher.GenerateKeyPair()
		next.RouteFinder = "dmsg://" + pk.Hex() + ":80"
		next.RouteFinderDmsg = next.RouteFinder
		return &next, next.RouteFinder
	}

	v := &Visor{conf: conf, dmsgServersCache: NewDmsgServersCache(filepath.Join(dir, "dmsg_servers.json"))}
	first, firstRF := rotate()
	v.applyDeploymentServices(first, "test", log)
	require.Equal(t, firstRF, conf.Routing.RouteFinder)
	_, err = os.Stat(filepath.Join(dir, deploymentServicesFile))
	require.NoError(t, err, "the applied config was not saved")
	written, err := os.ReadFile(confPath) //nolint:gosec
	require.NoError(t, err)
	require.Contains(t, string(written), firstRF, "the config file was not updated")

	restarted := &Visor{conf: conf}
	second, secondRF := rotate()
	restarted.applyDeploymentServices(second, "test", log)
	require.Equal(t, secondRF, conf.Routing.RouteFinder)
}

// The conf service serves a flat Services object. The refresh used to read
// only the {"prod": …} envelope, so it failed on every response.
func TestParseServicesConfigFlat(t *testing.T) {
	body, err := json.Marshal(deployment.Prod)
	require.NoError(t, err)

	s, err := parseServicesConfig(body)
	require.NoError(t, err)
	require.Equal(t, deployment.Prod.ConfDmsg, s.ConfDmsg)
	require.Equal(t, deployment.Prod.TransportSetupPKs, s.TransportSetupPKs)
}

func TestParseServicesConfigEnvelope(t *testing.T) {
	prod, err := json.Marshal(deployment.Prod)
	require.NoError(t, err)
	body, err := json.Marshal(map[string]json.RawMessage{"prod": prod, "test": json.RawMessage(`{}`)})
	require.NoError(t, err)

	s, err := parseServicesConfig(body)
	require.NoError(t, err)
	require.Equal(t, deployment.Prod.ConfDmsg, s.ConfDmsg)
}

func TestParseServicesConfigRejectsEmpty(t *testing.T) {
	_, err := parseServicesConfig([]byte(`{}`))
	require.Error(t, err)
	_, err = parseServicesConfig([]byte(`not json`))
	require.Error(t, err)
}
