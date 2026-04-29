// Package visor pkg/visor/rpc_hypervisor_proxy.go
//
// RPC methods that proxy through the hypervisor's DMSG connections
// to remote visors. These enable CLI/TUI access to remote visor data
// without needing direct transport connections.
package visor

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// HVVisorEntry is a summary of a remote visor connected to this hypervisor.
type HVVisorEntry struct {
	PK             cipher.PubKey `json:"pk"`
	Online         bool          `json:"online"`
	IsLocal        bool          `json:"is_local,omitempty"`
	Version        string        `json:"version,omitempty"`
	BuildTag       string        `json:"build_tag,omitempty"`
	Uptime         float64       `json:"uptime_seconds,omitempty"`
	LocalIP        string        `json:"local_ip,omitempty"`
	PublicIP       string        `json:"public_ip,omitempty"`
	CountryCode    string        `json:"country_code,omitempty"`
	IsSymmetricNAT bool          `json:"symmetric_nat,omitempty"`
	Transports     int           `json:"transports"`
	Apps           int           `json:"apps"`
	RewardAddress  string        `json:"reward_address,omitempty"`
	ConfigVersion  string        `json:"config_version,omitempty"`
	Error          string        `json:"error,omitempty"`
	// ProxiedVia is set when this entry was discovered through a connected
	// sub-hypervisor rather than a direct connection. The value is the PK
	// of the sub-hypervisor that proxies operations on this visor.
	ProxiedVia *cipher.PubKey `json:"proxied_via,omitempty"`
}

func populateEntryFromSummary(entry *HVVisorEntry, summary *Summary) {
	entry.Version = summary.Overview.BuildInfo.Version
	entry.BuildTag = summary.BuildTag
	entry.Uptime = summary.Uptime
	entry.LocalIP = summary.Overview.LocalIP
	entry.PublicIP = summary.Overview.PublicIP
	entry.CountryCode = summary.Overview.CountryCode
	entry.IsSymmetricNAT = summary.Overview.IsSymmetricNAT
	entry.Transports = len(summary.Overview.Transports)
	entry.Apps = len(summary.Overview.Apps)
	entry.ConfigVersion = summary.ConfigVersion
	entry.RewardAddress = summary.RewardAddress
}

