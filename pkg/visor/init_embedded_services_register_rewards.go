//go:build !mobile && !js && !tinygo

// Package visor pkg/visor/init_embedded_services_register_rewards.go c3-vis-core
package visor

import (
	_ "github.com/skycoin/skywire/pkg/services/rewards" // reward system
)
