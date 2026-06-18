// Package addrresolver implements address resolver client
package addrresolver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/sirupsen/logrus"
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauthclient"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network/packetfilter"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// sudphPriority is used to set an order how connection filters apply.
	sudphPriority            = 1
	stcprBindPath            = "/bind/stcpr"
	addrChSize               = 1024
	udpKeepHeartbeatInterval = 10 * time.Second
	sudphReRegisterInterval  = 90 * time.Second
	// UDPKeepHeartbeatMessage is used as a heartbeat packet to keep connection alive.
	UDPKeepHeartbeatMessage = "heartbeat"
	defaultUDPPort          = "30178"
	// stcprBindPublicIPWait bounds how long BindSTCPR waits for the visor's
	// asynchronously-determined public IP before binding without it. Sized to
	// comfortably cover the dmsg LookupIPGeo (10s) + STUN (20s) fallback path;
	// on timeout the bind proceeds and the re-registration loop carries the
	// real IP once it lands.
	stcprBindPublicIPWait = 35 * time.Second
	// UDPDelBindMessage is used as a deletebind packet on visor shutdown.
	UDPDelBindMessage = "delBind"
	// sudphReconnectInitialBackoff is the initial sleep before the first
	// retry after a SUDPH connection to AR dies. Doubles on each failed
	// attempt up to sudphReconnectMaxBackoff.
	sudphReconnectInitialBackoff = 2 * time.Second
	// sudphReconnectMaxBackoff caps the reconnect sleep so a long AR
	// outage doesn't push the retry interval out indefinitely.
	sudphReconnectMaxBackoff = 60 * time.Second
	// sudphARReadTimeout bounds how long the SUDPH read loop waits for any
	// inbound packet from the AR before treating the connection as dead.
	// Over KCP-on-UDP a visor's writes keep "succeeding" even after the AR
	// process is gone (e.g. a redeploy), so an inbound-silence deadline is
	// the only reliable liveness signal. The AR echoes our 10s heartbeats,
	// so a live AR resets this every interval; ~4 missed echoes trips it and
	// drives a reconnect+re-register via serveSUDPHReconnect.
	sudphARReadTimeout = 40 * time.Second
)

var (
	// ErrNoEntry means that there exists no entry for this PK.
	ErrNoEntry = errors.New("no entry for this PK")
	// ErrNotReady is returned when address resolver is not ready.
	ErrNotReady = errors.New("address resolver is not ready")
	// ErrNoTransportsFound returned when no transports are found.
	ErrNoTransportsFound = errors.New("failed to get response data from AR transports endpoint")
)

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

//go:generate mockery --name APIClient --case underscore --inpackage

// APIClient implements address resolver API client.
type APIClient interface {
	BindSTCPR(ctx context.Context, port string) error
	BindSUDPH(filter *pfilter.PacketFilter, handshake Handshake) (<-chan RemoteVisor, error)
	Resolve(ctx context.Context, netType string, pk cipher.PubKey) (VisorData, error)
	Transports(ctx context.Context) (map[cipher.PubKey][]string, error)
	TransportsType(ctx context.Context, tpType types.Type) (map[cipher.PubKey][]string, error)
	Addresses(ctx context.Context) string
	LocalPublicIP() string
	// SetPublicIP records the visor's externally-reachable IPs once they have
	// been determined asynchronously (so the control plane need not wait on
	// the dmsg/STUN lookup). Passing empty strings signals "determination
	// finished, none available" and unblocks any pending bind.
	SetPublicIP(publicIP, publicIPv6 string)
	Close() error
}

// VisorData stores visor data.
//
// RemoteAddr is the visor's reachable IPv4 endpoint ("host:port"),
// observed by the AR server from the bind request's source socket
// (or carried via the visor's declared PublicIP). RemoteAddrV6 is
// the optional IPv6 counterpart, populated when the visor binds
// over an IPv6 HTTP client. A dual-stack visor calls bind twice
// (once per family) and the AR server merges into a single record;
// peers Resolve once and the dialer picks a family (v6 first per
// RFC 8305 with v4 fallback). Backward-compat: v4-only visors and
// older AR servers emit/store an empty RemoteAddrV6 and the rest
// of the pipeline behaves exactly as today.
type VisorData struct {
	RemoteAddr   string `json:"remote_addr"`
	RemoteAddrV6 string `json:"remote_addr_v6,omitempty"`
	IsLocal      bool   `json:"is_local,omitempty"`
	LocalAddresses
}

