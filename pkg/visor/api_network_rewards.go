//go:build !mobile

// Package visor pkg/visor/api_network_rewards.go c3-vis-core
package visor

import (
	"net/http"

	clirewardsserver "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/server"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// rewardsWebsiteHandler builds the reward system UI that refreshWebsiteHandler
// mounts on the dmsghttp logserver's port 80. Desktop-only: the rewards
// server pulls the TPD store and VictoriaMetrics, which the mobile build
// cannot compile for iOS (api_network_rewards_mobile.go).
func rewardsWebsiteHandler(rw *visorconfig.RewardsConfig) http.Handler {
	return clirewardsserver.ConfigureAndBuild(clirewardsserver.RewardConfig{
		WorkDir:         rw.WorkDir,
		WhitelistPKs:    rw.Whitelist,
		CanonicalDomain: rw.CanonicalDomain,
		SkycoinNode:     rw.SkycoinNode,
		LoginNode:       rw.LoginNode,
		DisableTpVizAPI: true,
	})
}
