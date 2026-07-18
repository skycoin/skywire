//go:build !linux
// +build !linux

// Package vpn pkg/vpn/router_other.go c4-app-vpn
package vpn

import (
	"context"
	"fmt"
	"runtime"

	"github.com/sirupsen/logrus"
)

// Router is a stub on non-Linux platforms — the VPN gateway/AP role relies on
// hostapd, dnsmasq, iptables and Linux IP forwarding.
type Router struct{}

// NewRouter reports that the VPN router is unsupported on this platform. The
// config is still validated so misconfiguration surfaces the same way.
func NewRouter(cfg RouterConfig, _ logrus.FieldLogger) (*Router, error) {
	if err := cfg.withDefaults().validate(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("vpn-router: the VPN gateway/AP role is only supported on Linux (this is %s)", runtime.GOOS)
}

// Run never runs — NewRouter already errored on non-Linux.
func (r *Router) Run(_ context.Context) error {
	return fmt.Errorf("vpn-router: unsupported on %s", runtime.GOOS)
}
