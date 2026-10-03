// Package config pkg/serviceconfig/dmsgserver_test.go c4-net-discovery
package config

import (
	"strings"
	"testing"
)

// TestEmptyDmsgServerConfigHasNoAdvertisedAddress guards a copy-paste where
// PublicAddress, a host:port the server advertises, held the discovery URL.
func TestEmptyDmsgServerConfigHasNoAdvertisedAddress(t *testing.T) {
	cfg := EmptyDmsgServerConfig()
	if cfg.PublicAddress != "" {
		t.Errorf("PublicAddress = %q, want empty, as there is no global default for it", cfg.PublicAddress)
	}
	if strings.HasPrefix(cfg.PublicAddress, "http") {
		t.Errorf("PublicAddress = %q, which is a URL and not a host:port", cfg.PublicAddress)
	}
	if cfg.Discovery != PublicDmsgDiscovery {
		t.Errorf("Discovery = %q, want %q", cfg.Discovery, PublicDmsgDiscovery)
	}
}
