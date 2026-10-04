package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
)

func dmsgURL(pk cipher.PubKey) string { return "dmsg://" + pk.Hex() + ":80" }

// prodConfig is a visor config generated from the embedded deployment. The
// base config leaves the transport-setup and survey key sets to config gen,
// which fills them from the deployment; so does this.
func prodConfig() *V1 {
	svcs := deployment.Prod
	conf := MakeBaseConfig(nil, false, false, &svcs, nil)
	conf.Transport.TransportSetupPKs = svcs.TransportSetupPKs
	conf.SurveyWhitelist = svcs.SurveyWhitelist
	return conf
}

func rotated(t *testing.T) (*Services, cipher.PubKey) {
	t.Helper()
	next := deployment.Prod
	newTPD, _ := cipher.GenerateKeyPair()
	next.TransportDiscovery = dmsgURL(newTPD)
	next.TransportDiscoveryDmsg = dmsgURL(newTPD)
	return &next, newTPD
}

// A config generated from the deployment follows the deployment's new key.
func TestApplyDeploymentServicesFollowsDeploymentValues(t *testing.T) {
	conf := prodConfig()
	next, newTPD := rotated(t)

	changed := conf.ApplyDeploymentServices(next, nil)

	require.Equal(t, dmsgURL(newTPD), conf.Transport.Discovery)
	require.Contains(t, changed, "transport.discovery")
	require.Equal(t, deployment.Prod.AddressResolver, conf.Transport.AddressResolver, "unchanged service touched")
}

// An address the operator set is not the deployment's to change.
func TestApplyDeploymentServicesKeepsOperatorValues(t *testing.T) {
	conf := prodConfig()
	own, _ := cipher.GenerateKeyPair()
	conf.Transport.Discovery = dmsgURL(own)
	next, _ := rotated(t)

	changed := conf.ApplyDeploymentServices(next, nil)

	require.Equal(t, dmsgURL(own), conf.Transport.Discovery)
	require.NotContains(t, changed, "transport.discovery")
}

// A value applied by an earlier refresh is still the deployment's: a second
// rotation moves it again, though it no longer matches the embedded default.
func TestApplyDeploymentServicesFollowsTwoRotations(t *testing.T) {
	conf := prodConfig()
	first, _ := rotated(t)
	conf.ApplyDeploymentServices(first, nil)

	second, newer := rotated(t)
	conf.ApplyDeploymentServices(second, first)

	require.Equal(t, dmsgURL(newer), conf.Transport.Discovery)
}

// An empty field already resolves to the embedded default, so it is only
// filled when the deployment moved away from that default.
func TestApplyDeploymentServicesEmptyFields(t *testing.T) {
	conf := prodConfig()
	conf.ConfServiceDmsg = ""
	same := deployment.Prod
	require.NotContains(t, conf.ApplyDeploymentServices(&same, nil), "conf_service_dmsg")
	require.Empty(t, conf.ConfServiceDmsg)

	moved := deployment.Prod
	newConf, _ := cipher.GenerateKeyPair()
	moved.ConfDmsg = dmsgURL(newConf)
	require.Contains(t, conf.ApplyDeploymentServices(&moved, nil), "conf_service_dmsg")
	require.Equal(t, dmsgURL(newConf), conf.ConfServiceDmsg)
}

// The deployment key sets are replaced; operators' keys sit in the user_*
// fields and are untouched.
func TestApplyDeploymentServicesReplacesKeySets(t *testing.T) {
	conf := prodConfig()
	mine, _ := cipher.GenerateKeyPair()
	conf.Transport.UserTransportSetupPKs = []cipher.PubKey{mine}
	next := deployment.Prod
	tps, _ := cipher.GenerateKeyPair()
	next.TransportSetupPKs = []cipher.PubKey{tps}

	require.Contains(t, conf.ApplyDeploymentServices(&next, nil), "transport.transport_setup")
	require.Equal(t, []cipher.PubKey{tps}, conf.Transport.TransportSetupPKs)
	require.Equal(t, []cipher.PubKey{mine}, conf.Transport.UserTransportSetupPKs)
	require.ElementsMatch(t, []cipher.PubKey{tps, mine}, conf.EffectiveTransportSetupPKs())
}

// The current deployment changes nothing.
func TestApplyDeploymentServicesNoChange(t *testing.T) {
	conf := prodConfig()
	same := deployment.Prod
	require.Empty(t, conf.ApplyDeploymentServices(&same, nil))
}
