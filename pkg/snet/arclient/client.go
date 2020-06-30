// Package arclient implements address resolver client
package arclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/netutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/xtaci/kcp-go"

	"github.com/SkycoinProject/skywire-mainnet/internal/httpauth"
	"github.com/SkycoinProject/skywire-mainnet/internal/packetfilter"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtp/tpconn"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/directtp/tphandshake"
)

const (
	bindPath             = "/bind/"
	addrChSize           = 1024
	udpKeepAliveInterval = 10 * time.Second
	udpKeepAliveMessage  = "keepalive"
	sudphPriority        = 10
)

var (
	// ErrNoEntry means that there exists no entry for this PK.
	ErrNoEntry = errors.New("no entry for this PK")
	// ErrNotConnected is returned when PK is not connected.
	ErrNotConnected = errors.New("this PK is not connected")
	// ErrUnknownTransportType is returned when transport type is unknown.
	ErrUnknownTransportType = errors.New("unknown transport type")
)

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

//go:generate mockery -name APIClient -case underscore -inpkg

// APIClient implements DMSG discovery API client.
type APIClient interface {
	io.Closer
	BindSTCPR(ctx context.Context, tType, port string) error
	BindSUDPH(ctx context.Context, filter *pfilter.PacketFilter) (<-chan RemoteVisor, error)
	Resolve(ctx context.Context, tType string, pk cipher.PubKey) (VisorData, error)
}

// VisorData stores visor data.
type VisorData struct {
	RemoteAddr string `json:"remote_addr"`
	IsLocal    bool   `json:"is_local,omitempty"`
	LocalAddresses
}

type key struct {
	remoteAddr string
	pk         cipher.PubKey
	sk         cipher.SecKey
}

var clients = make(map[key]*client) // nolint: gochecknoglobals

// client implements Client for address resolver API.
type client struct {
	log           *logging.Logger
	httpClient    *httpauth.Client
	pk            cipher.PubKey
	sk            cipher.SecKey
	remoteUDPAddr string
	sudphConn     net.PacketConn
	sudphAddrCh   chan RemoteVisor
	closed        chan struct{}
}

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key.
// * SW-Nonce:  The nonce for that public key.
// * SW-Sig:    The signature of the payload + the nonce.
func NewHTTP(remoteAddr string, pk cipher.PubKey, sk cipher.SecKey) (APIClient, error) {
	key := key{
		remoteAddr: remoteAddr,
		pk:         pk,
		sk:         sk,
	}

	// Same clients would have nonce collisions. Client should be reused in this case.
	if client, ok := clients[key]; ok {
		return client, nil
	}

	log := logging.MustGetLogger("arclient")

	httpAuthClient, err := httpauth.NewClient(context.Background(), remoteAddr, pk, sk)
	if err != nil {

		log.WithError(err).
			Error("Failed to connect to address resolver. STCPR/SUDPH services are temporarily unavailable. Retrying...")

		retryLog := logging.MustGetLogger("snet.arclient.retrier")
		retry := netutil.NewRetrier(retryLog, 1*time.Second, 10*time.Second, 0, 1)

		err := retry.Do(context.Background(), func() error {
			httpAuthClient, err = httpauth.NewClient(context.Background(), remoteAddr, pk, sk)
			return err
		})

		if err != nil {
			// This should not happen as retries is set to try indefinitely.
			// If address resolver cannot be contacted indefinitely, 'arDone' will be blocked indefinitely.
			log.WithError(err).Fatal("Permanently failed to connect to address resolver.")
		}
	}

	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	client := &client{
		closed:        make(chan struct{}),
		log:           log,
		httpClient:    httpAuthClient,
		pk:            pk,
		sk:            sk,
		remoteUDPAddr: remoteURL.Host,
	}

	clients[key] = client

	return client, nil
}

// Get performs a new GET request.
func (c *client) Get(ctx context.Context, path string) (*http.Response, error) {
	addr := c.httpClient.Addr() + path

	req, err := http.NewRequest(http.MethodGet, addr, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.httpClient.Do(req.WithContext(ctx))
}

// Post performs a POST request.
func (c *client) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
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

// BindRequest stores bind request values.
type BindRequest struct {
	Port string `json:"port"`
}

// LocalAddresses contains outbound port and all network addresses of visor.
type LocalAddresses struct {
	Port      string   `json:"port"`
	Addresses []string `json:"addresses"`
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *client) BindSTCPR(ctx context.Context, tType, port string) error {
	return c.bind(ctx, bindPath+tType, port)
}

func (c *client) bind(ctx context.Context, path string, port string) error {
	addresses, err := localAddresses()
	if err != nil {
		return err
	}

	localAddresses := LocalAddresses{
		Addresses: addresses,
		Port:      port,
	}

	resp, err := c.Post(ctx, path, localAddresses)
	if err != nil {
		return err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			c.log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	return nil
}

func (c *client) Resolve(ctx context.Context, tType string, pk cipher.PubKey) (VisorData, error) {
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
		return VisorData{}, fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return VisorData{}, err
	}

	var resolveResp VisorData

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return VisorData{}, err
	}

	return resolveResp, nil
}

