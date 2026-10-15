package visorconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appserver"
)

// The "app <name>" pair has to lead the args whatever order the map gives.
func TestBatchArgsPutAppFirst(t *testing.T) {
	for i := 0; i < 20; i++ {
		conf := &Launcher{Apps: []appserver.AppConfig{{Name: "proxy-2"}}}
		_, err := applyBatchArgs(conf, "proxy-2", map[string]any{"--srv": "PK", "app": "skysocks-client", "--addr": ":1081", "--reconnect": true})
		require.NoError(t, err)
		require.Equal(t, []string{"app", "skysocks-client", "--addr", ":1081", "--reconnect", "--srv", "PK"}, conf.Apps[0].Args)
	}
}