// HVListVisors returns summaries of all visors connected to this hypervisor.
func (v *Visor) HVListVisors() ([]HVVisorEntry, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if !v.hvInstance.IsEnabled() {
		return nil, fmt.Errorf("hypervisor not enabled")
	}

	hv := v.hvInstance
	hv.mu.RLock()
	type remote struct {
		pk   cipher.PubKey
		conn Conn
	}
	remotes := make([]remote, 0, len(hv.remoteVisors))
	for pk, c := range hv.remoteVisors {
		remotes = append(remotes, remote{pk, c})
	}
	hv.mu.RUnlock()

	log := logging.MustGetLogger("hv_list_visors")

	// Query each visor in parallel with a per-visor timeout
	results := make([]HVVisorEntry, len(remotes))
	var wg sync.WaitGroup
	wg.Add(len(remotes))

	for i, e := range remotes {
		go func(idx int, pk cipher.PubKey, api API) {
			defer wg.Done()
			entry := HVVisorEntry{PK: pk, Online: true}

			done := make(chan struct{})
			go func() {
				defer close(done)
				summary, err := api.Summary()
				if err != nil {
					entry.Error = err.Error()
					return
				}
				populateEntryFromSummary(&entry, summary)
			}()

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				entry.Error = "timeout"
				log.WithField("pk", pk.String()).Warn("HVListVisors: visor query timed out")
			}
			results[idx] = entry
		}(i, e.pk, e.conn.API)
	}
	wg.Wait()

	// Prepend local visor
	if hv.visor != nil {
		localEntry := HVVisorEntry{PK: v.conf.PK, Online: true, IsLocal: true}
		if localSummary, err := v.Summary(); err == nil {
			populateEntryFromSummary(&localEntry, localSummary)
		}
		results = append([]HVVisorEntry{localEntry}, results...)
	}

	// Merge sub-hypervisor visors: for each direct remote that is itself a
	// hypervisor, query its HVListVisors and add any visors not already
	// represented in our list. Tagged with ProxiedVia so write actions can
	// route through the right hop. Cycle protection: skip any PK that is
	// already in our `results` (covers immediate H2↔H1 mutual loops).
	seen := make(map[cipher.PubKey]bool, len(results))
	for _, e := range results {
		seen[e.PK] = true
	}
	subResults := make([][]HVVisorEntry, len(remotes))
	subPKs := make([]cipher.PubKey, len(remotes))
	var subWg sync.WaitGroup
	subWg.Add(len(remotes))
	for i, e := range remotes {
		subPKs[i] = e.pk
		go func(idx int, hyperPK cipher.PubKey, api API) {
			defer subWg.Done()
			done := make(chan []HVVisorEntry, 1)
			go func() {
				sub, err := api.HVListVisors()
				if err != nil {
					done <- nil
					return
				}
				done <- sub
			}()
			select {
			case sub := <-done:
				subResults[idx] = sub
			case <-time.After(10 * time.Second):
				log.WithField("pk", hyperPK.String()).Warn("HVListVisors: sub-hypervisor query timed out")
			}
		}(i, e.pk, e.conn.API)
	}
	subWg.Wait()
	for idx, sub := range subResults {
		hyperPK := subPKs[idx]
		for _, entry := range sub {
			if seen[entry.PK] {
				continue
			}
			seen[entry.PK] = true
			entry.IsLocal = false
			pk := hyperPK
			entry.ProxiedVia = &pk
			results = append(results, entry)
		}
	}

	return results, nil
}

// HVVisorSummary returns detailed info about a specific remote visor.
func (v *Visor) HVVisorSummary(pk cipher.PubKey) (*Summary, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		if direct == API(v) {
			return v.Summary()
		}
		return direct.Summary()
	}
	return sub.HVVisorSummary(pk)
}

// hvDispatch finds how to reach pk from this hypervisor:
//   - direct != nil: pk is local or a directly-connected remote visor; call
//     `direct.SomeMethod(...)` (the regular API method, not HV-prefixed).
//   - sub != nil: pk is reachable through one of our connected sub-hypervisors;
//     call `sub.HVSomeMethod(pk, ...)` (recursing one hop deeper).
//
// Sub-hypervisor lookup queries each direct remote's HVListVisors with a 10s
// timeout. Cycle protection relies on the timeout: a mutual hypervisor pair
// will fail-fast with timeouts rather than recurse forever.
func (v *Visor) hvDispatch(pk cipher.PubKey) (direct API, sub API, err error) {
	if v.hvInstance == nil {
		return nil, nil, fmt.Errorf("hypervisor not running")
	}
	if pk == v.conf.PK {
		return v, nil, nil
	}
	if conn, ok := v.hvInstance.visorConn(pk); ok {
		return conn.API, nil, nil
	}
	// Walk sub-hypervisors looking for pk.
	v.hvInstance.mu.RLock()
	type remote struct {
		pk   cipher.PubKey
		conn Conn
	}
	remotes := make([]remote, 0, len(v.hvInstance.remoteVisors))
	for rpk, c := range v.hvInstance.remoteVisors {
		remotes = append(remotes, remote{rpk, c})
	}
	v.hvInstance.mu.RUnlock()

	for _, e := range remotes {
		listDone := make(chan []HVVisorEntry, 1)
		go func(api API) {
			out, err := api.HVListVisors()
			if err != nil {
				listDone <- nil
				return
			}
			listDone <- out
		}(e.conn.API)
		select {
		case sub := <-listDone:
			for _, entry := range sub {
				if entry.PK == pk {
					return nil, e.conn.API, nil
				}
			}
		case <-time.After(10 * time.Second):
			// Skip this sub-hypervisor; it's slow or in a cycle.
		}
	}
	return nil, nil, fmt.Errorf("visor %s not reachable", pk.String())
}

