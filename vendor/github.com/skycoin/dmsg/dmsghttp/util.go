package dmsghttp

import (
	"context"
	"net/http"
	"time"

	"github.com/skycoin/dmsg/direct"
	"github.com/skycoin/dmsg/disc"

	"github.com/skycoin/skycoin/src/util/logging"
)

// GetServers is used to get all the available servers from the dmsg-discovery.
func GetServers(ctx context.Context, dmsgDisc string, log *logging.Logger) (entries []*disc.Entry) {
	dmsgclient := disc.NewHTTP(dmsgDisc, http.Client{})
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	for {
		servers, err := dmsgclient.AvailableServers(ctx)
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
func UpdateServers(ctx context.Context, dClient direct.APIClient, dmsgDisc string, log *logging.Logger) (entries []*disc.Entry) {
	dmsgclient := disc.NewHTTP(dmsgDisc, http.Client{})
	ticker := time.NewTicker(time.Second * 10)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			servers, err := dmsgclient.AvailableServers(ctx)
			if err != nil {
				log.WithError(err).Error("Error getting dmsg-servers.")
				break
			}
			for _, server := range servers {
				dClient.PostEntry(ctx, server) //nolint
			}
		}
	}
}
