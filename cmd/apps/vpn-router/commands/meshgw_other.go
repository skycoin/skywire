//go:build !linux
// +build !linux

// Package commands cmd/apps/vpn-router/commands/meshgw_other.go c4-app-vpn
package commands

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// runMeshGatewayOnly is Linux-only (needs netfilter). On other platforms it
// returns a clear error so the app still builds everywhere.
func runMeshGatewayOnly(_ context.Context, _ meshgw.MeshDial, _ logrus.FieldLogger) error {
	return fmt.Errorf("--mesh-gateway-only is only supported on Linux")
}