// httpClient implements APIClient for address resolver API.
//
// httpClientV6 is the optional IPv6-family-forced HTTP transport
// added in #1525 Phase 2b: when the operator's AR URL resolves to
// both v4 and v6 (i.e. has both A and AAAA records), the visor
// fires a SECONDARY BindSTCPR POST over this client so the AR
// server captures the visor's v6 source via splitFamilyAddr and
// stores RemoteAddrV6 alongside RemoteAddr. nil when the AR is
// reached via dmsg, when v6 init failed, or when the caller didn't
// supply a v6 client — preserves pre-#1525 v4-only behavior.
type httpClient struct {
	log            *logging.Logger
	mLog           *logging.MasterLogger
	httpClient     *httpauthclient.Client
	httpClientV6   *httpauthclient.Client
	pk             cipher.PubKey
	sk             cipher.SecKey
	remoteHTTPAddr string
	remoteHTTPURL  *url.URL
	remoteUDPAddr  string
	sudphConn      net.PacketConn
	sudphArConn    net.Conn
	sudphArConnMu  sync.Mutex
	sudphLocalAddr LocalAddresses
	// clientPublicIP / clientPublicIPv6 are the visor's externally-reachable
	// IPs declared to the AR on bind. They may be determined asynchronously
	// (see SetPublicIP) so the visor's control plane (RPC) need not wait on
	// the slow dmsg/STUN public-IP lookup — guard every access with pubIPMu.
	pubIPMu          sync.RWMutex
	clientPublicIP   string
	clientPublicIPv6 string
	// ipReady is closed once the public IP has been determined (or determined
	// to be unavailable) via SetPublicIP — or immediately at construction when
	// a non-empty IP was supplied. The bind path waits on it (bounded) so
	// registrations carry the public IP without blocking the control plane.
	ipReady        chan struct{}
	ipReadyOnce    sync.Once
	ready          chan struct{}
	closed         chan struct{}
	delBindSudphWg sync.WaitGroup
}

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key.
// * SW-Nonce:  The nonce for that public key.
// * SW-Sig:    The signature of the payload + the nonce.
//
// When remoteAddr uses the dmsg:// scheme the URL host is a public key,
// not an IP, so it cannot be UDP-resolved for SUDPH. In that case the
// UDP target is left empty here and resolved from the AR's /health
// (udp_address field) once the auth client is ready. ARs that don't
// publish udp_address simply leave SUDPH unavailable to dmsg-only
// callers — the same behavior as before this change.
func NewHTTP(remoteAddr string, pk cipher.PubKey, sk cipher.SecKey, httpC, httpCV6 *http.Client, clientPublicIP, clientPublicIPv6 string, log *logging.Logger, mLog *logging.MasterLogger) (APIClient, error) {
	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	var remoteUDP string
	if remoteURL.Scheme != "dmsg" {
		remoteUDP = remoteURL.Host
		if _, _, err := net.SplitHostPort(remoteUDP); err != nil {
			remoteUDP = net.JoinHostPort(remoteUDP, defaultUDPPort)
		}
	}

	client := &httpClient{
		log:              log,
		mLog:             mLog,
		pk:               pk,
		sk:               sk,
		remoteHTTPAddr:   remoteAddr,
		remoteHTTPURL:    remoteURL,
		remoteUDPAddr:    remoteUDP,
		clientPublicIP:   clientPublicIP,
		clientPublicIPv6: clientPublicIPv6,
		ipReady:          make(chan struct{}),
		ready:            make(chan struct{}),
		closed:           make(chan struct{}),
	}

	// When a public IP was supplied at construction the synchronous behavior
	// is unchanged — mark it ready so the bind path never waits. Callers that
	// defer the lookup pass "" here and call SetPublicIP once it's known.
	if clientPublicIP != "" {
		client.ipReadyOnce.Do(func() { close(client.ipReady) })
	}

	client.log.Debugf("Remote UDP server: %q", remoteUDP)

	go client.initHTTPClient(httpC, httpCV6)

	return client, nil
}

