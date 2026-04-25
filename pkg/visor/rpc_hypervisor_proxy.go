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

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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
				log.WithField("pk", pk.String()[:8]).Warn("HVListVisors: visor query timed out")
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

	return results, nil
}

// HVVisorSummary returns detailed info about a specific remote visor.
func (v *Visor) HVVisorSummary(pk cipher.PubKey) (*Summary, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if pk == v.conf.PK {
		return v.Summary()
	}
	conn, ok := v.hvInstance.visorConn(pk)
	if !ok {
		return nil, fmt.Errorf("visor %s not connected", pk.String()[:8])
	}
	return conn.API.Summary()
}

// hvAPI returns the API for the visor identified by pk. If pk matches the
// local visor it returns the local API; otherwise it looks up the remote
// connection through the hypervisor.
func (v *Visor) hvAPI(pk cipher.PubKey) (API, error) {
	if v.hvInstance == nil {
		return nil, fmt.Errorf("hypervisor not running")
	}
	if pk == v.conf.PK {
		return v, nil
	}
	conn, ok := v.hvInstance.visorConn(pk)
	if !ok {
		return nil, fmt.Errorf("visor %s not connected", pk.String()[:8])
	}
	return conn.API, nil
}

// HVStartApp starts an app on the visor identified by pk.
func (v *Visor) HVStartApp(pk cipher.PubKey, appName string) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.StartApp(appName)
}

// HVStopApp stops an app on the visor identified by pk.
func (v *Visor) HVStopApp(pk cipher.PubKey, appName string) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.StopApp(appName)
}

// HVSetMinHops sets the routing min_hops on the visor identified by pk.
func (v *Visor) HVSetMinHops(pk cipher.PubKey, hops uint16) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.SetMinHops(hops)
}

// HVSetRewardAddress sets the reward address on the visor identified by pk.
// Returns the resulting config string from the visor.
func (v *Visor) HVSetRewardAddress(pk cipher.PubKey, addr string) (string, error) {
	api, err := v.hvAPI(pk)
	if err != nil {
		return "", err
	}
	return api.SetRewardAddress(addr)
}

// HVRemoveTransport deletes a transport on the visor identified by pk.
func (v *Visor) HVRemoveTransport(pk cipher.PubKey, tid uuid.UUID) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.RemoveTransport(tid)
}

// HVRemoveRoutingRule deletes a routing rule on the visor identified by pk.
func (v *Visor) HVRemoveRoutingRule(pk cipher.PubKey, key routing.RouteID) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.RemoveRoutingRule(key)
}

// HVAddTransport creates a new transport on the visor identified by pk.
func (v *Visor) HVAddTransport(pk, remote cipher.PubKey, tpType, label string, timeout time.Duration) (*TransportSummary, error) {
	api, err := v.hvAPI(pk)
	if err != nil {
		return nil, err
	}
	if label == "" {
		label = string(transport.LabelUser)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return api.AddTransport(remote, tpType, timeout, label, false, false)
}

// HVSetPublicAutoconnect toggles public_autoconnect on the visor identified by pk.
func (v *Visor) HVSetPublicAutoconnect(pk cipher.PubKey, enable bool) error {
	api, err := v.hvAPI(pk)
	if err != nil {
		return err
	}
	return api.SetPublicAutoconnect(enable)
}
