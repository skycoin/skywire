package stunclient

import (
	"github.com/ccding/go-stun/stun"
	"github.com/skycoin/skycoin/src/util/logging"
)

// Details represents the visors public network details.
type Details struct {
	PublicIP *stun.Host
	NATType  stun.NATType
}

// GetDetails provides STUN details
func GetDetails(stunServers []string, log *logging.Logger) *Details {

	var nat stun.NATType
	var host *stun.Host
	var err error
	for _, stunServer := range stunServers {
		nC := stun.NewClient()
		nC.SetServerAddr(stunServer)

		nat, host, err = nC.Discover()
		if err != nil {
			log.Warn(err)
		}

		switch nat {
		case stun.NATError, stun.NATUnknown, stun.NATBlocked:
			log.Warn(nat.String())
			// incase we fail to connect to all the STUN servers give a warning
			if stunServer == stunServers[len(stunServers)-1] {
				log.Warn("All STUN servers offline")
			}
		default:
			log.Info(nat.String())
			log.Info(host.String())
			return &Details{
				PublicIP: host,
				NATType:  nat,
			}
		}
	}

	return &Details{
		PublicIP: host,
		NATType:  nat,
	}
}
