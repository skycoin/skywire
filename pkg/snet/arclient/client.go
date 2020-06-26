// Package arclient implements address resolver client
// TODO(nkryuchkov): move to internal
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
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/libp2p/go-reuseport"
	"nhooyr.io/websocket"

	"github.com/SkycoinProject/skywire-mainnet/internal/httpauth"
)

var log = logging.MustGetLogger("arclient")

const (
	bindSTCPRPath        = "/bind/stcpr"
	bindSUDPRPath        = "/bind/sudpr"
	resolveSTCPRPath     = "/resolve/"
	resolveSTCPHPath     = "/resolve_hole_punch/"
	resolveSUDPRPath     = "/resolve_sudpr/"
	resolveSUDPHPath     = "/resolve_sudph/"
	wsPath               = "/ws"
	addrChSize           = 1024
	udpKeepAliveInterval = 10 * time.Second
	udpKeepAliveMessage  = "keepalive"
)

var (
	// ErrNoEntry means that there exists no entry for this PK.
	ErrNoEntry = errors.New("no entry for this PK")
	// ErrNotConnected is returned when PK is not connected.
	ErrNotConnected = errors.New("this PK is not connected")
)

// Error is the object returned to the client when there's an error.
type Error struct {
	Error string `json:"error"`
}

// APIClient implements DMSG discovery API client.
type APIClient interface {
	io.Closer
	LocalTCPAddr() string
	LocalUDPAddr() string
	RemoteHTTPAddr() string
	RemoteUDPAddr() string
	BindSTCPR(ctx context.Context, port string) error
	BindSTCPH(ctx context.Context, dialCh <-chan cipher.PubKey) (<-chan RemoteVisor, error)
	BindSUDPR(ctx context.Context, port string) error
	BindSUDPH(ctx context.Context, conn net.Conn, localPort string) (<-chan RemoteVisor, error)
	ResolveSTCPR(ctx context.Context, pk cipher.PubKey) (VisorData, error)
	ResolveSTCPH(ctx context.Context, pk cipher.PubKey) (VisorData, error)
	ResolveSUDPR(ctx context.Context, pk cipher.PubKey) (VisorData, error)
	ResolveSUDPH(ctx context.Context, pk cipher.PubKey) (VisorData, error)
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

var clients = make(map[key]*client)

// client implements Client for address resolver API.
type client struct {
	client         *httpauth.Client
	localTCPAddr   string
	localUDPAddr   string
	remoteHTTPAddr string
	remoteUDPAddr  string
	pk             cipher.PubKey
	sk             cipher.SecKey
	stcphConn      *websocket.Conn
	stcphAddrCh    <-chan RemoteVisor
	sudphConn      *net.UDPConn
	sudphAddrCh    <-chan RemoteVisor
	filterConn     *pfilter.PacketFilter
	visorConn      net.PacketConn
	arConn         net.PacketConn
}

func (c *client) RemoteHTTPAddr() string {
	return c.remoteHTTPAddr
}

func (c *client) RemoteUDPAddr() string {
	return c.remoteUDPAddr
}

// NewHTTP creates a new client setting a public key to the client to be used for auth.
// When keys are set, the client will sign request before submitting.
// The signature information is transmitted in the header using:
// * SW-Public: The specified public key
// * SW-Nonce:  The nonce for that public key
// * SW-Sig:    The signature of the payload + the nonce
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

	httpAuthClient, err := httpauth.NewClient(context.Background(), remoteAddr, pk, sk)
	if err != nil {
		return nil, fmt.Errorf("address resolver httpauth: %w", err)
	}

	remoteURL, err := url.Parse(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("parse URL: %w", err)
	}

	client := &client{
		client:         httpAuthClient,
		pk:             pk,
		sk:             sk,
		remoteHTTPAddr: remoteAddr,
		remoteUDPAddr:  remoteURL.Host,
	}

	transport := &http.Transport{
		DialContext: func(_ context.Context, network, remoteAddr string) (conn net.Conn, err error) {
			conn, err = reuseport.Dial(network, client.localTCPAddr, remoteAddr)
			if err == nil && client.localTCPAddr == "" {
				client.localTCPAddr = conn.LocalAddr().String()
			}

			return conn, err
		},
		DisableKeepAlives: false,
	}

	httpAuthClient.SetTransport(transport)

	clients[key] = client

	return client, nil
}

