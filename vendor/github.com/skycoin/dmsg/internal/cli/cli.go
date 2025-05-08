// Package cli internal/cli/cli.go
package cli

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/direct"
	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
)

// StartDmsg starts dmsg returns a dmsg client for the given dmsg discovery
func StartDmsg(ctx context.Context, dmsgLogger *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, dmsgDisc string, dmsgSessions int) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, httpClient, dmsgLogger), &dmsg.Config{MinSessions: dmsgSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		dmsgLogger.WithError(err).Debug("Disconnected from dmsg network.\n")
		log.Println()
	}
	dmsgLogger.WithField("dmsg_disc", dmsgDisc).Debug("Connecting to dmsg network...\n")
	dmsgLogger.WithField("public_key", pk.String()).Debug("\n")
	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dmsgLogger.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

func StartDmsgDirect(ctx context.Context, dmsgLogger *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, _ string, dmsgSessions int, destination string) (dmsgC *dmsg.Client, stop func(), err error) { //nolint:all
	var servers []*disc.Entry
	for i := range dmsg.Prod.DmsgServers {
		servers = append(servers, &dmsg.Prod.DmsgServers[i])
	}
	if len(servers) == 0 {
		return nil, nil, fmt.Errorf("no dmsg servers configured")
	}

	var keys cipher.PubKeys

	keys = append(keys, pk)
	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, dmsgLogger)

	// Fix `dmsg error 102 - entry is not of client in discovery` error
	destinationPk := cipher.PubKey{}
	if err = destinationPk.UnmarshalText([]byte(destination)); err != nil {
		return nil, nil, fmt.Errorf("destination address (pk) is wrong")
	}
	var delegatedServers []cipher.PubKey
	for _, server := range servers {
		delegatedServers = append(delegatedServers, server.Static)
	}
	clientEntry := &disc.Entry{
		Client: &disc.Client{
			DelegatedServers: delegatedServers,
		},
		Static: destinationPk,
	}
	err = dClient.PostEntry(ctx, clientEntry)
	if err != nil {
		return nil, nil, fmt.Errorf("an error occurred during setup dClient for httpClient of destination")
	}

	dmsgConfig := dmsg.DefaultConfig()
	dmsgConfig.MinSessions = dmsgSessions
	return direct.StartDmsg(ctx, dmsgLogger, pk, sk, dClient, dmsgConfig)
}
