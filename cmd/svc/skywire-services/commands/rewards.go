//go:build !(js && wasm) && !tinygo

// Package commands cmd/svc/skywire-services/commands/rewards.go c4-net-discovery
package commands

import (
	_ "github.com/skycoin/skywire/pkg/services/rewards" // reward system
)
