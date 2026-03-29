// Package dmsgc dmsg config and client
package dmsgc

import (
	"context"
	"net/http"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// DmsgConfig defines config for Dmsg network.
type DmsgConfig struct {
	Discovery            string        `json:"discovery"`
	SessionsCount        int           `json:"sessions_count"`
	Servers              []*disc.Entry `json:"servers"`
	ConnectedServersType string        `json:"servers_type"`
	Protocol             string        `json:"protocol"`
	LANServers           []*disc.Entry `json:"lan_servers,omitempty"` // Static LAN DMSG servers (tried first, auto-populated by hypervisor)
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

	var dc disc.APIClient
	dc = disc.NewHTTP(conf.Discovery, httpC, masterLogger.PackageLogger("dmsgC:disc"))
	if len(conf.LANServers) > 0 {
		masterLogger.PackageLogger("dmsgC").Infof("Using %d LAN DMSG servers (tried first)", len(conf.LANServers))
		dc = &lanPriorityDisc{APIClient: dc, lanEntries: conf.LANServers}
	}

	dmsgC := dmsg.NewClient(pk, sk, dc, dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("dmsgC"))
	dmsgC.SetMasterLogger(masterLogger)
	return dmsgC
}