func (c *httpClient) initHTTPClient(httpC, httpCV6 *http.Client) {
	// Snapshot the public IP once under the lock — it may be set concurrently
	// by SetPublicIP. Empty is fine: the SW-PublicIP header is simply omitted
	// until the next request that the AR observes the source IP for, and the
	// bind POST body carries the IP independently once SetPublicIP fires.
	pubIP := c.localPublicIPRaw()
	httpAuthClient, err := httpauthclient.NewClient(context.Background(), c.remoteHTTPAddr, c.pk, c.sk, httpC, pubIP, c.mLog)
	if err != nil {
		c.log.WithError(err).
			Warnf("Failed to connect to address resolver. STCPR/SUDPH services are temporarily unavailable. Retrying...")

		retry := netutil.NewRetrier(c.log, 1*time.Second, 10*time.Second, 0, 1)

		err := retry.Do(context.Background(), func() error {
			httpAuthClient, err = httpauthclient.NewClient(context.Background(), c.remoteHTTPAddr, c.pk, c.sk, httpC, c.localPublicIPRaw(), c.mLog)
			return err
		})

		if err != nil {
			// This should not happen as retrier is set to try indefinitely.
			// If address resolver cannot be contacted indefinitely, 'c.ready' will be blocked indefinitely.
			c.log.WithError(err).Fatal("Permanently failed to connect to address resolver.")
		}
	}

	c.httpClient = httpAuthClient

	// #1525 Phase 2b: initialize the optional v6-forced auth client.
	// Best-effort, NO retry: a v6 init failure (no AAAA record on the
	// AR, no v6 connectivity from the visor, AR not listening on v6)
	// leaves httpClientV6 nil and the visor proceeds v4-only — same
	// behavior as a pre-#1525 build. The retry+fatal contract from the
	// primary client doesn't apply here: v6 is additive, not required.
	if httpCV6 != nil {
		v6AuthClient, v6Err := httpauthclient.NewClient(context.Background(), c.remoteHTTPAddr, c.pk, c.sk, httpCV6, c.localPublicIPRaw(), c.mLog)
		if v6Err != nil {
			c.log.WithError(v6Err).Debug("v6 address-resolver init failed; proceeding v4-only")
		} else {
			c.httpClientV6 = v6AuthClient
			c.log.Debug("v6 address-resolver init ok; BindSTCPR will dual-stack")
		}
	}

	// dmsg:// URL has no IP for SUDPH — pull AR's public UDP address
	// from /health (set by --public-udp-address on the server). Best
	// effort: a missing or unparseable value just leaves SUDPH unavailable
	// for this AR, which is the pre-fix behavior.
	if c.remoteHTTPURL != nil && c.remoteHTTPURL.Scheme == "dmsg" {
		if udpAddr := c.fetchPublicUDPAddr(httpC); udpAddr != "" {
			c.remoteUDPAddr = udpAddr
			c.log.Debugf("Resolved AR UDP address via /health: %q", udpAddr)
		} else {
			c.log.Info("AR /health did not advertise udp_address; SUDPH unavailable for this AR")
		}
	}

	c.log.Debug("Connected to address resolver. STCPR/SUDPH services are available.")

	close(c.ready)
}

