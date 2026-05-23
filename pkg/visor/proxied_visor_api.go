// Package visor pkg/visor/proxied_visor_api.go
//
// proxiedVisorAPI lets the hypervisor's HTTP layer talk to a visor
// that isn't directly connected to it — instead reaching it through
// a sub-hypervisor that is. The hypervisor walks ProxiedVia from
// HVListVisors output, finds the sub-hypervisor that has the target
// visor in its own remoteVisors map, and constructs a
// proxiedVisorAPI that re-routes each VisorAPI method through the
// sub-hypervisor's HVxxx RPC method (using the target visor's PK
// as the first arg).
//
// The HVxxx surface on the sub-hypervisor's API is partial — only
// the ~30 most operator-facing methods have counterparts today.
// Other methods on proxiedVisorAPI return ErrProxyNotSupported via
// the embedded proxyDefaultAPI's stubs (generated from api.go's
// API interface). The hvui falls back to its existing offline-row
// rendering for fields it can't fetch.
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// ErrProxyNotSupported is returned by proxiedVisorAPI methods that
// don't have an HVxxx counterpart on the sub-hypervisor's API. The
// hypervisor's HTTP layer surfaces this back as a 5xx so the hvui
// can show "operation not supported for proxied visor" instead of a
// generic null/empty.
var ErrProxyNotSupported = errors.New("operation not supported on proxied visor")

// proxiedVisorAPI satisfies the API interface for a visor that is
// reachable only through a sub-hypervisor (ProxiedVia). Methods with
// HVxxx counterparts route through the sub-hypervisor's API; the
// rest fall through to proxyDefaultAPI's ErrProxyNotSupported stubs.
type proxiedVisorAPI struct {
	proxyDefaultAPI
	targetPK cipher.PubKey
	hvAPI    API
}

func newProxiedVisorAPI(targetPK cipher.PubKey, hvAPI API) *proxiedVisorAPI {
	return &proxiedVisorAPI{targetPK: targetPK, hvAPI: hvAPI}
}

// --- Methods with direct HVxxx counterparts ---

func (p *proxiedVisorAPI) Summary() (*Summary, error) {
	return p.hvAPI.HVVisorSummary(p.targetPK)
}

func (p *proxiedVisorAPI) Overview() (*Overview, error) {
	// HVVisorSummary returns a full Summary which includes Overview.
	// Re-projecting saves us a second round-trip and matches the
	// shape /api/visors/{pk} returns for direct visors.
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrProxyNotSupported
	}
	return s.Overview, nil
}

func (p *proxiedVisorAPI) Health() (*HealthInfo, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrProxyNotSupported
	}
	return s.Health, nil
}

func (p *proxiedVisorAPI) Uptime() (float64, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return 0, err
	}
	if s == nil {
		return 0, ErrProxyNotSupported
	}
	return s.Uptime, nil
}

func (p *proxiedVisorAPI) Apps() ([]*appserver.AppState, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil || s.Overview == nil {
		return nil, ErrProxyNotSupported
	}
	return s.Overview.Apps, nil
}

func (p *proxiedVisorAPI) Transports(_ []string, _ []cipher.PubKey, _ bool) ([]*TransportSummary, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil || s.Overview == nil {
		return nil, ErrProxyNotSupported
	}
	return s.Overview.Transports, nil
}

func (p *proxiedVisorAPI) RoutingRules() ([]routing.Rule, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrProxyNotSupported
	}
	out := make([]routing.Rule, 0, len(s.Routes))
	for _, r := range s.Routes {
		decoded, decodeErr := decodeRouteHex(r.Rule)
		if decodeErr != nil {
			continue
		}
		out = append(out, decoded)
	}
	return out, nil
}

func (p *proxiedVisorAPI) RouteGroups() ([]RouteGroupInfo, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrProxyNotSupported
	}
	return s.RouteGroups, nil
}

func (p *proxiedVisorAPI) GetMinHops() (uint16, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return 0, err
	}
	if s == nil {
		return 0, ErrProxyNotSupported
	}
	return s.MinHops, nil
}

func (p *proxiedVisorAPI) GetRewardAddress() (string, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return "", err
	}
	if s == nil {
		return "", ErrProxyNotSupported
	}
	return s.RewardAddress, nil
}

func (p *proxiedVisorAPI) IsHypervisorEnabled() bool {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil || s == nil {
		return false
	}
	return s.IsHypervisor
}

// --- Write-side methods with HVxxx counterparts ---

func (p *proxiedVisorAPI) StartApp(appName string) error {
	return p.hvAPI.HVStartApp(p.targetPK, appName)
}

func (p *proxiedVisorAPI) StopApp(appName string) error {
	return p.hvAPI.HVStopApp(p.targetPK, appName)
}

func (p *proxiedVisorAPI) SetAutoStart(appName string, autostart bool) error {
	return p.hvAPI.HVSetAutoStart(p.targetPK, appName, autostart)
}

func (p *proxiedVisorAPI) SetMinHops(n uint16) error {
	return p.hvAPI.HVSetMinHops(p.targetPK, n)
}

func (p *proxiedVisorAPI) SetMuxRoutes(n int) error {
	return p.hvAPI.HVSetMuxRoutes(p.targetPK, n)
}

func (p *proxiedVisorAPI) SetCalculateRoutes(enabled bool) error {
	return p.hvAPI.HVSetCalculateRoutes(p.targetPK, enabled)
}

func (p *proxiedVisorAPI) SetRewardAddress(addr string) (string, error) {
	return p.hvAPI.HVSetRewardAddress(p.targetPK, addr)
}

