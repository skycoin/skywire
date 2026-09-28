//go:build mobile

// Package visor pkg/visor/api_network_rewards_mobile.go c3-vis-core
package visor

import (
	"net/http"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// rewardsWebsiteHandler: the mobile build has no reward system UI (see
// api_network_rewards.go); nil makes refreshWebsiteHandler fall through to
// the forwarded-port and default modes.
func rewardsWebsiteHandler(_ *visorconfig.RewardsConfig) http.Handler {
	return nil
}