// fetchPublicUDPAddr does a single unauthenticated GET /health using
// the underlying HTTP client (which carries the dmsghttp transport)
// and returns the udp_address advertised by the AR. Returns "" on any
// failure — the caller treats that as "SUDPH not available via this AR".
func (c *httpClient) fetchPublicUDPAddr(httpC *http.Client) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.remoteHTTPAddr+"/health", nil)
	if err != nil {
		c.log.WithError(err).Debug("Failed to build /health request for udp_address lookup")
		return ""
	}
	resp, err := httpC.Do(req)
	if err != nil {
		c.log.WithError(err).Debug("Failed to GET /health for udp_address lookup")
		return ""
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil {
			c.log.WithError(cerr).Debug("Failed to close /health response body")
		}
	}()
	if resp.StatusCode != http.StatusOK {
		c.log.Debugf("/health returned status %d for udp_address lookup", resp.StatusCode)
		return ""
	}
	var body struct {
		UDPAddr string `json:"udp_address"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		c.log.WithError(err).Debug("Failed to decode /health for udp_address lookup")
		return ""
	}
	udpAddr := strings.TrimSpace(body.UDPAddr)
	if udpAddr == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(udpAddr); err != nil {
		// Allow "host" without port; default to AR's well-known SUDPH port.
		udpAddr = net.JoinHostPort(udpAddr, defaultUDPPort)
	}
	return udpAddr
}

// Get performs a new GET request.
func (c *httpClient) Get(ctx context.Context, path string) (*http.Response, error) {
	<-c.ready

	addr := c.httpClient.Addr() + path

	req, err := http.NewRequest(http.MethodGet, addr, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.httpClient.Do(req.WithContext(ctx))
}

// Post performs a POST request.
func (c *httpClient) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
	<-c.ready

	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	addr := c.httpClient.Addr() + path

	req, err := http.NewRequest(http.MethodPost, addr, body)
	if err != nil {
		return nil, err
	}

	return c.httpClient.Do(req.WithContext(ctx))
}

// Delete performs a DELETE request.
func (c *httpClient) Delete(ctx context.Context, path string) (*http.Response, error) {
	<-c.ready
	var payload struct{}
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	addr := c.httpClient.Addr() + path

	req, err := http.NewRequest(http.MethodDelete, addr, body)
	if err != nil {
		return nil, err
	}
	return c.httpClient.Do(req.WithContext(ctx))
}

// BindRequest stores bind request values.
type BindRequest struct {
	Port string `json:"port"`
}

// LocalAddresses contains outbound port and all network addresses of visor.
type LocalAddresses struct {
	Port      string   `json:"port"`
	Addresses []string `json:"addresses"`
	// PublicIP is the visor's STUN- or dmsg-derived public IPv4 address,
	// if known. AR uses this to override the observed source IP when the
	// latter is non-public (e.g., a visor running on the same Docker host
	// as AR reaches it via hairpin SNAT, so AR's UDP socket sees the
	// docker bridge gateway IP — 172.x.y.z — instead of the visor's
	// actual public IP). Old visors that don't set this field leave AR's
	// behavior unchanged: AR falls back to the observed source IP, which
	// is correct for any visor whose path to AR is not NAT'd into a
	// private space. Empty string means "let AR decide."
	//
	// Historical note: pre-#1525 this field carried either v4 or v6
	// indiscriminately. With Phase 2c, the convention is v4-only here;
	// PublicIPv6 carries v6. AR still accepts v6 in this field for
	// backward-compat with pre-Phase-2c visors.
	PublicIP string `json:"public_ip,omitempty"`
	// PublicIPv6 is the visor's declared IPv6 public address. Added in
	// #1525 Phase 2c so visors reaching AR via the dmsg-routed default
	// path (where AR observes the dmsg-bridge's source, not the visor's
	// own family) can still register their v6 endpoint. The AR populates
	// VisorData.RemoteAddrV6 from this field when the observed remote
	// source isn't v6. Empty when the visor is v4-only — preserves the
	// pre-Phase-2c single-stack contract.
	PublicIPv6 string `json:"public_ip_v6,omitempty"`
}

func (c *httpClient) Addresses(_ context.Context) string {
	if c.sudphConn != nil {
		return strings.Split(c.sudphConn.LocalAddr().String(), ":")[3]
	}
	return ""
}

// LocalPublicIP returns the local visor's public IP address.
// This can be used to detect when a remote visor shares the same public IP
// (i.e., is behind the same NAT), allowing LAN addresses to be tried first.
func (c *httpClient) LocalPublicIP() string {
	ip := c.localPublicIPRaw()
	if ip == "" {
		return ""
	}
	// Extract just the IP (without port) from clientPublicIP
	if host, _, err := net.SplitHostPort(ip); err == nil {
		return host
	}
	return ip
}

// localPublicIPRaw returns the declared public IPv4 (possibly "host:port")
// under the lock. Empty until determined.
func (c *httpClient) localPublicIPRaw() string {
	c.pubIPMu.RLock()
	defer c.pubIPMu.RUnlock()
	return c.clientPublicIP
}

// localPublicIPv6Raw returns the declared public IPv6 under the lock.
func (c *httpClient) localPublicIPv6Raw() string {
	c.pubIPMu.RLock()
	defer c.pubIPMu.RUnlock()
	return c.clientPublicIPv6
}

// SetPublicIP records the visor's externally-reachable IPs (determined
// asynchronously by the visor after dmsg/STUN lookups) and unblocks any bind
// waiting on awaitPublicIP. Safe to call once with empty strings to signal
// "determination finished, none available" so binds don't wait the full
// timeout. Idempotent for the ready signal.
func (c *httpClient) SetPublicIP(publicIP, publicIPv6 string) {
	c.pubIPMu.Lock()
	c.clientPublicIP = publicIP
	c.clientPublicIPv6 = publicIPv6
	c.pubIPMu.Unlock()
	c.ipReadyOnce.Do(func() { close(c.ipReady) })
}

// awaitPublicIP blocks until the public IP has been determined (SetPublicIP),
// the client is closed, or the timeout elapses — whichever comes first. It is
// called off the control-plane critical path (only by the bind goroutines), so
// a bounded wait here lets the first registration carry the public IP without
// the RPC ever waiting on dmsg/STUN.
func (c *httpClient) awaitPublicIP(timeout time.Duration) {
	select {
	case <-c.ipReady:
	case <-c.closed:
	case <-time.After(timeout):
	}
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *httpClient) BindSTCPR(ctx context.Context, port string) error {
	log := c.log.WithField("func", "httpClient.BindSTCPR")
	if !c.isReady() {
		log.Debug("Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("Address resolver became ready, binding")
	}

	// The visor determines its public IP asynchronously so the RPC control
	// plane never waits on dmsg/STUN. This bind runs in its own goroutine
	// (off that critical path), so wait — bounded — for the IP to land. If it
	// doesn't arrive in time we bind without it; the AR falls back to the
	// observed source IP and the stcpr re-registration loop carries the real
	// IP on the next pass once it's known.
	c.awaitPublicIP(stcprBindPublicIPWait)

	addresses, err := netutil.LocalAddresses()
	if err != nil {
		return err
	}

	clientPublicIP := c.localPublicIPRaw()
	// Include public IP in addresses list to pass address resolver's hasAddress check
	// when behind NAT (public IP won't be on local interfaces)
	if clientPublicIP != "" {
		// Extract just the IP (without port) from clientPublicIP
		publicIP := clientPublicIP
		if host, _, err := net.SplitHostPort(publicIP); err == nil {
			publicIP = host
		}
		// Add public IP if not already in the list
		found := false
		for _, addr := range addresses {
			if addr == publicIP {
				found = true
				break
			}
		}
		if !found {
			addresses = append(addresses, publicIP)
		}
	}

	localAddresses := LocalAddresses{
		Addresses:  addresses,
		Port:       port,
		PublicIP:   c.LocalPublicIP(),
		PublicIPv6: c.localPublicIPv6Raw(),
	}
	log.Debugf("Address resolver binding with: %v", addresses)
	resp, err := c.Post(ctx, stcprBindPath, localAddresses)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusTooManyRequests {
		return fmt.Errorf("rate limited by address resolver (status 429)")
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauthclient.ExtractError(resp.Body))
	}

	// #1525 Phase 2b: when v6 is available, fire a SECONDARY POST over
	// the v6-forced auth client. The AR's bind handler captures the
	// connecting socket's family via splitFamilyAddr (#2715 Phase 1)
	// and stores RemoteAddrV6 alongside the v4 RemoteAddr we just
	// wrote — both addresses end up in a single VisorData record for
	// peers to Resolve. Best-effort: a v6 POST failure (e.g. AR
	// momentarily unreachable over v6) is logged at debug and doesn't
	// affect the v4 bind result. The 90s refresh cycle re-tries on the
	// next tick. Pre-#1525 v4-only visors (httpClientV6 nil) skip this
	// entirely.
	c.postV6BindSTCPR(ctx, localAddresses, log)

	return nil
}

// postV6BindSTCPR fires the secondary v6 BindSTCPR POST. No-op when
// the v6 client is nil (AR v6-init failed or operator didn't configure
// v6 — same code path as a pre-#1525 build). Errors are logged at
// debug and discarded: the v4 bind's success is the primary outcome
// and a v6 hiccup shouldn't surface as a startup-fatal error.
func (c *httpClient) postV6BindSTCPR(ctx context.Context, payload LocalAddresses, log *logrus.Entry) {
	if c.httpClientV6 == nil {
		return
	}
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		log.WithError(err).Debug("v6 BindSTCPR: encode payload failed")
		return
	}
	addr := c.httpClientV6.Addr() + stcprBindPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, body)
	if err != nil {
		log.WithError(err).Debug("v6 BindSTCPR: build request failed")
		return
	}
	resp, err := c.httpClientV6.Do(req)
	if err != nil {
		log.WithError(err).Debug("v6 BindSTCPR: POST failed (AR v6 transient unreachable; v4 bind already succeeded)")
		return
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		log.WithField("status", resp.StatusCode).Debug("v6 BindSTCPR: non-OK status (v4 bind already succeeded)")
		return
	}
	log.Debug("v6 BindSTCPR: ok — AR will populate RemoteAddrV6")
}

// delBindSTCPR uinbinds STCPR entry PK to IP:port on address resolver.
func (c *httpClient) delBindSTCPR(ctx context.Context) error {
	log := c.log.WithField("func", "httpClient.delBindSTCPR")
	if !c.isReady() {
		log.Debug("Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("Address resolver became ready, unbinding")
	}

	log.Debugf("Deleting the binding pk: %v from Address resolver", c.pk.String())
	resp, err := c.Delete(ctx, stcprBindPath)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauthclient.ExtractError(resp.Body))
	}

	log.Debugf("Deleted bind pk: %v from Address resolver successfully", c.pk.String())
	return nil
}

// Handshake type is used to decouple client from handshake and network packages
type Handshake func(net.Conn) (net.Conn, error)

func (c *httpClient) BindSUDPH(filter *pfilter.PacketFilter, hs Handshake) (<-chan RemoteVisor, error) {
	log := c.log.WithField("func", "httpClient.BindSUDPH")
	if !c.isReady() {
		log.Debug("BindSUDPH: Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("BindSUDPH: Address resolver became ready, binding")
	}

	// First connect must succeed for the caller to consider SUDPH initialized;
	// surface dial / handshake / register failure synchronously.
	arConn, localAddresses, err := c.connectSUDPH(filter, hs)
	if err != nil {
		return nil, err
	}

	c.sudphArConnMu.Lock()
	c.sudphArConn = arConn
	c.sudphArConnMu.Unlock()
	c.sudphLocalAddr = localAddresses

	// addrCh is the long-lived channel returned to the caller. It survives
	// individual arConn instances; reconnects do not close it. It is closed
	// exactly once, on shutdown, by serveSUDPHReconnect's defer.
	addrCh := make(chan RemoteVisor, addrChSize)

	// Single delBindSUDPH goroutine for the lifetime of the client. It
	// blocks until c.closed fires, then writes the unbind packet on
	// whichever arConn is current at that moment.
	c.delBindSudphWg.Add(1)
	go c.delBindSUDPHLoop()

	go c.serveSUDPHReconnect(filter, hs, arConn, addrCh)

	return addrCh, nil
}

// connectSUDPH establishes a fresh KCP+handshake session with the AR's UDP
// listener and posts the initial register payload. Called once on initial
// BindSUDPH and again on every reconnect attempt.
//
// A fresh per-connection packet-filter conn (c.sudphConn) is built on every
// call. kcp client sockets close their underlying conn on Close (see
// xtaci/kcp-go sess.go: a session with no listener closes s.conn), so the
// prior c.sudphConn is already dead by reconnect time and reusing it fails
// every attempt with "use of closed network connection". The local UDP port
// is the SHARED listener's — filter.NewConn does not allocate a new port — so
// it stays stable across rebuilds anyway, both for AR's record of our public
// address and for remote visors that received our (PK, port) tuple via Resolve.
func (c *httpClient) connectSUDPH(filter *pfilter.PacketFilter, hs Handshake) (net.Conn, LocalAddresses, error) {
	if c.remoteUDPAddr == "" {
		// dmsg-only AR with no udp_address in /health. Surface a clear
		// reason so the visor log isn't a confusing UDP-resolve error.
		return nil, LocalAddresses{}, errors.New("AR has no UDP address (dmsg-only AR did not advertise udp_address in /health); SUDPH unavailable")
	}
	rAddr, err := net.ResolveUDPAddr("udp", c.remoteUDPAddr)
	if err != nil {
		return nil, LocalAddresses{}, err
	}

	// Drop any prior (kcp-closed) conn and build a fresh one. The defensive
	// Close is a no-op when kcp already closed it and guards against leaking a
	// filter conn on the rare path where it is still open.
	if c.sudphConn != nil {
		_ = c.sudphConn.Close() //nolint:errcheck,gosec
	}
	c.sudphConn = filter.NewConn(sudphPriority, packetfilter.NewAddressFilter(rAddr, c.mLog))

	_, localPort, err := net.SplitHostPort(c.sudphConn.LocalAddr().String())
	if err != nil {
		return nil, LocalAddresses{}, err
	}

	kcpConn, err := kcp.NewConn(c.remoteUDPAddr, nil, 0, 0, c.sudphConn)
	if err != nil {
		return nil, LocalAddresses{}, err
	}
	arConn, err := hs(kcpConn)
	if err != nil {
		kcpConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	addresses, err := netutil.LocalAddresses()
	if err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	localAddresses := LocalAddresses{
		Addresses:  addresses,
		Port:       localPort,
		PublicIP:   c.LocalPublicIP(),
		PublicIPv6: c.localPublicIPv6Raw(),
	}

	laData, err := json.Marshal(localAddresses)
	if err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	if _, err := arConn.Write(laData); err != nil {
		arConn.Close() //nolint:errcheck,gosec
		return nil, LocalAddresses{}, err
	}

	return arConn, localAddresses, nil
}

// serveSUDPHReconnect runs the per-connection loops (heartbeat / re-register /
// read-and-forward) over arConn and reconnects with backoff when the
// connection dies. The supplied addrCh is held across reconnects so callers
// see a single channel that survives transient AR outages, network blips,
// or KCP session resets. Without this loop, any of those events would
// silently exit the per-connection goroutines, leaving the visor unreachable
// via SUDPH until it was restarted.
func (c *httpClient) serveSUDPHReconnect(filter *pfilter.PacketFilter, hs Handshake, arConn net.Conn, addrCh chan<- RemoteVisor) {
	defer close(addrCh)

	backoff := sudphReconnectInitialBackoff
	for {
		c.serveSUDPHConn(arConn, addrCh)

		if c.isClosed() {
			return
		}

		c.log.Warn("SUDPH connection to address-resolver lost, reconnecting...")
		var newConn net.Conn
		var newLocalAddrs LocalAddresses
		for {
			select {
			case <-c.closed:
				return
			case <-time.After(backoff):
			}
			var err error
			newConn, newLocalAddrs, err = c.connectSUDPH(filter, hs)
			if err == nil {
				break
			}
			c.log.WithError(err).Warn("SUDPH reconnect failed, will retry")
			backoff *= 2
			if backoff > sudphReconnectMaxBackoff {
				backoff = sudphReconnectMaxBackoff
			}
		}
		backoff = sudphReconnectInitialBackoff
		arConn = newConn
		c.sudphArConnMu.Lock()
		c.sudphArConn = arConn
		c.sudphArConnMu.Unlock()
		c.sudphLocalAddr = newLocalAddrs
		c.log.Info("SUDPH reconnected to address-resolver")
	}
}

// serveSUDPHConn runs the read / heartbeat / re-register loops on arConn
// until any of them errors (connection dead) or the client is closed.
// Returns once all three loops have exited.
//
// On the connection-dead path, the first loop to exit closes arConn so the
// other two unblock quickly (read by EOF, write by broken-pipe). On the
// clean-shutdown path (c.closed) we leave arConn open so delBindSUDPHLoop
// can still write the unbind packet; Close() tears down the underlying
// packet listener afterwards, which makes the read loop exit.
func (c *httpClient) serveSUDPHConn(arConn net.Conn, addrCh chan<- RemoteVisor) {
	var (
		wg           sync.WaitGroup
		closeOnce    sync.Once
		closeOnError = func() {
			if c.isClosed() {
				return
			}
			closeOnce.Do(func() { _ = arConn.Close() }) //nolint:errcheck
		}
	)

	wg.Add(3)
	go func() {
		defer wg.Done()
		c.readSUDPHIntoChan(arConn, addrCh)
		closeOnError()
	}()
	go func() {
		defer wg.Done()
		_ = c.keepSudphHeartbeatLoop(arConn) //nolint:errcheck
		closeOnError()
	}()
	go func() {
		defer wg.Done()
		_ = c.sudphReRegisterLoop(arConn) //nolint:errcheck
		closeOnError()
	}()

	wg.Wait()
}

func (c *httpClient) Resolve(ctx context.Context, tType string, pk cipher.PubKey) (VisorData, error) {
	if !c.isReady() {
		return VisorData{}, ErrNotReady
	}

	path := fmt.Sprintf("/resolve/%s/%s", tType, pk.String())

	const maxRetries = 3
	delay := 2 * time.Second

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			c.log.Debugf("Retrying resolve for %s (attempt %d/%d) after %v", pk.String(), attempt+1, maxRetries+1, delay)
			select {
			case <-ctx.Done():
				return VisorData{}, ctx.Err()
			case <-time.After(delay):
			}
			delay *= 2
		}

		resp, err := c.Get(ctx, path)
		if err != nil {
			return VisorData{}, err
		}

		status := resp.StatusCode

		if status == http.StatusTooManyRequests {
			resp.Body.Close() //nolint:errcheck,gosec
			c.log.Warnf("Rate limited by address resolver on resolve for %s, retrying...", pk.String())
			continue
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				c.log.WithError(err).Warn("Failed to close response body")
			}
		}()

		if status == http.StatusNotFound {
			return VisorData{}, ErrNoEntry
		}

		if status != http.StatusOK {
			return VisorData{}, fmt.Errorf("status: %d, error: %w", status, httpauthclient.ExtractError(resp.Body))
		}

		rawBody, err := io.ReadAll(resp.Body)
		if err != nil {
			return VisorData{}, err
		}

		var resolveResp VisorData
		if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
			return VisorData{}, err
		}

		return resolveResp, nil
	}

	return VisorData{}, fmt.Errorf("resolve for %s failed: rate limited after %d attempts", pk.String(), maxRetries+1)
}

// Transports query available transports.
func (c *httpClient) Transports(ctx context.Context) (map[cipher.PubKey][]string, error) {
	resp, err := c.Get(ctx, "/transports")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.log.Warn(ErrNoTransportsFound.Error())
		return nil, ErrNoTransportsFound
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	transportsMap := map[string][]string{}
	if err = json.Unmarshal(body, &transportsMap); err != nil {
		return nil, err
	}

	results := map[cipher.PubKey][]string{}

	for k, pks := range transportsMap {
		for _, pk := range pks {
			rPK := cipher.PubKey{}
			if err := rPK.Set(pk); err != nil {
				c.log.WithError(err).Warn("Invalid public key")
				continue
			}

			// Two kinds of network, SUDPH and STCPR
			if _, ok := results[rPK]; ok {
				if len(results[rPK]) == 1 && k != results[rPK][0] {
					results[rPK] = append(results[rPK], k)
				}
			} else {
				nTypeSlice := make([]string, 0, 2)
				nTypeSlice = append(nTypeSlice, k)
				results[rPK] = nTypeSlice
			}
		}
	}
	return results, nil
}

func (c *httpClient) TransportsType(ctx context.Context, tpType types.Type) (map[cipher.PubKey][]string, error) {
	resp, err := c.Get(ctx, "/transports")
	if err != nil {
		return nil, err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		c.log.Warn(ErrNoTransportsFound.Error())
		return nil, ErrNoTransportsFound
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data struct {
		SUDPH []string `json:"sudph"`
		STCPR []string `json:"stcpr"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, err
	}

	// Select the appropriate key slice
	var keyList []string
	switch tpType {
	case types.SUDPH:
		keyList = data.SUDPH
	case types.STCPR:
		keyList = data.STCPR
	default:
		return nil, fmt.Errorf("unsupported network type: %s", tpType)
	}

	results := make(map[cipher.PubKey][]string)
	for _, pkStr := range keyList {
		var pk cipher.PubKey
		if err := pk.Set(pkStr); err != nil {
			c.log.WithError(err).WithField("pk", pkStr).Warn("Invalid public key")
			continue
		}
		results[pk] = nil
	}

	return results, nil
}

