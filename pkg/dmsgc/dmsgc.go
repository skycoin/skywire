// Package dmsgc dmsg config and client
package dmsgc

import (
	"context"
	"net/http"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// DiscoveryConfig represents a single dmsg-discovery this client
// should register with. URL is the plain-HTTP endpoint; DmsgURL, when
// set, is the dmsg-HTTP endpoint used by dmsgfirst as the DMSG-side
// fallback target. PK, when set, identifies the discovery's own
// dmsg-server PK so dmsgfirst can dial it over DMSG before falling
// back to HTTP.
type DiscoveryConfig struct {
	URL     string        `json:"url"`
	DmsgURL string        `json:"dmsg_url,omitempty"`
	PK      cipher.PubKey `json:"public_key,omitempty"`
}

// DmsgConfig defines config for Dmsg network.
type DmsgConfig struct {
	// Configs is the list of dmsg-discoveries this client registers
	// with. When non-empty it supersedes Discovery / DiscoveryDmsg —
	// the legacy fields are kept so existing visor configs keep working
	// and so a single-discovery deployment doesn't need the verbose
	// multi-discovery shape.
	Configs              []DiscoveryConfig `json:"configs,omitempty"`
	Discovery            string            `json:"discovery"`
	DiscoveryDmsg        string            `json:"discovery_dmsg,omitempty"`
	SessionsCount        int               `json:"sessions_count"`
	Servers              []*disc.Entry     `json:"servers"`
	ConnectedServersType string            `json:"servers_type"`
	Protocol             string            `json:"protocol"`
	LANServers           []*disc.Entry     `json:"lan_servers,omitempty"` // Static LAN DMSG servers (tried first, auto-populated by hypervisor)
}

// NormalizedConfigs returns the discovery list this client should
// register with. When the modern Configs field is non-empty, it's
// returned as-is; otherwise a single-element list is synthesized
// from the legacy Discovery + DiscoveryDmsg fields. Returns nil
// when neither is set (deserves a hard error from the caller).
func (c *DmsgConfig) NormalizedConfigs() []DiscoveryConfig {
	if len(c.Configs) > 0 {
		return c.Configs
	}
	if c.Discovery == "" && c.DiscoveryDmsg == "" {
		return nil
	}
	return []DiscoveryConfig{{URL: c.Discovery, DmsgURL: c.DiscoveryDmsg}}
}

// lanPriorityDisc wraps a disc.APIClient to prepend LAN server entries
// to discovery results. LAN servers are tried first, with automatic
// fallback to public servers if the LAN server is unreachable.
type lanPriorityDisc struct {
	disc.APIClient
	lanEntries []*disc.Entry
}

func (d *lanPriorityDisc) AvailableServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AvailableServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

func (d *lanPriorityDisc) AllServers(ctx context.Context) ([]*disc.Entry, error) {
	entries, err := d.APIClient.AllServers(ctx)
	if err != nil {
		if len(d.lanEntries) > 0 {
			return d.lanEntries, nil
		}
		return nil, err
	}
	return append(d.lanEntries, entries...), nil
}

// New makes new dmsg client from configuration
func New(pk cipher.PubKey, sk cipher.SecKey, eb *appevent.Broadcaster, conf *DmsgConfig, httpC *http.Client, masterLogger *logging.MasterLogger) *dmsg.Client {
	dmsgConf := &dmsg.Config{
		MinSessions: conf.SessionsCount,
		Callbacks: &dmsg.ClientCallbacks{
			OnSessionDial: func(network, addr string) error {
				data := appevent.TCPDialData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPDial, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
				// @evanlinjin: An error is not returned here as this will cancel the session dial.
				return nil
			},
			OnSessionDisconnect: func(network, addr string, _ error) {
				data := appevent.TCPCloseData{RemoteNet: network, RemoteAddr: addr}
				event := appevent.NewEvent(appevent.TCPClose, data)
				_ = eb.Broadcast(context.Background(), event) //nolint:errcheck
			},
		},
		ConnectedServersType: conf.ConnectedServersType,
		Protocol:             conf.Protocol,
	}
	dmsgConf.ClientType = "visor"

	configs := conf.NormalizedConfigs()
	if len(configs) == 0 {
		// Preserve legacy zero-value behavior: a Discovery="" config
		// produced an HTTP client pointed at "" (effectively dead) but
		// the caller didn't explode. Mirror that here.
		configs = []DiscoveryConfig{{URL: conf.Discovery, DmsgURL: conf.DiscoveryDmsg}}
	}

	primary := disc.NewHTTP(configs[0].URL, httpC, masterLogger.PackageLogger("dmsgC:disc"))
	if len(conf.LANServers) > 0 {
		masterLogger.PackageLogger("dmsgC").Infof("Using %d LAN DMSG servers (tried first)", len(conf.LANServers))
		primary = &lanPriorityDisc{APIClient: primary, lanEntries: conf.LANServers}
	}

	dmsgC := dmsg.NewClient(pk, sk, primary, dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("dmsgC"))
	dmsgC.SetMasterLogger(masterLogger)
	// Attach extra discoveries so the visor publishes its client entry
	// to all configured discoveries. Plain HTTP at construction time;
	// the visor's init_dmsg path can later swap these for dmsgfirst-
	// wrapped clients once dmsgC has a usable session.
	for i := 1; i < len(configs); i++ {
		dmsgC.AddDiscovery(disc.NewHTTP(configs[i].URL, httpC, masterLogger.PackageLogger("dmsgC:disc")), configs[i].PK)
	}
	return dmsgC
}
