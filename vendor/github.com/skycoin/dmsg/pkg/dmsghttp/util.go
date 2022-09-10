package dmsghttp

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

// GetServers is used to get all the available servers from the dmsg-discovery.
func GetServers(ctx context.Context, dmsgDisc string, log *logging.Logger) (entries []*disc.Entry) {
	dmsgclient := disc.NewHTTP(dmsgDisc, &http.Client{}, log)
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	for {
		servers, err := dmsgclient.AllServers(ctx)
		if err != nil {
			log.WithError(err).Fatal("Error getting dmsg-servers.")
		}
		if len(servers) > 0 {
			return servers
		}
		log.Warn("No dmsg-servers found, trying again in 1 minute.")
		select {
		case <-ctx.Done():
			return []*disc.Entry{}
		case <-ticker.C:
			GetServers(ctx, dmsgDisc, log)
		}
	}
}

// UpdateServers is used to update the servers in the direct client.
func UpdateServers(ctx context.Context, dClient disc.APIClient, dmsgDisc string, dmsgC *dmsg.Client, log *logging.Logger) (entries []*disc.Entry) {
	dmsgclient := disc.NewHTTP(dmsgDisc, &http.Client{}, log)
	ticker := time.NewTicker(time.Minute * 10)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers, err := dmsgclient.AllServers(ctx)
			if err != nil {
				log.WithError(err).Error("Error getting dmsg-servers.")
				break
			}
			log.Debugf("Servers found : %v.", len(servers))
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint
				err := dmsgC.EnsureSession(ctx, server)
				if err != nil {
					log.WithField("remote_pk", server.Static).WithError(err).Warn("Failed to establish session.")
				}
			}
		}
	}
}