func (c *httpClient) isReady() bool {
	select {
	case <-c.ready:
		return true
	default:
		return false
	}
}

// RemoteVisor contains public key and address of remote visor.
type RemoteVisor struct {
	PK   cipher.PubKey
	Addr string
}

// readSUDPHIntoChan reads framed messages from arConn and forwards parsed
// RemoteVisor entries onto out. Unlike the previous readSUDPHMessages it
// does NOT close out — that channel is owned by serveSUDPHReconnect and
// outlives individual arConn instances across reconnects.
//
// On c.closed, a watcher goroutine forces the blocked Read to return via
// SetReadDeadline (a closed deadline is the only reliable way to interrupt
// a blocked KCP/yamux Read mid-call without closing the underlying conn).
func (c *httpClient) readSUDPHIntoChan(arConn net.Conn, out chan<- RemoteVisor) {
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-c.closed:
			_ = arConn.SetReadDeadline(time.Unix(1, 0)) //nolint:errcheck
		case <-done:
		}
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-c.closed:
			return
		default:
		}

		// Bound the read so a silently-dead AR connection (e.g. the AR
		// process restarted/redeployed) is detected: KCP-on-UDP reads
		// never EOF and our heartbeat writes keep "succeeding", so without
		// this deadline the visor would block here forever, never
		// reconnect, and leave a stale SUDPH entry in the AR. The AR
		// echoes our heartbeats, so a live AR resets this every interval.
		if err := arConn.SetReadDeadline(time.Now().Add(sudphARReadTimeout)); err != nil {
			if !c.isClosed() {
				c.log.Debugf("SUDPH set read deadline failed (will reconnect): %v", err)
			}
			return
		}

		n, err := arConn.Read(buf)
		if err != nil {
			if c.isClosed() {
				c.log.Debugf("SUDPH conn closed on shutdown: %v", err)
			} else {
				c.log.Debugf("SUDPH read error (will reconnect): %v", err)
			}
			return
		}

		// Echoed heartbeat from the AR: a liveness signal only (the
		// successful read above already reset the deadline). Not a
		// RemoteVisor payload, so skip before unmarshalling.
		if string(buf[:n]) == UDPKeepHeartbeatMessage {
			continue
		}

		c.log.Debugf("New SUDPH message: %v", string(buf[:n]))

		var remote RemoteVisor
		if err := json.Unmarshal(buf[:n], &remote); err != nil {
			c.log.Errorf("Failed to unmarshal SUDPH message: %v", err)
			continue
		}

		select {
		case <-c.closed:
			return
		case out <- remote:
		}
	}
}

