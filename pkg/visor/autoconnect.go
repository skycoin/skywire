// Package visor pkg/visor/autoconnect.go
package visor

import (
	"context"
	"crypto/rand"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
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
	client         *servicedisc.HTTPClient
	maxConns       int
	log            *logging.Logger
	tm             *transport.Manager
	visorIsPublic  bool
	clientPublicIP string
}

// MakeConnector returns a new connector that will try to connect to at most maxConns
// services
func MakeConnector(conf servicedisc.Config, maxConns int, tm *transport.Manager, httpC *http.Client, clientPublicIP string,
	log *logging.Logger, mLog *logging.MasterLogger) Autoconnector {
	// Extract just the IP from clientPublicIP (may include port)
	publicIP := clientPublicIP
	if host, _, err := net.SplitHostPort(publicIP); err == nil {
		publicIP = host
	}

	connector := &autoconnector{}
	connector.client = servicedisc.NewClient(log, mLog, conf, httpC, clientPublicIP)
	connector.maxConns = maxConns
	connector.log = log
	connector.tm = tm
	connector.clientPublicIP = publicIP
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

			// Public autoconnect logic:
			// Phase 1: SUDPH to public visors (if SUDPH available)
			// Phase 2: STCPR to public visors (both transport types to same visors)
			// Phase 3: SUDPH to other connected visors

			const maxPublicVisors = 5 // Connect to up to 5 public visors
			const maxSUDPH = 30       // Max SUDPH transports to other visors

			// Count existing automatic transports by type and remote PK
			countSTCPR := 0
			countSUDPH := 0
			existingByPK := make(map[cipher.PubKey]map[tptypes.Type]bool)
			for _, autoconnTP := range a.tm.GetTransportsByLabel(transport.LabelAutomatic) {
				remotePK := autoconnTP.Remote()
				if existingByPK[remotePK] == nil {
					existingByPK[remotePK] = make(map[tptypes.Type]bool)
				}
				existingByPK[remotePK][autoconnTP.Type()] = true
				switch autoconnTP.Type() {
				case tptypes.STCPR:
					countSTCPR++
				case tptypes.SUDPH:
					countSUDPH++
				}
			}

			// Track which public visors we connect to
			connectedPublicVisors := make([]cipher.PubKey, 0, maxPublicVisors)

			// Phase 1: SUDPH to public visors (if supported)
			if localSupportsSUDPH {
				a.log.Debug("Phase 1: Connecting to public visors via SUDPH")
				for _, pk := range shufflePubKeys(absent1) {
					select {
					case <-ctx.Done():
						a.log.Debug("Context canceled, stopping public autoconnect loop")
						return context.Canceled
					default:
					}

					if len(connectedPublicVisors) >= maxPublicVisors {
						break
					}

					if pk == v.conf.PK {
						continue
					}

					// Skip if we already have SUDPH to this visor
					if existingByPK[pk][tptypes.SUDPH] {
						connectedPublicVisors = append(connectedPublicVisors, pk)
						continue
					}

					// Skip visors behind the same NAT
					if a.clientPublicIP != "" {
						if sameLAN := a.isSameLAN(ctx, pk, tptypes.SUDPH); sameLAN {
							a.log.WithField("pk", pk).
								Debugln("Skipping same-LAN visor (same public IP)")
							continue
						}
					}

					logger := a.log.WithField("pk", pk).WithField("type", string(tptypes.SUDPH))
					logger.Debugln("Trying to add SUDPH transport to public visor")

					if err = a.tryEstablishTransport(ctx, pk, tptypes.SUDPH, logger); err != nil {
						if errors.Is(err, context.Canceled) ||
							errors.Is(err, context.DeadlineExceeded) ||
							strings.Contains(err.Error(), "operation was canceled") ||
							strings.Contains(err.Error(), "context canceled") {
							logger.WithError(err).Debugln("Transport creation canceled (shutdown)")
						} else {
							logger.WithError(err).Warnln("Failed to add SUDPH transport to public visor")
						}
						// Still track this visor for STCPR attempt
						connectedPublicVisors = append(connectedPublicVisors, pk)
						continue
					}

					countSUDPH++
					connectedPublicVisors = append(connectedPublicVisors, pk)
					a.log.WithField("count", countSUDPH).Debug("SUDPH transport to public visor established")
				}
			} else {
				// If no SUDPH, just pick public visors for STCPR
				for _, pk := range shufflePubKeys(absent1) {
					if len(connectedPublicVisors) >= maxPublicVisors {
						break
					}
					if pk != v.conf.PK {
						connectedPublicVisors = append(connectedPublicVisors, pk)
					}
				}
			}

			// Phase 2: STCPR to the same public visors
			if localSupportsSTCPR {
				a.log.Debug("Phase 2: Connecting to public visors via STCPR")
				for _, pk := range connectedPublicVisors {
					select {
					case <-ctx.Done():
						a.log.Debug("Context canceled, stopping STCPR autoconnect loop")
						return context.Canceled
					default:
					}

					// Skip if we already have STCPR to this visor
					if existingByPK[pk][tptypes.STCPR] {
						continue
					}

					// Skip visors behind the same NAT
					if a.clientPublicIP != "" {
						if sameLAN := a.isSameLAN(ctx, pk, tptypes.STCPR); sameLAN {
							a.log.WithField("pk", pk).
								Debugln("Skipping same-LAN visor for STCPR (same public IP)")
							continue
						}
					}

					logger := a.log.WithField("pk", pk).WithField("type", string(tptypes.STCPR))
					logger.Debugln("Trying to add STCPR transport to public visor")

					if err = a.tryEstablishTransport(ctx, pk, tptypes.STCPR, logger); err != nil {
						if errors.Is(err, context.Canceled) ||
							errors.Is(err, context.DeadlineExceeded) ||
							strings.Contains(err.Error(), "operation was canceled") ||
							strings.Contains(err.Error(), "context canceled") {
							logger.WithError(err).Debugln("Transport creation canceled (shutdown)")
						} else {
							logger.WithError(err).Warnln("Failed to add STCPR transport to public visor")
						}
						continue
					}

					countSTCPR++
					a.log.WithField("count", countSTCPR).Debug("STCPR transport to public visor established")
				}
			}

			// Phase 3: SUDPH to other connected visors
			if localSupportsSUDPH && countSUDPH < maxSUDPH && len(absent2) > 0 {
				a.log.Debug("Phase 3: Connecting to other visors via SUDPH")
				for _, pk := range shufflePubKeys(absent2) {
					select {
					case <-ctx.Done():
						a.log.Debug("Context canceled, stopping SUDPH autoconnect loop")
						return context.Canceled
					default:
					}

					if countSUDPH >= maxSUDPH {
						break
					}

					if pk == v.conf.PK {
						continue
					}

					// Skip if we already have SUDPH to this visor
					if existingByPK[pk][tptypes.SUDPH] {
						continue
					}

					// Skip visors behind the same NAT
					if a.clientPublicIP != "" {
						if sameLAN := a.isSameLAN(ctx, pk, tptypes.SUDPH); sameLAN {
							a.log.WithField("pk", pk).
								Debugln("Skipping same-LAN visor (same public IP)")
							continue
						}
					}

					logger := a.log.WithField("pk", pk).WithField("type", string(tptypes.SUDPH))
					logger.Debugln("Trying to add SUDPH transport to other visor")

					if err = a.tryEstablishTransport(ctx, pk, tptypes.SUDPH, logger); err != nil {
						if errors.Is(err, context.Canceled) ||
							errors.Is(err, context.DeadlineExceeded) ||
							strings.Contains(err.Error(), "operation was canceled") ||
							strings.Contains(err.Error(), "context canceled") {
							logger.WithError(err).Debugln("Transport creation canceled (shutdown)")
						} else {
							logger.WithError(err).Warnln("Failed to add SUDPH transport")
						}
						continue
					}

					countSUDPH++
					a.log.WithField("count", countSUDPH).Debug("SUDPH transport established")
				}
			}

			a.log.WithField("stcpr", countSTCPR).WithField("sudph", countSUDPH).
				WithField("public_visors", len(connectedPublicVisors)).
				Debug("Public autoconnect cycle completed")
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

// isSameLAN checks if the remote visor is behind the same NAT as us by comparing
// public IPs via the address resolver. Returns false on any error (fail-open).
func (a *autoconnector) isSameLAN(ctx context.Context, pk cipher.PubKey, netType tptypes.Type) bool {
	arClient := a.tm.ARClient()
	if arClient == nil {
		return false
	}

	visorData, err := arClient.Resolve(ctx, string(netType), pk)
	if err != nil {
		return false
	}

	remoteIP := visorData.RemoteAddr
	if host, _, err := net.SplitHostPort(remoteIP); err == nil {
		remoteIP = host
	}

	return remoteIP != "" && remoteIP == a.clientPublicIP
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
