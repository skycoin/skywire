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

			a.log.WithField("public visors", len(addrs)).Debugln("Found")

			absent1 := a.filterDuplicates(addrs, a.tm.GetTransportsByLabel(transport.LabelAutomatic))

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

			// auto-transport logic - for visors running as public visors
			// should only attempt to connect stcpr transports to other public visors
			var absent2 []cipher.PubKey
			if !visorIsPublic {
				// track keys we already have
				absentSet := make(map[cipher.PubKey]struct{}, len(addrs))
				for _, pk := range addrs {
					absentSet[pk] = struct{}{}
				}

				// existing auto transports to filter against
				autoTransports := a.tm.GetTransportsByLabel(transport.LabelAutomatic)

				// track which keys we already plan to connect to
				absent2Set := make(map[cipher.PubKey]struct{})

				for _, pk := range addrs {
					entries, err := v.DiscoverTransportsByPK(pk)
					if err != nil {
						v.isServicesHealthy.unset()
						a.log.WithError(err).Warn("cannot connect to transport discovery service")
						continue
					}
					v.isServicesHealthy.set()

					seen := make(map[cipher.PubKey]struct{})
					for _, entry := range entries {
						for _, edge := range entry.Edges {
							if edge == v.conf.PK {
								continue
							}
							seen[edge] = struct{}{}
						}
					}

					// collect new candidate keys
					entryKeys := make([]cipher.PubKey, 0, len(seen))
					for edge := range seen {
						entryKeys = append(entryKeys, edge)
					}

					// filter out ones that already have transports
					filtered := a.filterDuplicates(entryKeys, autoTransports)

					for _, newPK := range filtered {
						// skip if in original absent list
						if _, inAbsent := absentSet[newPK]; inAbsent {
							continue
						}
						// skip if already added
						if _, seen := absent2Set[newPK]; seen {
							continue
						}
						// skip if not a valid SUDPH key
						if _, ok := sudphKeys[newPK]; !ok {
							continue
						}

						absent2Set[newPK] = struct{}{}
						absent2 = append(absent2, newPK)
					}
				}
			}

			a.log.WithField("total", len(append(absent1, absent2...))).Debugln("Found visors to connect to")

			// attempt to establish transports to the keys in random order for 5 minutes
			attemptDeadline := time.Now().Add(5 * time.Minute)

			// Max STCPR Transports == a.maxConns == 3
			maxSTCPR := a.maxConns
			// Max SUDPH Transports == a.maxConns * 5 == 15
			maxSUDPH := a.maxConns * 5

			// Calculate current transport counts once
			countSTCPR := 0
			countSUDPH := 0
			for _, autoconnTP := range a.tm.GetTransportsByLabel(transport.LabelAutomatic) {
				switch autoconnTP.Type() {
				case tptypes.STCPR:
					countSTCPR++
				case tptypes.SUDPH:
					countSUDPH++
				}
			}

			// Skip loop entirely if we're already at the max for both
			if countSTCPR >= maxSTCPR && countSUDPH >= maxSUDPH {
				a.log.Debugln("Skipping public autoconnect; max STCPR and SUDPH transports reached")
				return
			}

			// Attempt to connect to shuffled keys
			for _, pk := range append(shufflePubKeys(absent1), shufflePubKeys(absent2)...) {
				if time.Now().After(attemptDeadline) {
					a.log.Debugln("Refreshing list of keys for public autoconnect")
					break
				}

				// don't make self-transports
				if pk == v.conf.PK {
					continue
				}

				// Determine network type first
				var netType tptypes.Type
				if _, ok := stcprKeys[pk]; ok {
					netType = tptypes.STCPR
				} else if _, ok := sudphKeys[pk]; ok {
					netType = tptypes.SUDPH
				} else {
					a.log.WithField("pk", pk).Debugln("No supported network type found for visor")
					continue
				}

				// Check limits before any network calls
				if netType == tptypes.STCPR && countSTCPR >= maxSTCPR {
					// silently skip
					continue
				}
				if netType == tptypes.SUDPH && countSUDPH >= maxSUDPH {
					// silently skip
					continue
				}

				// Check the transport discovery for overload
				entries, err := v.DiscoverTransportsByPK(pk)
				if err != nil {
					v.isServicesHealthy.unset()
					a.log.WithField("pk", pk).WithError(err).Warn("Failed to discover transports for remote visor")
					continue
				}
				v.isServicesHealthy.set()

				if len(entries) >= a.maxConns*100 {
					a.log.WithField("pk", pk).WithField("count", len(entries)).
						Debugln("Remote visor has reached or exceeded max connections, skipping")
					continue
				}

				// Try to establish transport
				a.log.WithField("pk", pk).WithField("type", netType).Debugln("Trying to add transport to public visor")
				logger := a.log.WithField("pk", pk).WithField("type", string(netType))
				if err = a.tryEstablishTransport(ctx, pk, netType, logger); err != nil {
					logger.WithError(err).Warnln("Failed to add transport to visor")
					continue
				}

				// Transport successfully added, increment the counters
				if netType == tptypes.STCPR {
					countSTCPR++
				} else {
					countSUDPH++
				}

				// Exit early if we reach the limit after adding
				if countSTCPR >= maxSTCPR && countSUDPH >= maxSUDPH {
					a.log.Debugln("Max STCPR and SUDPH transports reached, stopping loop")
					break
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