// HVStartApp starts an app on the visor identified by pk.
func (v *Visor) HVStartApp(pk cipher.PubKey, appName string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.StartApp(appName)
	}
	return sub.HVStartApp(pk, appName)
}

// HVStopApp stops an app on the visor identified by pk.
func (v *Visor) HVStopApp(pk cipher.PubKey, appName string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.StopApp(appName)
	}
	return sub.HVStopApp(pk, appName)
}

// HVSetMinHops sets the routing min_hops on the visor identified by pk.
func (v *Visor) HVSetMinHops(pk cipher.PubKey, hops uint16) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetMinHops(hops)
	}
	return sub.HVSetMinHops(pk, hops)
}

// HVSetRewardAddress sets the reward address on the visor identified by pk.
// Returns the resulting config string from the visor.
func (v *Visor) HVSetRewardAddress(pk cipher.PubKey, addr string) (string, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return "", err
	}
	if direct != nil {
		return direct.SetRewardAddress(addr)
	}
	return sub.HVSetRewardAddress(pk, addr)
}

// HVRemoveTransport deletes a transport on the visor identified by pk.
func (v *Visor) HVRemoveTransport(pk cipher.PubKey, tid uuid.UUID) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RemoveTransport(tid)
	}
	return sub.HVRemoveTransport(pk, tid)
}

// HVRemoveRoutingRule deletes a routing rule on the visor identified by pk.
func (v *Visor) HVRemoveRoutingRule(pk cipher.PubKey, key routing.RouteID) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RemoveRoutingRule(key)
	}
	return sub.HVRemoveRoutingRule(pk, key)
}

// HVAddTransport creates a new transport on the visor identified by pk.
func (v *Visor) HVAddTransport(pk, remote cipher.PubKey, tpType, label string, timeout time.Duration) (*TransportSummary, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = string(transport.LabelUser)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if direct != nil {
		return direct.AddTransport(remote, tpType, timeout, label, false, false)
	}
	return sub.HVAddTransport(pk, remote, tpType, label, timeout)
}

// HVSetPublicAutoconnect toggles public_autoconnect on the visor identified by pk.
func (v *Visor) HVSetPublicAutoconnect(pk cipher.PubKey, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetPublicAutoconnect(enable)
	}
	return sub.HVSetPublicAutoconnect(pk, enable)
}

// HVSetMuxRoutes sets the mux_routes count on the visor identified by pk.
func (v *Visor) HVSetMuxRoutes(pk cipher.PubKey, n int) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetMuxRoutes(n)
	}
	return sub.HVSetMuxRoutes(pk, n)
}

// HVSetCalculateRoutes toggles calculate_routes on the visor identified by pk.
func (v *Visor) HVSetCalculateRoutes(pk cipher.PubKey, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetCalculateRoutes(enable)
	}
	return sub.HVSetCalculateRoutes(pk, enable)
}

// HVReload reloads the visor identified by pk.
func (v *Visor) HVReload(pk cipher.PubKey) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.Reload()
	}
	return sub.HVReload(pk)
}

// HVShutdown shuts down the visor identified by pk.
func (v *Visor) HVShutdown(pk cipher.PubKey) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.Shutdown()
	}
	return sub.HVShutdown(pk)
}

// HVServiceHealth returns deployment service health entries for the visor identified by pk.
func (v *Visor) HVServiceHealth(pk cipher.PubKey) ([]ServiceHealthEntry, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ServiceHealth()
	}
	return sub.HVServiceHealth(pk)
}

// HVDmsgConnectAll triggers a one-shot connect-all on the visor identified by pk.
func (v *Visor) HVDmsgConnectAll(pk cipher.PubKey) (*DmsgConnectAllResult, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.DmsgConnectAll()
	}
	return sub.HVDmsgConnectAll(pk)
}

