//go:build !(js && wasm) && !mobile

// Package visor pkg/visor/rewards_ui_native.go c3-vis-core
package visor

import (
	"net/http"

	clirewardsserver "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/server"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// rewardsUIHandler builds the reward system UI that the visor serves on port 80.
func rewardsUIHandler(_ *logging.Logger, rw *visorconfig.RewardsConfig) http.Handler {
	return clirewardsserver.ConfigureAndBuild(clirewardsserver.RewardConfig{
		WorkDir:         rw.WorkDir,
		WhitelistPKs:    rw.Whitelist,
		CanonicalDomain: rw.CanonicalDomain,
		SkycoinNode:     rw.SkycoinNode,
		LoginNode:       rw.LoginNode,
		DisableTpVizAPI: true,
	})
}
