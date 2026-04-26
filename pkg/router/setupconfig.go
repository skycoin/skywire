// Package router pkg/router/
package router

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsgc"
)

// Various timeouts for setup node.
const (
	RequestTimeout = time.Second * 60
	ReadTimeout    = time.Second * 30
)

// SetupConfig defines configuration parameters for setup Node.
type SetupConfig struct {
	PK                 cipher.PubKey    `json:"public_key"`
	SK                 cipher.SecKey    `json:"secret_key"`
	Dmsg               dmsgc.DmsgConfig `json:"dmsg"`
	TransportDiscovery string           `json:"transport_discovery"`
	LogLevel           string           `json:"log_level"`

	// Cascade configures the cascade route setup protocol.
	// When set, the RSN attempts to install routing rules via transport-level
	// cascade (route ID 0) before falling back to DMSG-based setup.
	Cascade *CascadeConfig `json:"cascade,omitempty"`

	// Transport configures the RSN's own transport manager.
	// When set, the RSN creates STCPR transports with label "setup" and
	// uses them for cascade route setup.
	Transport *SetupTransportConfig `json:"transport,omitempty"`
}

// SetupTransportConfig configures transport connectivity for the RSN.
type SetupTransportConfig struct {
	// STCPRAddr is the listen address for STCPR (e.g. ":7780").
	STCPRAddr string `json:"stcpr_addr,omitempty"`
	// AddressResolver is the AR URL. Empty = no AR registration (RSN-only outbound).
	AddressResolver string `json:"address_resolver,omitempty"`
	// MaxTransports limits the number of transports the RSN maintains.
	MaxTransports int `json:"max_transports,omitempty"`
	// DefaultLabel is the transport label (default: "setup").
	DefaultLabel string `json:"label,omitempty"`
}

// CascadeConfig configures the cascade route setup protocol.
type CascadeConfig struct {
	// ReserveTimeout is the per-hop timeout for the reserve phase.
	ReserveTimeout time.Duration `json:"reserve_timeout,omitempty"`
	// InstallTimeout is the per-hop timeout for the install phase.
	InstallTimeout time.Duration `json:"install_timeout,omitempty"`
	// SessionTTL is how long to keep session state between phases.
	SessionTTL time.Duration `json:"session_ttl,omitempty"`
}

// SetCascadeDefaults fills in zero cascade config values.
func (cc *CascadeConfig) SetCascadeDefaults() {
	if cc.ReserveTimeout <= 0 {
		cc.ReserveTimeout = 10 * time.Second
	}
	if cc.InstallTimeout <= 0 {
		cc.InstallTimeout = 10 * time.Second
	}
	if cc.SessionTTL <= 0 {
		cc.SessionTTL = 30 * time.Second
	}
}
