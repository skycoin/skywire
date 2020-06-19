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
	"strings"

	"github.com/AudriusButkevicius/pfilter"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/libp2p/go-reuseport"
	"github.com/xtaci/kcp-go"
	"nhooyr.io/websocket"

	"github.com/SkycoinProject/skywire-mainnet/internal/httpauth"
	"github.com/SkycoinProject/skywire-mainnet/internal/packetfilter"
)

var log = logging.MustGetLogger("arclient")

const (
	bindSTCPRPath    = "/bind/stcpr"
	bindSUDPRPath    = "/bind/sudpr"
	resolveSTCPRPath = "/resolve/"
	resolveSTCPHPath = "/resolve_hole_punch/"
	resolveSUDPRPath = "/resolve_sudpr/"
	resolveSUDPHPath = "/resolve_sudph/"
	wsPath           = "/ws"
	addrChSize       = 1024
	handshakeMessage = "handshake"
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
	LocalAddr() string
	BindSTCPR(ctx context.Context, port string) error
	BindSUDPR(ctx context.Context, port string) error
	BindSTCPH(ctx context.Context, dialCh <-chan cipher.PubKey) (<-chan RemoteVisor, error)
	BindSUDPH(ctx context.Context, dialCh <-chan cipher.PubKey) (<-chan RemoteVisor, error)
	ResolveSTCPR(ctx context.Context, pk cipher.PubKey) (string, error)
	ResolveSTCPH(ctx context.Context, pk cipher.PubKey) (string, error)
	ResolveSUDPR(ctx context.Context, pk cipher.PubKey) (string, error)
	ResolveSUDPH(ctx context.Context, pk cipher.PubKey) (string, error)
	DialUDP(addr string) (net.Conn, error)
	VisorConn() net.PacketConn
}

type key struct {
	remoteAddr string
	pk         cipher.PubKey
	sk         cipher.SecKey
}

var clients = make(map[key]*client)

// client implements Client for address resolver API.
type client struct {
	client       *httpauth.Client
	localTCPAddr string
	localUDPAddr string
	remoteAddr   string
	pk           cipher.PubKey
	sk           cipher.SecKey
	stcphConn    *websocket.Conn
	stcphAddrCh  <-chan RemoteVisor
	sudphConn    *net.UDPConn
	filterConn   *pfilter.PacketFilter
	visorConn    net.PacketConn
	arConn       net.PacketConn
	sudphAddrCh  <-chan RemoteVisor
}