func (c *httpClient) Close() error {
	select {
	case <-c.closed:
		return nil // already closed
	default: // close
	}

	defer func() {
		c.sudphConn = nil
	}()

	// Signal shutdown. delBindSUDPHLoop (if BindSUDPH was called) and the
	// per-conn read/heartbeat/re-register loops watch this. delBindSudphWg
	// has counter 1 if BindSUDPH ran, 0 otherwise — Wait returns
	// immediately in the latter case.
	close(c.closed)
	c.delBindSudphWg.Wait()

	if c.sudphConn != nil {
		if err := c.sudphConn.Close(); err != nil {
			c.log.WithError(err).Errorf("Failed to close SUDPH")
		}
	}

	hasPublic, err := netutil.HasPublicIP()
	if err != nil {
		c.log.Errorf("Failed to check for public IP: %v", err)
	}
	if hasPublic {
		if err := c.delBindSTCPR(context.Background()); err != nil {
			c.log.WithError(err).Errorf("Failed to delete STCPR binding")
		}
	}

	return nil
}

// Keep NAT mapping alive. Returns on c.closed (clean shutdown) or the
// first Write error (connection dead → caller will reconnect).
func (c *httpClient) keepSudphHeartbeatLoop(w io.Writer) error {
	for {
		select {
		case <-c.closed:
			return nil
		default:
		}
		if _, err := w.Write([]byte(UDPKeepHeartbeatMessage)); err != nil {
			return err
		}
		select {
		case <-c.closed:
			return nil
		case <-time.After(udpKeepHeartbeatInterval):
		}
	}
}