func (p *proxiedVisorAPI) AddTransport(remote cipher.PubKey, tpType string, timeout time.Duration, label string, _ bool, _ bool) (*TransportSummary, error) {
	return p.hvAPI.HVAddTransport(p.targetPK, remote, tpType, label, timeout)
}

func (p *proxiedVisorAPI) RemoveTransport(tid uuid.UUID) error {
	return p.hvAPI.HVRemoveTransport(p.targetPK, tid)
}

func (p *proxiedVisorAPI) RemoveRoutingRule(key routing.RouteID) error {
	return p.hvAPI.HVRemoveRoutingRule(p.targetPK, key)
}

func (p *proxiedVisorAPI) SetPublicAutoconnect(pAc bool) error {
	return p.hvAPI.HVSetPublicAutoconnect(p.targetPK, pAc)
}

func (p *proxiedVisorAPI) Reload() error {
	return p.hvAPI.HVReload(p.targetPK)
}

func (p *proxiedVisorAPI) Shutdown() error {
	return p.hvAPI.HVShutdown(p.targetPK)
}

func (p *proxiedVisorAPI) ServiceHealth() ([]ServiceHealthEntry, error) {
	return p.hvAPI.HVServiceHealth(p.targetPK)
}

func (p *proxiedVisorAPI) LogsSince(timestamp time.Time, appName string) ([]string, error) {
	return p.hvAPI.HVLogsSince(p.targetPK, timestamp, appName)
}

func (p *proxiedVisorAPI) DMSGServers() ([]DMSGServerInfo, error) {
	s, err := p.hvAPI.HVVisorSummary(p.targetPK)
	if err != nil {
		return nil, err
	}
	if s == nil {
		return nil, ErrProxyNotSupported
	}
	return s.DMSGServers, nil
}

// EmbeddedProxies / RegisterTCPPort / RegisterForwardedPort etc.
// reach the sub-hypervisor's HV methods directly.

func (p *proxiedVisorAPI) EmbeddedProxies() (*EmbeddedProxiesStatus, error) {
	return p.hvAPI.HVEmbeddedProxies(p.targetPK)
}

func (p *proxiedVisorAPI) SetEmbeddedProxyEnabled(kind string, enable bool) error {
	return p.hvAPI.HVSetEmbeddedProxyEnabled(p.targetPK, kind, enable)
}

func (p *proxiedVisorAPI) SetEmbeddedProxyUpstream(kind, addr string) error {
	return p.hvAPI.HVSetEmbeddedProxyUpstream(p.targetPK, kind, addr)
}

func (p *proxiedVisorAPI) ListTCPPorts() ([]int, error) {
	return p.hvAPI.HVListTCPPorts(p.targetPK)
}

func (p *proxiedVisorAPI) RegisterTCPPort(localPort int) error {
	return p.hvAPI.HVRegisterTCPPort(p.targetPK, localPort)
}

func (p *proxiedVisorAPI) DeregisterTCPPort(localPort int) error {
	return p.hvAPI.HVDeregisterTCPPort(p.targetPK, localPort)
}

func (p *proxiedVisorAPI) ListForwardedPorts() ([]ForwardedPort, error) {
	return p.hvAPI.HVListForwardedPorts(p.targetPK)
}

func (p *proxiedVisorAPI) RegisterForwardedPort(fp ForwardedPort) error {
	return p.hvAPI.HVRegisterForwardedPort(p.targetPK, fp)
}

func (p *proxiedVisorAPI) UpdateForwardedPort(fp ForwardedPort) error {
	return p.hvAPI.HVUpdateForwardedPort(p.targetPK, fp)
}

// decodeRouteHex is a tiny helper for RoutingRules — Summary carries
// routes as hex-encoded rules; we decode them back into the
// routing.Rule slice the API method returns. ignored:json keep
// imports happy for downstream packages that may need it.
func decodeRouteHex(hexStr string) (routing.Rule, error) {
	// The internal representation matches what visor.RoutingRules()
	// returns directly. Sub-hypervisor proxying is informational —
	// the operator drilling in mostly wants to see the rules, not
	// re-edit them per-byte. If a future need arises for bit-exact
	// round-trip, replace this with a hex.DecodeString.
	if hexStr == "" {
		return nil, errors.New("empty rule")
	}
	rule, err := hexDecodeRule(hexStr)
	if err != nil {
		return nil, err
	}
	return rule, nil
}

// hexDecodeRule turns a hex string back into a routing.Rule. Routing
// rules are []byte under the hood with parsing helpers that read
// the byte slice. encoding/hex's DecodeString gives us the bytes;
// routing.Rule is a type alias for []byte.
func hexDecodeRule(s string) (routing.Rule, error) {
	b, err := hexToBytes(s)
	if err != nil {
		return nil, err
	}
	return routing.Rule(b), nil
}

func hexToBytes(s string) ([]byte, error) {
	// Avoid pulling encoding/hex import shuffle: decode pairwise.
	if len(s)%2 != 0 {
		return nil, errors.New("odd-length hex")
	}
	out := make([]byte, len(s)/2)
	for i := 0; i < len(out); i++ {
		hi, err := hexNibble(s[2*i])
		if err != nil {
			return nil, err
		}
		lo, err := hexNibble(s[2*i+1])
		if err != nil {
			return nil, err
		}
		out[i] = (hi << 4) | lo
	}
	return out, nil
}

func hexNibble(c byte) (byte, error) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', nil
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, nil
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, nil
	}
	return 0, errors.New("not hex")
}

// Compile-time guard.
var _ API = (*proxiedVisorAPI)(nil)

// Suppress unused-import warnings on the doc-only imports.
var _ context.Context
var _ json.RawMessage
