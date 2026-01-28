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
	client        *servicedisc.HTTPClient
	maxConns      int
	log           *logging.Logger
	tm            *transport.Manager
	visorIsPublic bool
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
	publicServiceTicker := time.NewTicker(PublicServiceDelay)

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case <-publicServiceTicker.C:

			a.log.Infoln("Fetching public visors")

			// fetch public visors
			var addrs []cipher.PubKey
			addrs, err = a.fetchPubAddresses(ctx)
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

			// Check which transport types are supported locally
			localSupportsSUDPH := a.tm.IsKnownNetwork(tptypes.SUDPH)
			localSupportsSTCPR := a.tm.IsKnownNetwork(tptypes.STCPR)

			if !localSupportsSUDPH && !localSupportsSTCPR {
				a.log.Warn("No supported network types available locally (SUDPH and STCPR both unavailable)")
				continue
			}

			// Check if this visor is configured as public
			// This uses the same config flag that initPublicVisor uses to decide whether to register in SD
			visorIsPublic := v.conf.IsPublic
			a.visorIsPublic = visorIsPublic
			if visorIsPublic {
				a.log.Debug("This visor is configured as public")
			}

			// Fetch ALL transport discovery data once per cycle to reduce API load
			// This replaces individual DiscoverTransportsByPK calls throughout the cycle
			a.log.Debug("Fetching all transport discovery data for caching")
			transportCache, err := a.buildTransportCache(ctx, v)
			if err != nil {
				a.log.WithError(err).Warn("Failed to fetch transport discovery cache, continuing without it")
				transportCache = &transportDiscoveryCache{
					entriesByPK:     make(map[cipher.PubKey][]*transport.Entry),
					transportCounts: make(map[cipher.PubKey]int),
				}
			} else {
				a.log.WithField("cached_keys", len(transportCache.transportCounts)).
					Debug("Successfully cached transport discovery data")
			}

			// auto-transport logic - for non-public visors connecting to public visors
			var absent2 []cipher.PubKey
			if !visorIsPublic {
				absentSet := make(map[cipher.PubKey]struct{}, len(addrs))
				for _, pk := range addrs {
					absentSet[pk] = struct{}{}
				}

				autoTransports := a.tm.GetTransportsByLabel(transport.LabelAutomatic)
				absent2Set := make(map[cipher.PubKey]struct{})

				// Use cached transport data instead of individual API calls
				for _, pk := range addrs {
					entries := transportCache.entriesByPK[pk]

					seen := make(map[cipher.PubKey]struct{})
					for _, entry := range entries {
						for _, edge := range entry.Edges {
							if edge != v.conf.PK {
								seen[edge] = struct{}{}
							}
						}
					}

					entryKeys := make([]cipher.PubKey, 0, len(seen))
					for edge := range seen {
						entryKeys = append(entryKeys, edge)
					}

					filtered := a.filterDuplicates(entryKeys, autoTransports)
					for _, newPK := range filtered {
						if _, inAbsent := absentSet[newPK]; inAbsent {
							continue
						}
						if _, seen := absent2Set[newPK]; seen {
							continue
						}

						absent2Set[newPK] = struct{}{}
						absent2 = append(absent2, newPK)
					}
				}
			}

			a.log.WithField("total", len(append(absent1, absent2...))).
				Debugln("Found visors to connect to")

			// Attempt to establish transports to the keys in random order for 5 minutes
			attemptDeadline := time.Now().Add(5 * time.Minute)

			maxSTCPR := a.maxConns
			maxSUDPH := a.maxConns * 5

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

			if countSTCPR >= maxSTCPR && countSUDPH >= maxSUDPH {
				a.log.Debugln("Skipping public autoconnect; max STCPR and SUDPH transports reached")
				continue
			}

			for _, pk := range append(shufflePubKeys(absent1), shufflePubKeys(absent2)...) {
				if time.Now().After(attemptDeadline) {
					a.log.Debugln("Refreshing list of keys for public autoconnect")
					break
				}

				if pk == v.conf.PK {
					continue
				}

				// Determine transport type based on visor category:
				// - Public visors (from service discovery) -> STCPR (they have public IPs)
				// - Non-public visors (discovered via transport data) -> SUDPH
				var netType tptypes.Type
				isPublicVisor := a.isInList(pk, absent1)
				if isPublicVisor && localSupportsSTCPR {
					netType = tptypes.STCPR
				} else if localSupportsSUDPH {
					netType = tptypes.SUDPH
				} else {
					a.log.WithField("pk", pk).Debugln("No supported network type available for visor")
					continue
				}

				if netType == tptypes.STCPR && countSTCPR >= maxSTCPR {
					continue // silently skip
				}
				if netType == tptypes.SUDPH && countSUDPH >= maxSUDPH {
					continue // silently skip
				}

				// Hybrid approach for connection count checking:
				// - For STCPR to public visors: get FRESH stats (critical bottleneck)
				// - For everything else: use cached data (stale is acceptable)
				var transportCount int
				if netType == tptypes.STCPR && isPublicVisor {
					// Fresh check for STCPR to public visors - these are most prone to overload
					tpD := v.tpDiscClient()
					freshStats, err := tpD.GetTransportStats(ctx, pk)
					if err != nil {
						a.log.WithField("pk", pk).WithError(err).
							Warn("Failed to get fresh transport stats, using cached count")
						transportCount = transportCache.transportCounts[pk]
					} else {
						transportCount = freshStats.Total
						a.log.WithField("pk", pk).WithField("fresh_count", freshStats.Total).
							Debug("Got fresh transport count for public visor")
					}
				} else {
					// Use cached count for SUDPH or non-public visors
					transportCount = transportCache.transportCounts[pk]
				}

				// Skip transport limit check for public-to-public connections
				// Public visors must maintain full mesh connectivity regardless of individual transport limits
				if a.visorIsPublic && isPublicVisor {
					a.log.WithField("pk", pk).WithField("count", transportCount).
						Debug("Public-to-public connection: bypassing transport limit check")
				} else if transportCount >= a.maxConns*100 {
					a.log.WithField("pk", pk).WithField("count", transportCount).
						Debugln("Remote visor has reached or exceeded max connections, skipping")
					continue
				}

				a.log.WithField("pk", pk).WithField("type", netType).
					Debugln("Trying to add transport to public visor")

				// Double-check that the network type is supported locally before attempting
				if !a.tm.IsKnownNetwork(netType) {
					a.log.WithField("pk", pk).WithField("type", netType).
						Warnln("Network type not supported locally, skipping")
					continue
				}

				logger := a.log.WithField("pk", pk).WithField("type", string(netType))
				if err = a.tryEstablishTransport(ctx, pk, netType, logger); err != nil {
					logger.WithError(err).Warnln("Failed to add transport to visor")
					continue
				}

				if netType == tptypes.STCPR {
					countSTCPR++
				} else {
					countSUDPH++
				}

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

// isInList checks if a public key is in the given list
func (a *autoconnector) isInList(pk cipher.PubKey, list []cipher.PubKey) bool {
	for _, listPK := range list {
		if listPK == pk {
			return true
		}
	}
	return false
}

// transportDiscoveryCache holds cached transport discovery data to reduce API calls
type transportDiscoveryCache struct {
	entriesByPK     map[cipher.PubKey][]*transport.Entry
	transportCounts map[cipher.PubKey]int
}

// buildTransportCache fetches all transport discovery data once and builds lookup maps.
// This replaces hundreds of individual DiscoverTransportsByPK API calls with a single
// GetAllTransports call, dramatically reducing load on the transport discovery service.
func (a *autoconnector) buildTransportCache(ctx context.Context, v *Visor) (*transportDiscoveryCache, error) {
	tpD := v.tpDiscClient()

	// Fetch ALL transports in one API call (instead of per-key calls)
	allEntries, err := tpD.GetAllTransports(ctx)
	if err != nil {
		return nil, err
	}

	cache := &transportDiscoveryCache{
		entriesByPK:     make(map[cipher.PubKey][]*transport.Entry),
		transportCounts: make(map[cipher.PubKey]int),
	}

	// Build lookup maps: pk -> entries and pk -> count
	for _, entry := range allEntries {
		for _, edge := range entry.Edges {
			cache.entriesByPK[edge] = append(cache.entriesByPK[edge], entry)
			cache.transportCounts[edge]++
		}
	}

	return cache, nil
}