func (c *client) LocalTCPAddr() string {
	return c.localTCPAddr
}

func (c *client) LocalUDPAddr() string {
	return c.localUDPAddr
}

// Get performs a new GET request.
func (c *client) Get(ctx context.Context, path string) (*http.Response, error) {
	addr := c.client.Addr() + path

	req, err := http.NewRequest(http.MethodGet, addr, new(bytes.Buffer))
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// Post performs a POST request.
func (c *client) Post(ctx context.Context, path string, payload interface{}) (*http.Response, error) {
	body := bytes.NewBuffer(nil)
	if err := json.NewEncoder(body).Encode(payload); err != nil {
		return nil, err
	}

	addr := c.client.Addr() + path

	req, err := http.NewRequest(http.MethodPost, addr, body)
	if err != nil {
		return nil, err
	}

	return c.client.Do(req.WithContext(ctx))
}

// Websocket performs a new websocket request.
func (c *client) Websocket(ctx context.Context, path string) (*websocket.Conn, error) {
	header, err := c.client.Header()
	if err != nil {
		return nil, err
	}

	dialOpts := &websocket.DialOptions{
		HTTPClient: c.client.ReuseClient(),
		HTTPHeader: header,
	}

	addr, err := url.Parse(c.client.Addr())
	if err != nil {
		return nil, err
	}
	switch addr.Scheme {
	case "http":
		addr.Scheme = "ws"
	case "https":
		addr.Scheme = "wss"
	}

	addr.Path = path

	conn, resp, err := websocket.Dial(ctx, addr.String(), dialOpts)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusOK {
		c.client.IncrementNonce()
	}

	return conn, nil
}

// BindRequest stores bind request values.
type BindRequest struct {
	Port string `json:"port"`
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *client) BindSTCPR(ctx context.Context, port string) error {
	return c.bind(ctx, bindSTCPRPath, port)
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *client) BindSUDPR(ctx context.Context, port string) error {
	return c.bind(ctx, bindSUDPRPath, port)
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
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	return nil
}

func (c *client) ResolveSTCPR(ctx context.Context, pk cipher.PubKey) (VisorData, error) {
	return c.resolve(ctx, resolveSTCPRPath, pk)
}

func (c *client) ResolveSTCPH(ctx context.Context, pk cipher.PubKey) (VisorData, error) {
	return c.resolve(ctx, resolveSTCPHPath, pk)
}

func (c *client) ResolveSUDPR(ctx context.Context, pk cipher.PubKey) (VisorData, error) {
	return c.resolve(ctx, resolveSUDPRPath, pk)
}

func (c *client) ResolveSUDPH(ctx context.Context, pk cipher.PubKey) (VisorData, error) {
	return c.resolve(ctx, resolveSUDPHPath, pk)
}

func (c *client) resolve(ctx context.Context, path string, pk cipher.PubKey) (VisorData, error) {
	resp, err := c.Get(ctx, path+pk.String())
	if err != nil {
		return VisorData{}, err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
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

func (c *client) BindSTCPH(ctx context.Context, dialCh <-chan cipher.PubKey) (<-chan RemoteVisor, error) {
	if c.stcphAddrCh == nil {
		if err := c.initSTCPH(ctx, dialCh); err != nil {
			return nil, err
		}
	}

	return c.stcphAddrCh, nil
}

// TODO(nkryuchkov): Ensure this works correctly with closed channels and connections.
func (c *client) initSTCPH(ctx context.Context, dialCh <-chan cipher.PubKey) error {
	conn, err := c.Websocket(ctx, wsPath)
	if err != nil {
		return err
	}

	_, localPort, err := net.SplitHostPort(c.LocalTCPAddr())
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

	if err := conn.Write(ctx, websocket.MessageText, laData); err != nil {
		return err
	}

	c.stcphConn = conn
	addrCh := make(chan RemoteVisor, addrChSize)

	go func(conn *websocket.Conn, addrCh chan<- RemoteVisor) {
		defer func() {
			close(addrCh)
		}()

		for {
			kind, rawMsg, err := conn.Read(context.TODO())
			if err != nil {
				log.Errorf("Failed to read WS message: %v", err)
				return
			}

			log.Infof("New WS message of type %v: %v", kind.String(), string(rawMsg))

			var remote RemoteVisor
			if err := json.Unmarshal(rawMsg, &remote); err != nil {
				log.Errorf("Failed to read unmarshal message: %v", err)
				continue
			}

			addrCh <- remote
		}
	}(conn, addrCh)

	go func(conn *websocket.Conn, dialCh <-chan cipher.PubKey) {
		for pk := range dialCh {
			if err := conn.Write(ctx, websocket.MessageText, []byte(pk.String())); err != nil {
				log.Errorf("Failed to write to %v: %v", pk, err)
				return
			}
		}
	}(conn, dialCh)

	c.stcphAddrCh = addrCh

	return nil
}

type LocalAddresses struct {
	Port      string   `json:"port"`
	Addresses []string `json:"addresses"`
}

func (c *client) BindSUDPH(ctx context.Context, conn net.Conn, localPort string) (<-chan RemoteVisor, error) {
	if c.sudphAddrCh == nil {
		if err := c.initSUDPH(ctx, conn, localPort); err != nil {
			return nil, err
		}
	}

	return c.sudphAddrCh, nil
}

func (c *client) initSUDPH(ctx context.Context, conn net.Conn, localPort string) error {
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

	if _, err := conn.Write(laData); err != nil {
		return err
	}

	addrCh := make(chan RemoteVisor, addrChSize)

	go func(conn net.Conn, addrCh chan<- RemoteVisor) {
		defer func() {
			close(addrCh)
		}()

		buf := make([]byte, 4096)

		for {
			n, err := conn.Read(buf)
			if err != nil {
				log.Errorf("Failed to read SUDPH message: %v", err)
				return
			}

			log.Infof("New SUDPH message: %v", string(buf[:n]))

			var remote RemoteVisor
			if err := json.Unmarshal(buf[:n], &remote); err != nil {
				log.Errorf("Failed to read unmarshal message: %v", err)
				continue
			}

			addrCh <- remote
		}
	}(conn, addrCh)

	go func() {
		if err := c.keepAliveLoop(ctx, conn); err != nil {
			log.WithError(err).Errorf("Failed to send keep alive UDP packet to address-resolver")
		}
	}()

	c.sudphAddrCh = addrCh

	return nil
}

func (c *client) Close() error {
	defer func() {
		c.stcphConn = nil
		// TODO(nkryuchkov): uncomment
		// c.sudphConn = nil
	}()

	if c.stcphConn != nil {
		if err := c.stcphConn.Close(websocket.StatusNormalClosure, "client closed"); err != nil {
			log.WithError(err).Errorf("Failed to close STCPH")
		}
	}

	// TODO(nkryuchkov): uncomment, check if nil
	// if err := c.sudphConn.Close(); err != nil {
	// 	log.WithError(err).Errorf("Failed to close SUDPH")
	// }

	return nil
}

// keep NAT mapping alive
func (c *client) keepAliveLoop(ctx context.Context, conn net.Conn) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if _, err := conn.Write([]byte(udpKeepAliveMessage)); err != nil {
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