// sudphReRegisterLoop periodically re-registers with address resolver to keep the entry alive.
func (c *httpClient) sudphReRegisterLoop(w io.Writer) error {
	ticker := time.NewTicker(sudphReRegisterInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.closed:
			c.log.Debug("Stopping SUDPH re-registration loop")
			return nil
		case <-ticker.C:
			c.log.Debug("Re-registering SUDPH with address resolver")
			laData, err := json.Marshal(c.sudphLocalAddr)
			if err != nil {
				c.log.WithError(err).Warn("Failed to marshal local addresses for SUDPH re-registration")
				continue
			}
			if _, err := w.Write(laData); err != nil {
				return err
			}
			c.log.Debug("Successfully re-registered SUDPH")
		}
	}
}

// delBindSUDPHLoop is the lifetime-of-client goroutine that sends the
// unbind packet on shutdown. It blocks until c.closed fires, then writes
// to whichever arConn is current at that moment (which may have been
// rotated zero or more times by the reconnect loop). A write failure here
// is non-fatal — if the conn is dead the entry will simply expire via
// AR's TTL instead of being deleted explicitly.
func (c *httpClient) delBindSUDPHLoop() {
	defer c.delBindSudphWg.Done()
	<-c.closed

	c.sudphArConnMu.Lock()
	arConn := c.sudphArConn
	c.sudphArConnMu.Unlock()
	if arConn == nil {
		return
	}
	if _, err := arConn.Write([]byte(UDPDelBindMessage)); err != nil {
		c.log.WithError(err).Debugf("Failed to send UDP unbind packet (entry will TTL out): pk=%v", c.pk.String())
		return
	}
	c.log.WithField("func", "httpClient.delBindSUDPHLoop").Debugf("Deleted bind pk: %v from Address resolver successfully", c.pk.String())
}

func (c *httpClient) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}
