//go:build !linux

// Package commands cmd/apps/vpn-router/commands/meshgw_other.go c4-app-vpn
package commands

import (
	"context"
	"fmt"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// runMeshGatewayOnly is Linux-only (needs netfilter). On other platforms it
// returns a clear error so the app still builds everywhere.
func runMeshGatewayOnly(_ context.Context, _ meshgw.MeshDial, _ logging.FieldLogger) error {
	return fmt.Errorf("--mesh-gateway-only is only supported on Linux")
}
