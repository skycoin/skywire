//go:build (js && wasm) || tinygo

// Package visor pkg/visor/rewards_ui_stub.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// rewardsUIHandler is nil in a browser. The reward UI reads the reward
// system's work directory and links gin, which js/wasm leaves out.
func rewardsUIHandler(log *logging.Logger, _ *visorconfig.RewardsConfig) http.Handler {
	log.Warn("The reward system UI is not available in a browser visor")
	return nil
}
