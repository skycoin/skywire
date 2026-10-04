//go:build mobile && !(js && wasm)

// Package visor pkg/visor/rewards_ui_mobile.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// rewardsUIHandler is nil in the mobile build. The reward UI server pulls the
// TPD store and VictoriaMetrics, which do not compile for iOS.
func rewardsUIHandler(log *logging.Logger, _ *visorconfig.RewardsConfig) http.Handler {
	log.Warn("rewards.enable is set but this build has no reward system UI")
	return nil
}
