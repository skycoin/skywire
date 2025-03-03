//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

// AppState contains the struct for the json output of the `visor app ls` command
type AppState struct {
	App            string `json:"app"`
	Port           int    `json:"port"`
	AutoStart      bool   `json:"auto_start"`
	Status         string `json:"status"`
	DetailedStatus string `json:"detailed_status"`
}

// AppState contains the struct for the json output of the `route ls-rules` command
type RouteRule struct {
	ID          routing.RouteID `json:"id"`
	Type        string          `json:"type"`
	LocalPort   string          `json:"local_port,omitempty"`
	RemotePort  string          `json:"remote_port,omitempty"`
	RemotePK    string          `json:"remote_pk,omitempty"`
	NextRouteID string          `json:"next_route_id,omitempty"`
	NextTpID    string          `json:"next_transport_id,omitempty"`
	ExpireAt    time.Duration   `json:"expire-at"`
}

// RouteKey contains the struct for the json output of the `route add-rule`'s sub-commands
type RouteKey struct {
	RoutingRuleKey routing.RouteID `json:"routing_route_key"`
}

// VPNStatus contains the struct for the json output of the `vpn status`
type VPNStatus struct {
	Status string `json:"status"`
}

// VPNSart contains the struct for the json output of the `vpn start`
type VPNStart struct {
	CurrentIP string `json:"current_ip,omitempty"`
	AppError  string `json:"app_error,omitempty"`
}