// HVSetDmsgSessionsCount persists the dmsg sessions_count and triggers connect-all on the visor identified by pk.
func (v *Visor) HVSetDmsgSessionsCount(pk cipher.PubKey, count int) (*DmsgConnectAllResult, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.SetDmsgSessionsCount(count)
	}
	return sub.HVSetDmsgSessionsCount(pk, count)
}

// HVLogsSince returns app logs since the given timestamp on the visor identified by pk.
func (v *Visor) HVLogsSince(pk cipher.PubKey, since time.Time, appName string) ([]string, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.LogsSince(since, appName)
	}
	return sub.HVLogsSince(pk, since, appName)
}

// HVSetAutoStart toggles autostart for an app on the visor identified by pk.
func (v *Visor) HVSetAutoStart(pk cipher.PubKey, appName string, autostart bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetAutoStart(appName, autostart)
	}
	return sub.HVSetAutoStart(pk, appName, autostart)
}

// HVEmbeddedProxies returns embedded resolving proxy status on the visor identified by pk.
func (v *Visor) HVEmbeddedProxies(pk cipher.PubKey) (*EmbeddedProxiesStatus, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.EmbeddedProxies()
	}
	return sub.HVEmbeddedProxies(pk)
}

// HVSetEmbeddedProxyEnabled enables/disables an embedded proxy ("dmsg" or "skynet") on the visor identified by pk.
func (v *Visor) HVSetEmbeddedProxyEnabled(pk cipher.PubKey, kind string, enable bool) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetEmbeddedProxyEnabled(kind, enable)
	}
	return sub.HVSetEmbeddedProxyEnabled(pk, kind, enable)
}

// HVSetEmbeddedProxyUpstream sets the SOCKS5 upstream for an embedded proxy on the visor identified by pk.
func (v *Visor) HVSetEmbeddedProxyUpstream(pk cipher.PubKey, kind, addr string) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.SetEmbeddedProxyUpstream(kind, addr)
	}
	return sub.HVSetEmbeddedProxyUpstream(pk, kind, addr)
}

// HVListTCPPorts returns the registered skynet TCP ports on the visor identified by pk.
func (v *Visor) HVListTCPPorts(pk cipher.PubKey) ([]int, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ListTCPPorts()
	}
	return sub.HVListTCPPorts(pk)
}

// HVRegisterTCPPort registers a skynet TCP port on the visor identified by pk.
func (v *Visor) HVRegisterTCPPort(pk cipher.PubKey, port int) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RegisterTCPPort(port)
	}
	return sub.HVRegisterTCPPort(pk, port)
}

// HVDeregisterTCPPort deregisters a skynet TCP port on the visor identified by pk.
func (v *Visor) HVDeregisterTCPPort(pk cipher.PubKey, port int) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.DeregisterTCPPort(port)
	}
	return sub.HVDeregisterTCPPort(pk, port)
}

// HVListForwardedPorts returns the configured forwarded ports on the visor identified by pk.
func (v *Visor) HVListForwardedPorts(pk cipher.PubKey) ([]ForwardedPort, error) {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return nil, err
	}
	if direct != nil {
		return direct.ListForwardedPorts()
	}
	return sub.HVListForwardedPorts(pk)
}

// HVRegisterForwardedPort registers a forwarded port on the visor identified by pk.
func (v *Visor) HVRegisterForwardedPort(pk cipher.PubKey, p ForwardedPort) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.RegisterForwardedPort(p)
	}
	return sub.HVRegisterForwardedPort(pk, p)
}

// HVUpdateForwardedPort updates a forwarded port on the visor identified by pk.
func (v *Visor) HVUpdateForwardedPort(pk cipher.PubKey, p ForwardedPort) error {
	direct, sub, err := v.hvDispatch(pk)
	if err != nil {
		return err
	}
	if direct != nil {
		return direct.UpdateForwardedPort(p)
	}
	return sub.HVUpdateForwardedPort(pk, p)
}
