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
	"github.com/xtaci/kcp-go"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/internal/httpauth"
	"github.com/skycoin/skywire/internal/packetfilter"
)

const (
	// sudphPriority is used to set an order how connection filters apply.
	sudphPriority            = 1
	stcprBindPath            = "/bind/stcpr"
	addrChSize               = 1024
	udpKeepHeartbeatInterval = 10 * time.Second
	udpKeepHeartbeatMessage  = "heartbeat"
	defaultUDPPort           = "30178"
	// UDPDelBindMessage is used as a deletebind packet on visor shutdown.
	UDPDelBindMessage = "delBind"
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

//go:generate mockery -name APIClient -case underscore -inpkg

// APIClient implements address resolver API client.
type APIClient interface {
	BindSTCPR(ctx context.Context, port string) error
	BindSUDPH(filter *pfilter.PacketFilter, handshake Handshake) (<-chan RemoteVisor, error)
	Resolve(ctx context.Context, netType string, pk cipher.PubKey) (VisorData, error)
	Transports(ctx context.Context) (map[cipher.PubKey][]string, error)
	Addresses(ctx context.Context) string
	Close() error
}

// VisorData stores visor data.
type VisorData struct {
	RemoteAddr string `json:"remote_addr"`
	IsLocal    bool   `json:"is_local,omitempty"`
	LocalAddresses
}

// httpClient implements APIClient for address resolver API.
type httpClient struct {
	log            *logging.Logger
	mLog           *logging.MasterLogger
	httpClient     *httpauth.Client
	pk             cipher.PubKey
	sk             cipher.SecKey
	remoteHTTPAddr string
	remoteUDPAddr  string
	sudphConn      net.PacketConn
	clientPublicIP string
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
func NewHTTP(remoteAddr string, pk cipher.PubKey, sk cipher.SecKey, httpC *http.Client, clientPublicIP string, log *logging.Logger,
	mLog *logging.MasterLogger) (APIClient, error) {
	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	remoteUDP := remoteURL.Host
	if _, _, err := net.SplitHostPort(remoteUDP); err != nil {
		remoteUDP = net.JoinHostPort(remoteUDP, defaultUDPPort)
	}

	client := &httpClient{
		log:            log,
		mLog:           mLog,
		pk:             pk,
		sk:             sk,
		remoteHTTPAddr: remoteAddr,
		remoteUDPAddr:  remoteUDP,
		clientPublicIP: clientPublicIP,
		ready:          make(chan struct{}),
		closed:         make(chan struct{}),
	}

	client.log.Debugf("Remote UDP server: %q", remoteUDP)

	go client.initHTTPClient(httpC)

	return client, nil
}

func (c *httpClient) initHTTPClient(httpC *http.Client) {
	httpAuthClient, err := httpauth.NewClient(context.Background(), c.remoteHTTPAddr, c.pk, c.sk, httpC, c.clientPublicIP, c.mLog)
	if err != nil {
		c.log.WithError(err).
			Warnf("Failed to connect to address resolver. STCPR/SUDPH services are temporarily unavailable. Retrying...")

		retry := netutil.NewRetrier(c.log, 1*time.Second, 10*time.Second, 0, 1)

		err := retry.Do(context.Background(), func() error {
			httpAuthClient, err = httpauth.NewClient(context.Background(), c.remoteHTTPAddr, c.pk, c.sk, httpC, c.clientPublicIP, c.mLog)
			return err
		})

		if err != nil {
			// This should not happen as retrier is set to try indefinitely.
			// If address resolver cannot be contacted indefinitely, 'c.ready' will be blocked indefinitely.
			c.log.WithError(err).Fatal("Permanently failed to connect to address resolver.")
		}
	}

	c.log.Debug("Connected to address resolver. STCPR/SUDPH services are available.")

	c.httpClient = httpAuthClient
	close(c.ready)
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
}

func (c *httpClient) Addresses(ctx context.Context) string {
	if c.sudphConn != nil {
		return strings.Split(c.sudphConn.LocalAddr().String(), ":")[3]
	}
	return ""
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *httpClient) BindSTCPR(ctx context.Context, port string) error {
	log := c.log.WithField("func", "httpClient.BindSTCPR")
	if !c.isReady() {
		log.Debug("Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("Address resolver became ready, binding")
	}

	addresses, err := netutil.LocalAddresses()
	if err != nil {
		return err
	}

	localAddresses := LocalAddresses{
		Addresses: addresses,
		Port:      port,
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

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauth.ExtractError(resp.Body))
	}

	return nil
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
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauth.ExtractError(resp.Body))
	}

	log.Debugf("Deleted bind pk: %v from Address resolver successfully", c.pk.String())
	return nil
}

