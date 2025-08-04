// Package visor pkg/visor/autoconnect.go
package visor

import (
	"context"
	"crypto/rand"
	"math/big"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
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
	// Wait for a random interval between 0 and 5 minutes before starting public autoconnect.											//
	// Prevents a cluster of nodes which was switched on at the same time																					//
	// from producing concurrent, synchronous requests to SD and subsequent connection attempts to public visors	//
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
			/*
				///health check to determine online status of public visor
				var addrs1 []cipher.PubKey
				for _, addr := range addrs {
					req, err := http.NewRequest(http.MethodGet, "dmsg://"+addr.String()+":80/health", nil)
					if err != nil {
						a.log.Debugln("failed to formulate http request")
						continue
					}
					resp, err := v.dmsgHTTP.Do(req)
					if err == nil {
						defer resp.Body.Close()          //nolint
						body, _ := io.ReadAll(resp.Body) //nolint
						addrs1 = append(addrs1, addr)
						a.log.WithField("pk", addr.String()).WithField("response.Body", string(body)).Debugln("Public visor dmsghttp '/health' check")
						continue
					}
					a.log.WithField("pk", addr.String()).Debugln("Public visor dmsghttp '/health' check failed")
					//				if addr == v.conf.PK {
					//					a.log.Debugln("Can't fetch '/health' from local visor over dmsg")
					//					v.Close()
					//				}
				}
				addrs = addrs1

				if len(addrs) == 0 {
					a.log.Debugln("no public visors found")
					continue
				}
			*/
			a.log.WithField("public visors", len(addrs)).Debugln("Found")

			absent := a.filterDuplicates(addrs, a.tm.GetTransportsByLabel(transport.LabelAutomatic))

			// Get keys available for SUDPH transport
			sudphKeys, err := v.arClient.TransportsType(ctx, tptypes.SUDPH)
			if err != nil {
				a.log.WithError(err).Warn("could not fetch keys from address resolver available for SUDPH transport")
				sudphKeys = map[cipher.PubKey][]string{}
			}

			// Get keys available for STCPR transport
			stcprKeys, err := v.arClient.TransportsType(ctx, tptypes.STCPR)
			if err != nil {
				a.log.WithError(err).Warn("could not fetch keys from address resolver available for STCPR transport")
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

				//check transport discovery for transport entries containing public visor edge key
				//in order to obtain a list of keys to connect to via SUDPH
				for _, pk := range addrs {
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

			//absent = append(absent, absent2...)
			a.log.WithField("total", len(append(absent, absent2...))).Debugln("Found visors to connect to")

			//attempt to establish transports to the keys in random order for 5 minutes
			attemptDeadline := time.Now().Add(5 * time.Minute)
			// Max STCPR Transports == a.maxConns == 3
			maxSTCPR := a.maxConns
			// Max SUDPH Transports == a.maxConns * 5 == 15
			maxSUDPH := a.maxConns * 5
			for _, pk := range append(shufflePubKeys(absent), shufflePubKeys(absent2)...) {
				if time.Now().After(attemptDeadline) {
					a.log.Debugln("Refreshing list of keys for public autoconnect")
					break
				}
				//don't make self-transports
				if pk == v.conf.PK {
					continue
				}

				// Limit maximum number of autoconnect transports per transport type
				countSTCPR := 0
				countSUDPH := 0

				for _, autoconnTP := range a.tm.GetTransportsByLabel(transport.LabelAutomatic) {
					switch autoconnTP.Type() {
					case tptypes.STCPR:
						countSTCPR++
					case tptypes.SUDPH:
						// filter SUDPH counting here to only count sudph transports
						// to visors which are currently connected to a public visor
						countSUDPH++
					}
				}

				// limit transports if the remote visor already has maxconns * 100 total transports
				// Check the transport discovery, and skip if number of entries is >= a.maxConns
				entries, err := v.DiscoverTransportsByPK(pk)
				if err != nil {
					v.isServicesHealthy.unset()
					a.log.WithField("pk", pk).WithError(err).Warn("Failed to discover transports for remote visor")
					continue
				}
				v.isServicesHealthy.set()

				if len(entries) >= a.maxConns*100 {
					a.log.WithField("pk", pk).WithField("count", len(entries)).Debugln("Remote visor has reached or exceeded max connections, skipping")
					continue
				}

				// Determine network type and attempt to establish transport using that type
				var netType tptypes.Type
				if _, ok := sudphKeys[pk]; ok {
					netType = tptypes.SUDPH
				} else if _, ok := stcprKeys[pk]; ok {
					netType = tptypes.STCPR
				} else {
					a.log.WithField("pk", pk).Debugln("No supported network type found for visor")
					continue
				}

				if netType == tptypes.STCPR && countSTCPR >= maxSTCPR {
					a.log.WithField("pk", pk).WithField("type", netType).Debugln("Not transporting; STCPR max reached")
					continue
				}
				if netType == tptypes.SUDPH && countSUDPH >= maxSUDPH {
					a.log.WithField("pk", pk).WithField("type", netType).Debugln("Not transporting; SUDPH max reached")
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
func (a *autoconnector) tryEstablishTransport(ctx context.Context, pk cipher.PubKey, netType tptypes.Type, logger *logrus.Entry) error {
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
