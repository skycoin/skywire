// Package visor pkg/visor/autoconnect.go
package visor

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/bitfield/script"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// PublicServiceDelay defines the interval for checking service discovery and adding transports to public visors.
	PublicServiceDelay = 300 * time.Second

	fetchServicesDelay = 10 * time.Second
)

// ConnectFn provides a way to connect to remote service
type ConnectFn func(context.Context, cipher.PubKey) error

// Autoconnector continuously tries to connect to services
type Autoconnector interface {
	Run(context.Context, *Visor) error
}

type autoconnector struct {
	client   *servicedisc.HTTPClient
	maxConns int
	log      *logging.Logger
	tm       *transport.Manager
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf servicedisc.Config, maxConns int, tm *transport.Manager, httpC *http.Client, clientPublicIP string,
	log *logging.Logger, mLog *logging.MasterLogger) Autoconnector {
	connector := &autoconnector{}
	connector.client = servicedisc.NewClient(log, mLog, conf, httpC, clientPublicIP)
	connector.maxConns = maxConns
	connector.log = log
	connector.tm = tm
	return connector
}

// Run implements Autoconnector interface
func (a *autoconnector) Run(ctx context.Context, v *Visor) (err error) {
	// Wait for a random interval between 0 and 5 minutes before starting public autoconnect.
	// Prevents a cluster of nodes which was switched on at the same time
	// from producing concurrent, synchronous requests to SD and subsequent connection attempts to public visors
	const maxDelaySeconds = 5 * 60 // 5 minutes

	randomDelaySeconds, err := randInt(0, maxDelaySeconds)
	if err != nil {
		a.log.WithError(err).Warn("Failed to generate secure random delay; falling back to 0")
		randomDelaySeconds = 0
	}
	randomDelay := time.Duration(randomDelaySeconds) * time.Second
	a.log.Debugln("Waiting for a random interval before starting public autoconnect:", randomDelay)

	select {
	case <-ctx.Done():
		return context.Canceled
	case <-time.After(randomDelay):
	}

	visorIsPublic := checkVisorIsPublic(v)

	publicServiceTicket := time.NewTicker(PublicServiceDelay)

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case <-publicServiceTicket.C:

			// limit maximum number of autoconnect transports
			// Note: this doesn't account for user established transports
			if len(a.tm.GetTransportsByLabel(transport.LabelAutomatic)) >= a.maxConns {
				a.log.Debugln("transport limit reached:", a.maxConns)
				continue
			}

			a.log.Infoln("Fetching public visors")
			addrs, err := a.fetchPubAddresses(ctx)
			if err != nil {
				a.log.Errorf("Cannot fetch public visors from service discovery: %s", err)
				v.isServicesHealthy.unset()
				continue
			}
			v.isServicesHealthy.set()

			if len(addrs) == 0 {
				a.log.Debugln("no public visors found")
				continue
			}

			a.log.WithField("public visors", len(addrs)).Debugln("Found public visors")

			absent := a.filterDuplicates(addrs, a.tm.GetTransportsByLabel(transport.LabelAutomatic))

			// Get keys available for SUDPH transport
			sudphKeys, err := v.arClient.TransportsType(ctx, types.SUDPH)
			if err != nil {
				a.log.WithError(err).Warn("could not fetch SUDPH transport keys")
				sudphKeys = map[cipher.PubKey][]string{}
			}

			// Get keys available for STCPR transport
			stcprKeys, err := v.arClient.TransportsType(ctx, types.STCPR)
			if err != nil {
				a.log.WithError(err).Warn("could not fetch STCPR transport keys")
				stcprKeys = map[cipher.PubKey][]string{}
			}

			// auto-transport logic - for visors running as public visors - should only attempt to connect stcpr transports to other public visors
			var absent2 []cipher.PubKey
			if !visorIsPublic {
				// find keys that have transports to public visors
				absentSet := make(map[cipher.PubKey]struct{}, len(addrs))
				for _, pk := range addrs {
					absentSet[pk] = struct{}{}
				}

				absent2Set := make(map[cipher.PubKey]struct{})

				for _, pk := range addrs {
					//check transport discovery for transport entries containing this edge key
					entries, err := v.DiscoverTransportsByPK(pk)
					if err != nil {
						v.isServicesHealthy.unset()
						a.log.WithError(err).Warn("cannot connect to transport discovery service")
						continue
					}
					v.isServicesHealthy.set()

					var entryKeys []cipher.PubKey
					seen := make(map[cipher.PubKey]struct{})
					for _, entry := range entries {
						for _, edge := range entry.Edges {
							if edge != v.conf.PK {
								if _, ok := seen[edge]; !ok {
									entryKeys = append(entryKeys, edge)
									seen[edge] = struct{}{}
								}
							}
						}
					}

					// exclude keys for which the visor already has transports
					filtered := a.filterDuplicates(entryKeys, a.tm.GetTransportsByLabel(transport.LabelAutomatic))
					for _, newPK := range filtered {
						if _, seen := absent2Set[newPK]; seen {
							continue // already in absent2
						}
						if _, isAbsent := absentSet[newPK]; isAbsent {
							continue // appears in original absent list
						}
						absent2Set[newPK] = struct{}{}
						absent2 = append(absent2, newPK)
					}
				}

				absent2 = a.filterDuplicates(absent2, a.tm.GetTransportsByLabel(transport.LabelAutomatic))

				// remove keys that are not available for sudph transports from absent2 slice
				filtered := absent2[:0]
				for _, pk := range absent2 {
					if _, ok := sudphKeys[pk]; ok {
						filtered = append(filtered, pk)
					}
				}
				absent2 = filtered

				// Ensure no duplicates in absent and absent2 and ensure no key appears in both lists
				absentSet = make(map[cipher.PubKey]struct{}, len(absent))
				uniqueAbsent := absent[:0]
				for _, pk := range absent {
					if _, seen := absentSet[pk]; !seen {
						absentSet[pk] = struct{}{}
						uniqueAbsent = append(uniqueAbsent, pk)
					}
				}
				absent = uniqueAbsent

				absent2Set = make(map[cipher.PubKey]struct{}, len(absent2))
				uniqueAbsent2 := absent2[:0]
				for _, pk := range absent2 {
					if _, seen := absent2Set[pk]; !seen {
						if _, inAbsent := absentSet[pk]; !inAbsent {
							absent2Set[pk] = struct{}{}
							uniqueAbsent2 = append(uniqueAbsent2, pk)
						}
					}
				}
				absent2 = uniqueAbsent2

			}

			absent = append(absent, absent2...)
			a.log.WithField("total", len(absent)).Debugln("Found visors to connect to")

			//attempt to establish transports to the keys in random order for 5 minutes
			attemptDeadline := time.Now().Add(5 * time.Minute)
			for _, pk := range shufflePubKeys(absent) {
				// limit maximum number of autoconnect transports
				if len(a.tm.GetTransportsByLabel(transport.LabelAutomatic)) >= a.maxConns {
					a.log.Debugln("transport limit reached:", a.maxConns)
					break
				}
				if time.Now().After(attemptDeadline) {
					a.log.Debugln("Refreshing list of keys for public autoconnect")
					break
				}
				//don't make self-transports
				if pk == v.conf.PK {
					continue
				}
				//don't attempt to make transports to offline visors
				vUptimeData, err := v.uptimeTracker.FetchUptimes(ctx, pk.String())
				if err != nil {
					a.log.WithField("pk", pk).WithError(err).Debugln("Failed to check remote visor online status")
					v.isServicesHealthy.unset()
					continue
				}
				v.isServicesHealthy.set()
				vOnline, err := script.Echo(string(vUptimeData)).JQ(`.[].on`).String()
				if err != nil {
					a.log.WithField("pk", pk).WithError(err).Debugln("No uptime tracker data for remote visor")
					continue
				}
				if strings.TrimSuffix(vOnline, "\n") != "true" {
					a.log.WithField("pk", pk).Debugln("Aborting connection attempt to offline visor")
					continue
				}

				// limit transports if the remote visor already has maxconns total transports
				// Check the transport discovery, and skip if number of entries is >= a.maxConns
				entries, err := v.DiscoverTransportsByPK(pk)
				if err != nil {
					v.isServicesHealthy.unset()
					a.log.WithField("pk", pk).WithError(err).Warn("Failed to discover transports for remote visor")
					continue
				}
				v.isServicesHealthy.set()

				if len(entries) >= a.maxConns {
					a.log.WithField("pk", pk).WithField("count", len(entries)).Debugln("Remote visor has reached or exceeded max connections, skipping")
					continue
				}

				// Determine network type and attempt to establish transport using that type
				var netType types.Type
				if _, ok := sudphKeys[pk]; ok {
					netType = types.SUDPH
				} else if _, ok := stcprKeys[pk]; ok {
					netType = types.STCPR
				} else {
					a.log.WithField("pk", pk).Debugln("No supported network type found for visor")
					continue
				}

				a.log.WithField("pk", pk).WithField("type", netType).Debugln("Trying to add transport to public visor")

				logger := a.log.WithField("pk", pk).WithField("type", string(netType))
				if err = a.tryEstablishTransport(ctx, pk, netType, logger); err != nil {
					logger.WithError(err).Warnln("Failed to add transport to visor")
					continue
				}
			}
		}
	}
}

