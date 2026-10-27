package cliconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func TestParseMaxPerType(t *testing.T) {
	m, err := parseMaxPerType("sudph=40, webrtc=10,stcpr=0")
	require.NoError(t, err)
	require.Equal(t, map[string]int{"sudph": 40, "webrtc": 10, "stcpr": 0}, m)
	for _, bad := range []string{"sudph", "sudph=x", "sudph=-1", "=3"} {
		_, err := parseMaxPerType(bad)
		require.Error(t, err, bad)
	}
}

func TestCheckMemoryLimit(t *testing.T) {
	for _, ok := range []string{"none", "auto", "256MiB", "1GiB", "512MB", "1.5GB", "300000000"} {
		require.NoError(t, checkMemoryLimit(ok), ok)
	}
	for _, bad := range []string{"lots", "256 megs", "-1MiB", "MiB"} {
		require.Error(t, checkMemoryLimit(bad), bad)
	}
}

// A variable that is set wins over the old config. One that is not set leaves
// the value keepAutoconnect carried over.
func TestAutoconnectVarsWinOnlyWhenSet(t *testing.T) {
	restoreConf, restoreTotal, restoreLinks, restoreMap := conf, autoconnMaxTotal, autoconnSiteLinks, autoconnMaxPerType
	t.Cleanup(func() {
		conf, autoconnMaxTotal, autoconnSiteLinks, autoconnMaxPerType = restoreConf, restoreTotal, restoreLinks, restoreMap
	})
	one := 1
	conf = &visorconfig.V1{Transport: &visorconfig.Transport{Autoconnect: visorconfig.Autoconnect{
		MaxTotal: 50, SiteLinks: &one, MaxPerType: map[string]int{"sudph": 5},
	}}}

	autoconnMaxTotal, autoconnSiteLinks, autoconnMaxPerType = -1, -1, ""
	require.NoError(t, applyAutoconnectVars())
	require.Equal(t, 50, conf.Transport.Autoconnect.MaxTotal)
	require.Equal(t, 1, *conf.Transport.Autoconnect.SiteLinks)
	require.Equal(t, map[string]int{"sudph": 5}, conf.Transport.Autoconnect.MaxPerType)

	autoconnMaxTotal, autoconnSiteLinks, autoconnMaxPerType = 0, 0, "webrtc=10"
	require.NoError(t, applyAutoconnectVars())
	require.Equal(t, 0, conf.Transport.Autoconnect.MaxTotal)
	require.Equal(t, 0, *conf.Transport.Autoconnect.SiteLinks)
	require.Equal(t, map[string]int{"webrtc": 10}, conf.Transport.Autoconnect.MaxPerType)

	autoconnMaxPerType = "sudph"
	require.Error(t, applyAutoconnectVars())
}
