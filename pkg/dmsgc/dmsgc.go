// Package dmsgc dmsg config and client
package dmsgc

import (
	"context"
	"net/http"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/app/appevent"
)

// DmsgConfig defines config for Dmsg network.
type DmsgConfig struct {
	Discovery     string        `json:"discovery"`
	SessionsCount int           `json:"sessions_count"`
	Servers       []*disc.Entry `json:"servers"`
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
	}
	dmsgC := dmsg.NewClient(pk, sk, disc.NewHTTP(conf.Discovery, httpC, masterLogger.PackageLogger("dmsgC:disc")), dmsgConf)
	dmsgC.SetLogger(masterLogger.PackageLogger("dmsgC"))
	dmsgC.SetMasterLogger(masterLogger)
	return dmsgC
}