func shufflePubKeys(keys []cipher.PubKey) []cipher.PubKey {
	n := len(keys)
	for i := n - 1; i > 0; i-- {
		jBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		keys[i], keys[j] = keys[j], keys[i]
	}
	return keys
}

// tryEstablishTransport attempts to establish a transport of the specified type to the given public key, and return error.
func (a *autoconnector) tryEstablishTransport(ctx context.Context, pk cipher.PubKey, netType types.Type, logger *logrus.Entry) error {
	if _, err := a.tm.SaveTransport(ctx, pk, netType, transport.LabelAutomatic); err != nil {
		return err
	}

	logger.Debugln("Added transport to visor")
	return nil
}

func (a *autoconnector) fetchPubAddresses(ctx context.Context) ([]cipher.PubKey, error) {
	retrier := netutil.NewRetrier(a.log, fetchServicesDelay, 0, 5, 3)
	var services []servicedisc.Service
	fetch := func() (err error) {
		// "return" services up from the closure
		services, err = a.client.Services(ctx, a.maxConns, "", "")
		if err != nil {
			return err
		}
		return nil
	}
	if err := retrier.Do(ctx, fetch); err != nil {
		return nil, err
	}
	pks := make([]cipher.PubKey, len(services))
	for i, service := range services {
		pks[i] = service.Addr.PubKey()
	}
	return pks, nil
}

// return public keys from pks that are absent in given list of transports
func (a *autoconnector) filterDuplicates(pks []cipher.PubKey, trs []*transport.ManagedTransport) []cipher.PubKey {
	var absent []cipher.PubKey
	for _, pk := range pks {
		found := false
		for _, tr := range trs {
			if tr.Entry.HasEdge(pk) {
				found = true
				break
			}
		}
		if !found {
			absent = append(absent, pk)
		}
	}
	return absent
}

// randInt returns a secure random integer in [min, max).
func randInt(n, x int) (int, error) {
	if n >= x {
		return n, nil
	}
	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(x-n)))
	if err != nil {
		return 0, err
	}
	return int(nBig.Int64()) + n, nil
}