func (c *client) VisorConn() net.PacketConn {
	return c.visorConn
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

	log.Infof("Creating arclient, key = %v", key)

	httpAuthClient, err := httpauth.NewClient(context.Background(), remoteAddr, pk, sk)
	if err != nil {
		return nil, fmt.Errorf("address resolver httpauth: %w", err)
	}

	client := &client{
		client:       httpAuthClient,
		pk:           pk,
		sk:           sk,
		localTCPAddr: "",
		localUDPAddr: "",
		remoteAddr:   remoteAddr,
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

func (c *client) LocalAddr() string {
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

// UDP performs a new UDP request.
func (c *client) ConnectToUDPServer(_ context.Context) (net.Conn, error) {
	log.Infof("SUDPH: UDP")

	remoteAddr := c.remoteAddr
	remoteAddr = strings.TrimPrefix(remoteAddr, "http://")
	remoteAddr = strings.TrimPrefix(remoteAddr, "https://")

	// log.Infof("SUDPH remote addr before trim %q, after %q", c.remoteAddr, remoteAddr)

	remoteAddr = "127.0.0.1:9099"

	conn, err := c.DialUDP(remoteAddr)
	if err != nil {
		return nil, fmt.Errorf("DialUDP: %w", err)
	}

	return conn, nil
}

// TODO(nkryuchkov): remove from arclient
func (c *client) DialUDP(remoteAddr string) (net.Conn, error) {
	log.Infof("SUDPH c.localUDPAddr: %q", c.localUDPAddr)

	var lAddr *net.UDPAddr
	if c.localUDPAddr != "" {
		la, err := net.ResolveUDPAddr("udp", c.localUDPAddr)
		if err != nil {
			return nil, fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
		}

		lAddr = la
		log.Infof("SUDPH: Resolved local addr from %v to %v", c.localUDPAddr, lAddr)
	} else {
		la, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
		if err != nil {
			return nil, fmt.Errorf("net.ResolveUDPAddr (local): %w", err)
		}

		lAddr = la
		log.Infof("SUDPH: Resolved local addr from %v to %v", "127.0.0.1:0", lAddr)
	}

	// rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	// if err != nil {
	// 	return nil, fmt.Errorf("net.ResolveUDPAddr (remote): %w", err)
	// }

	// udpConn, err := net.DialUDP("udp", lAddr, rAddr)
	// if err != nil {
	// 	return nil, fmt.Errorf("net.DialUDP: %w", err)
	// }
	//
	// log.Infof("SUDPH local addr: %q", udpConn.LocalAddr())
	//
	// conn, err := kcp.NewConn(remoteAddr, nil, 0, 0, udpConn)
	// if err != nil {
	// 	return nil, fmt.Errorf("kcp.NewConn: %w", err)
	// }

	rAddr, err := net.ResolveUDPAddr("udp", remoteAddr)
	if err != nil {
		return nil, err
	}

	network := "udp4"
	if rAddr.IP.To4() == nil {
		network = "udp"
	}

	log.Infof("SUDPH dialing udp from %v to %v", lAddr, remoteAddr)

	if c.sudphConn == nil {
		uc, err := net.ListenUDP(network, lAddr)
		if err != nil {
			return nil, err
		}

		c.sudphConn = uc

		c.filterConn = pfilter.NewPacketFilter(uc)
		c.visorConn = c.filterConn.NewConn(100, packetfilter.NewAddressFilter(rAddr, false))
		c.arConn = c.filterConn.NewConn(10, packetfilter.NewAddressFilter(rAddr, true))

		c.filterConn.Start()
	}

	// conn, err := net.DialUDP(network, lAddr, rAddr)
	// if err != nil {
	// 	return nil, err
	// }

	conn2, err := kcp.NewConn(remoteAddr, nil, 0, 0, c.arConn)
	if err != nil {
		return nil, err
	}

	// _, err = conn.Write([]byte("test"))
	// if err != nil {
	// 	panic(err)
	// }

	// log.Infof("test stacktrace: %v", string(debug.Stack()))

	if c.localUDPAddr == "" {
		log.Infof("SUDPH updating local UDP addr from %v to %v", c.localUDPAddr, conn2.LocalAddr().String())
		c.localUDPAddr = conn2.LocalAddr().String()
	}

	return conn2, nil
}

// BindRequest stores bind request values.
type BindRequest struct {
	Port string `json:"port"`
}

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *client) BindSTCPR(ctx context.Context, port string) error {
	req := BindRequest{
		Port: port,
	}

	resp, err := c.Post(ctx, bindSTCPRPath, req)
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

// BindSTCPR binds client PK to IP:port on address resolver.
func (c *client) BindSUDPR(ctx context.Context, port string) error {
	req := BindRequest{
		Port: port,
	}

	resp, err := c.Post(ctx, bindSUDPRPath, req)
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

// ResolveResponse stores response response values.
type ResolveResponse struct {
	Addr string `json:"addr"`
}

func (c *client) ResolveSTCPR(ctx context.Context, pk cipher.PubKey) (string, error) {
	resp, err := c.Get(ctx, resolveSTCPRPath+pk.String())
	if err != nil {
		return "", err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var resolveResp ResolveResponse

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return "", err
	}

	return resolveResp.Addr, nil
}

func (c *client) ResolveSTCPH(ctx context.Context, pk cipher.PubKey) (string, error) {
	resp, err := c.Get(ctx, resolveSTCPHPath+pk.String())
	if err != nil {
		return "", err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var resolveResp ResolveResponse

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return "", err
	}

	return resolveResp.Addr, nil
}

func (c *client) ResolveSUDPR(ctx context.Context, pk cipher.PubKey) (string, error) {
	resp, err := c.Get(ctx, resolveSUDPRPath+pk.String())
	if err != nil {
		return "", err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var resolveResp ResolveResponse

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return "", err
	}

	return resolveResp.Addr, nil
}

func (c *client) ResolveSUDPH(ctx context.Context, pk cipher.PubKey) (string, error) {
	resp, err := c.Get(ctx, resolveSUDPHPath+pk.String())
	if err != nil {
		return "", err
	}

	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("Failed to close response body")
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return "", ErrNoEntry
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status: %d, error: %w", resp.StatusCode, extractError(resp.Body))
	}

	rawBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var resolveResp ResolveResponse

	if err := json.Unmarshal(rawBody, &resolveResp); err != nil {
		return "", err
	}

	return resolveResp.Addr, nil
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

func (c *client) BindSUDPH(ctx context.Context, dialCh <-chan cipher.PubKey) (<-chan RemoteVisor, error) {
	log.Infof("Bind SUDPH")
	if c.sudphAddrCh == nil {
		log.Infof("Init SUDPH")
		if err := c.initSUDPH(ctx, dialCh); err != nil {
			return nil, err
		}
	}

	return c.sudphAddrCh, nil
}

func (c *client) initSTCPH(ctx context.Context, dialCh <-chan cipher.PubKey) error {
	// TODO(nkryuchkov): Ensure this works correctly with closed channels and connections.
	addrCh := make(chan RemoteVisor, addrChSize)

	conn, err := c.Websocket(ctx, wsPath)
	if err != nil {
		return err
	}

	c.stcphConn = conn

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

func (c *client) initSUDPH(ctx context.Context, dialCh <-chan cipher.PubKey) error {
	log.Infof("Init SUDPH [2]")

	// TODO(nkryuchkov): Ensure this works correctly with closed channels and connections.
	addrCh := make(chan RemoteVisor, addrChSize)

	conn, err := c.ConnectToUDPServer(ctx)
	if err != nil {
		return err
	}

	// log.Infof("Sending handshake")
	// if _, err := conn.Write([]byte(handshakeMessage)); err != nil {
	// 	return err
	// }
	// log.Infof("Sent handshake")

	log.Infof("Sending PK")
	if _, err := conn.Write([]byte(c.pk.String())); err != nil {
		return err
	}
	log.Infof("Sent PK")

	go func(conn net.Conn, addrCh chan<- RemoteVisor) {
		defer func() {
			close(addrCh)
		}()

		buf := make([]byte, 4096)
		for {
			log.Infof("Reading incoming message")
			n, err := conn.Read(buf)
			if err != nil {
				log.Errorf("Failed to read UDP message: %v", err)
				return
			}
			log.Infof("Read incoming message")

			data := buf[:n]

			log.Infof("New UDP message from %v: %v", conn.RemoteAddr(), string(data))

			var remote RemoteVisor
			if err := json.Unmarshal(data, &remote); err != nil {
				log.Errorf("Failed to read unmarshal message: %v", err)
				continue
			}

			addrCh <- remote
		}
	}(conn, addrCh)

	go func(conn net.Conn, dialCh <-chan cipher.PubKey) {
		for pk := range dialCh {
			log.Infof("Sending signal to dial %v", pk)
			if _, err := conn.Write([]byte(pk.String())); err != nil {
				log.Errorf("Failed to write to %v: %v", pk, err)
				return
			}
			log.Infof("Sent signal to dial %v", pk)
		}
	}(conn, dialCh)

	c.sudphAddrCh = addrCh

	return nil
}

func (c *client) Close() error {
	defer func() {
		c.stcphConn = nil
		// TODO: uncomment
		// c.sudphConn = nil
	}()

	if err := c.stcphConn.Close(websocket.StatusNormalClosure, "client closed"); err != nil {
		log.WithError(err).Errorf("Failed to close STCPH")
	}

	// TODO: uncomment
	// if err := c.sudphConn.Close(); err != nil {
	// 	log.WithError(err).Errorf("Failed to close SUDPH")
	// }

	return nil
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