// RemoteVisor contains public key and address of remote visor.
type RemoteVisor struct {
	PK   cipher.PubKey
	Addr string
}

func (c *client) BindSUDPH(ctx context.Context, filter *pfilter.PacketFilter) (<-chan RemoteVisor, error) {
	if c.sudphAddrCh == nil {
		if err := c.initSUDPH(ctx, filter); err != nil {
			return nil, err
		}
	}

	return c.sudphAddrCh, nil
}

func (c *client) initSUDPH(_ context.Context, filter *pfilter.PacketFilter) error {
	rAddr, err := net.ResolveUDPAddr("udp", c.remoteUDPAddr)
	if err != nil {
		return err
	}

	c.sudphConn = filter.NewConn(sudphPriority, packetfilter.NewAddressFilter(rAddr))

	_, localPort, err := net.SplitHostPort(c.sudphConn.LocalAddr().String())
	if err != nil {
		return err
	}

	c.log.Infof("SUDPH Local port: %v", localPort)

	arConn, err := c.wrapConn(c.sudphConn)
	if err != nil {
		return err
	}

	addresses, err := localAddresses()
	if err != nil {
		return err
	}

	localAddresses := LocalAddresses{
		Addresses: addresses,
		Port:      localPort,
	}

	laData, err := json.Marshal(localAddresses)
	if err != nil {
		return err
	}

	if _, err := arConn.Write(laData); err != nil {
		return err
	}

	addrCh := make(chan RemoteVisor, addrChSize)

	go c.readSUDPHMessages(arConn, addrCh)

	go func() {
		if err := c.keepAliveLoop(arConn); err != nil {
			c.log.WithError(err).Errorf("Failed to send keep alive UDP packet to address-resolver")
		}
	}()

	c.sudphAddrCh = addrCh

	return nil
}

func (c *client) readSUDPHMessages(reader io.Reader, addrCh chan<- RemoteVisor) {
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
				c.log.Errorf("Failed to read SUDPH message: %v", err)
				return
			}

			c.log.Infof("New SUDPH message: %v", string(buf[:n]))

			var remote RemoteVisor
			if err := json.Unmarshal(buf[:n], &remote); err != nil {
				c.log.Errorf("Failed to read unmarshal message: %v", err)
				continue
			}

			addrCh <- remote
		}
	}
}

func (c *client) wrapConn(conn net.PacketConn) (*tpconn.Conn, error) {
	arKCPConn, err := kcp.NewConn(c.remoteUDPAddr, nil, 0, 0, conn)
	if err != nil {
		return nil, err
	}

	emptyAddr := dmsg.Addr{PK: cipher.PubKey{}, Port: 0}
	hs := tphandshake.InitiatorHandshake(c.sk, dmsg.Addr{PK: c.pk, Port: 0}, emptyAddr)

	connConfig := tpconn.Config{
		Log:       c.log,
		Conn:      arKCPConn,
		LocalPK:   c.pk,
		LocalSK:   c.sk,
		Deadline:  time.Now().Add(tphandshake.Timeout),
		Handshake: hs,
		Encrypt:   false,
		Initiator: true,
	}

	arConn, err := tpconn.NewConn(connConfig)
	if err != nil {
		return nil, fmt.Errorf("newConn: %w", err)
	}

	return arConn, nil
}

func (c *client) Close() error {
	select {
	case <-c.closed:
		return nil // already closed
	default: // close
	}

	defer func() {
		c.sudphConn = nil
	}()

	if c.sudphConn != nil {
		if err := c.sudphConn.Close(); err != nil {
			c.log.WithError(err).Errorf("Failed to close SUDPH")
		}
	}

	close(c.closed)

	return nil
}

// Keep NAT mapping alive.
func (c *client) keepAliveLoop(w io.Writer) error {
	for {
		select {
		case <-c.closed:
			return nil
		default:
			if _, err := w.Write([]byte(udpKeepAliveMessage)); err != nil {
				return err
			}

			time.Sleep(udpKeepAliveInterval)
		}
	}
}

// extractError returns the decoded error message from Body.
func extractError(r io.Reader) error {
	var apiError Error

	body, err := ioutil.ReadAll(r)
	if err != nil {
		return err
	}

	if err := json.Unmarshal(body, &apiError); err != nil {
		return errors.New(string(body))
	}

	return errors.New(apiError.Error)
}

func localAddresses() ([]string, error) {
	result := make([]string, 0)

	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}

	for _, addr := range addresses {
		switch v := addr.(type) {
		case *net.IPNet:
			if v.IP.IsGlobalUnicast() || v.IP.IsLoopback() {
				result = append(result, v.IP.String())
			}
		case *net.IPAddr:
			if v.IP.IsGlobalUnicast() || v.IP.IsLoopback() {
				result = append(result, v.IP.String())
			}
		}
	}

	return result, nil
}