// Handshake type is used to decouple client from handshake and network packages
type Handshake func(net.Conn) (net.Conn, error)

func (c *httpClient) BindSUDPH(filter *pfilter.PacketFilter, hs Handshake) (<-chan RemoteVisor, error) {
	log := c.log.WithField("func", "httpClient.BindSUDPR")
	if !c.isReady() {
		log.Debug("BindSUDPR: Address resolver is not ready yet, waiting...")
		<-c.ready
		log.Debug("BindSUDPR: Address resolver became ready, binding")
	}

	rAddr, err := net.ResolveUDPAddr("udp", c.remoteUDPAddr)
	if err != nil {
		return nil, err
	}

	c.sudphConn = filter.NewConn(sudphPriority, packetfilter.NewAddressFilter(rAddr, c.mLog))

	_, localPort, err := net.SplitHostPort(c.sudphConn.LocalAddr().String())
	if err != nil {
		return nil, err
	}

	log.Debugf("SUDPH Local port: %v", localPort)
	kcpConn, err := kcp.NewConn(c.remoteUDPAddr, nil, 0, 0, c.sudphConn)
	if err != nil {
		return nil, err
	}
	arConn, err := hs(kcpConn)
	if err != nil {
		return nil, err
	}

	addresses, err := netutil.LocalAddresses()
	if err != nil {
		return nil, err
	}

	localAddresses := LocalAddresses{
		Addresses: addresses,
		Port:      localPort,
	}

	laData, err := json.Marshal(localAddresses)
	if err != nil {
		return nil, err
	}

	if _, err := arConn.Write(laData); err != nil {
		return nil, err
	}

	addrCh := c.readSUDPHMessages(arConn)

	go func() {
		if err := c.keepSudphHeartbeatLoop(arConn); err != nil {
			log.WithError(err).Errorf("Failed to send UDP heartbeat packet to address-resolver")
		}
	}()

	go func() {
		if err := c.delBindSUDPH(arConn); err != nil {
			log.WithError(err).Errorf("Failed to send UDP unbind packet to address-resolver")
		}
	}()

	return addrCh, nil
}

func (c *httpClient) Resolve(ctx context.Context, tType string, pk cipher.PubKey) (VisorData, error) {
	if !c.isReady() {
		return VisorData{}, ErrNotReady
	}

	path := fmt.Sprintf("/resolve/%s/%s", tType, pk.String())

	resp, err := c.Get(ctx, path)
	if err != nil {
		return VisorData{}, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return VisorData{}, ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return VisorData{}, fmt.Errorf("status: %d, error: %w", resp.StatusCode, httpauth.ExtractError(resp.Body))
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
				c.log.WithError(err).Warn("unable to transform PK")
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

func (c *httpClient) readSUDPHMessages(reader io.Reader) <-chan RemoteVisor {
	addrCh := make(chan RemoteVisor, addrChSize)

	go func(addrCh chan<- RemoteVisor) {
		defer func() {
			close(addrCh)
		}()

		buf := make([]byte, 4096)

		for {
			select {
			case <-c.closed:
				return
			default:
				n, err := reader.Read(buf)
				if err != nil {
					if c.isClosed() {
						c.log.Debugf("SUDPH conn closed on shutdown message: %v", err)
						return
					}
					c.log.Errorf("Failed to read SUDPH message: %v", err)
					return
				}

				c.log.Debugf("New SUDPH message: %v", string(buf[:n]))

				var remote RemoteVisor
				if err := json.Unmarshal(buf[:n], &remote); err != nil {
					c.log.Errorf("Failed to read unmarshal message: %v", err)
					continue
				}

				addrCh <- remote
			}
		}
	}(addrCh)

	return addrCh
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

	if c.sudphConn != nil {
		c.delBindSudphWg.Add(1)
		close(c.closed)
		c.delBindSudphWg.Wait()
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

// Keep NAT mapping alive.
func (c *httpClient) keepSudphHeartbeatLoop(w io.Writer) error {
	for {
		select {
		case <-c.closed:
			return nil
		default:
			if _, err := w.Write([]byte(udpKeepHeartbeatMessage)); err != nil {
				return err
			}
			time.Sleep(udpKeepHeartbeatInterval)
		}
	}
}

// delBindSUDPH unbinds SUDPH entry in address resolver.
func (c *httpClient) delBindSUDPH(w io.Writer) error {
	// send unbind packet on shutdown
	<-c.closed
	defer c.delBindSudphWg.Done()
	if _, err := w.Write([]byte(UDPDelBindMessage)); err != nil {
		return err
	}
	c.log.WithField("func", "httpClient.delBindSUDPH").Debugf("Deleted bind pk: %v from Address resolver successfully", c.pk.String())

	return nil
}

func (c *httpClient) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}
