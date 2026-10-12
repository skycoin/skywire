// Package visor pkg/visor/hostbridge.go c3-vis-wasm
package visor

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/hostbridge"
	"github.com/skycoin/skywire/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const (
	hostBridgeDialTimeout = 15 * time.Second
	hostBridgeMinRetry    = time.Second
	hostBridgeMaxRetry    = 30 * time.Second
)

var errHostBridgeNotConnected = errors.New("not connected to a host bridge")

// hostBridgeState is the visor's session to a host bridge, which lends a
// browser visor the sockets of its host. See pkg/hostbridge.
type hostBridgeState struct {
	mu     sync.Mutex
	url    string
	client *hostbridge.Client
	err    string
	// gen counts connects and disconnects, so the supervisor of a replaced
	// session stops.
	gen      int
	forwards map[string]*hostBridgeFwd
	// sockets lend the bridge's host sockets to the transports of a browser
	// visor, which starts stcpr and sudph on its first session.
	sockets           hostBridgeSockets
	transportsStarted bool
	// secret is the pairing secret the bridge gave this visor, kept across
	// disconnects so the visor can reconnect to the bridge it paired with.
	secret       string
	secretLoaded bool
}

type hostBridgeFwd struct {
	host, local string
	bound, err  string
	l           net.Listener
}

func (v *Visor) hostBridgeLog() *logging.Logger {
	return v.MasterLogger().PackageLogger("hostbridge")
}

// HostBridgeConnect connects to the bridge at url, replacing any session,
// and keeps redialing it until HostBridgeDisconnect. The URL is saved in the
// config, so the visor reconnects at boot.
func (v *Visor) HostBridgeConnect(url string) error {
	hb := &v.hostBridge
	hb.mu.Lock()
	hb.gen++
	gen := hb.gen
	if hb.client != nil {
		_ = hb.client.Close() //nolint:errcheck
		hb.client = nil
	}
	hb.url, hb.err = url, ""
	hb.mu.Unlock()

	c, err := v.dialHostBridge(url)
	if err != nil {
		hb.mu.Lock()
		if hb.gen == gen {
			hb.url, hb.err = "", err.Error()
		}
		hb.mu.Unlock()
		return err
	}
	if !v.hostBridgeAttach(gen, c) {
		_ = c.Close() //nolint:errcheck
		return errors.New("replaced by a newer connect")
	}
	v.hostBridgeAttached(c)
	v.hostBridgeLog().WithField("url", url).Info("Connected to the host bridge")
	v.hostBridgeSave()
	go v.hostBridgeSupervise(gen, url, c)
	return nil
}

// initHostBridge reconnects to the bridge saved in the config, in the
// background, and reopens its forwards once connected.
func initHostBridge(_ context.Context, v *Visor, _ *logging.Logger) error {
	if v.conf == nil || v.conf.HostBridge == nil || v.conf.HostBridge.URL == "" {
		return nil
	}
	cfg := v.conf.HostBridge
	hb := &v.hostBridge
	hb.mu.Lock()
	if hb.url == cfg.URL && hb.client != nil {
		hb.mu.Unlock()
		return nil
	}
	hb.gen++
	gen := hb.gen
	hb.url, hb.err = cfg.URL, "connecting"
	hb.forwards = map[string]*hostBridgeFwd{}
	for _, f := range cfg.Forwards {
		hb.forwards[f.Host] = &hostBridgeFwd{host: f.Host, local: f.Local}
	}
	hb.mu.Unlock()
	go v.hostBridgeSupervise(gen, cfg.URL, nil)
	return nil
}

func (v *Visor) dialHostBridge(url string) (*hostbridge.Client, error) {
	probeLocalAccess(v.ctx, url)
	ctx, cancel := context.WithTimeout(v.ctx, hostBridgeDialTimeout)
	defer cancel()
	conn, err := dialHostBridgeConn(ctx, url)
	if err != nil {
		return nil, err
	}
	hb := &v.hostBridge
	hb.mu.Lock()
	if !hb.secretLoaded {
		if v.conf.HostBridge != nil {
			hb.secret = v.conf.HostBridge.Secret
		}
		hb.secretLoaded = true
	}
	p := &hostbridge.Pairing{PK: v.conf.PK.Hex(), Secret: hb.secret}
	hb.mu.Unlock()
	c, err := hostbridge.NewClient(conn, p)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
	}
	hb.mu.Lock()
	paired := p.Secret != hb.secret
	hb.secret = p.Secret
	hb.mu.Unlock()
	if paired {
		v.hostBridgeLog().Info("Paired with the host bridge")
		v.hostBridgeSave()
	}
	return c, nil
}

// hostBridgeAttach makes c the session of generation gen and reopens the
// forwards on it. It reports false when gen was replaced meanwhile.
func (v *Visor) hostBridgeAttach(gen int, c *hostbridge.Client) bool {
	hb := &v.hostBridge
	hb.mu.Lock()
	defer hb.mu.Unlock()
	if hb.gen != gen {
		return false
	}
	hb.client, hb.err = c, ""
	for _, f := range hb.forwards {
		v.hostBridgeListen(c, f)
	}
	return true
}

// hostBridgeSupervise keeps the session of generation gen up. It dials when
// c is nil and redials with backoff each time the session ends.
func (v *Visor) hostBridgeSupervise(gen int, url string, c *hostbridge.Client) {
	hb := &v.hostBridge
	log := v.hostBridgeLog().WithField("url", url)
	for {
		if c == nil {
			if c = v.hostBridgeRedial(gen, url); c == nil {
				return
			}
			v.hostBridgeAttached(c)
			log.Info("Connected to the host bridge")
		}
		select {
		case <-c.Done():
		case <-v.ctx.Done():
			_ = c.Close() //nolint:errcheck
			return
		}
		hb.mu.Lock()
		if hb.gen != gen {
			hb.mu.Unlock()
			return
		}
		hb.client, hb.err = nil, "session to the bridge ended"
		for _, f := range hb.forwards {
			f.l, f.bound = nil, ""
		}
		hb.mu.Unlock()
		hb.sockets.attach(nil)
		log.Warn("Session to the host bridge ended, redialing")
		c = nil
	}
}

// hostBridgeRedial dials url with backoff until it connects, and returns
// nil when generation gen is replaced or the visor stops.
func (v *Visor) hostBridgeRedial(gen int, url string) *hostbridge.Client {
	hb := &v.hostBridge
	for wait := hostBridgeMinRetry; ; wait = min(2*wait, hostBridgeMaxRetry) {
		hb.mu.Lock()
		current := hb.gen == gen
		hb.mu.Unlock()
		if !current {
			return nil
		}
		c, err := v.dialHostBridge(url)
		if err == nil {
			if v.hostBridgeAttach(gen, c) {
				return c
			}
			_ = c.Close() //nolint:errcheck
			return nil
		}
		hb.mu.Lock()
		if hb.gen == gen {
			hb.err = err.Error()
		}
		hb.mu.Unlock()
		select {
		case <-time.After(wait):
		case <-v.ctx.Done():
			return nil
		}
	}
}

// hostBridgeSave writes the URL and forwards to the config, or removes the
// section when disconnected.
func (v *Visor) hostBridgeSave() {
	if v.conf == nil {
		return
	}
	hb := &v.hostBridge
	hb.mu.Lock()
	var cfg *visorconfig.HostBridgeConfig
	if hb.url != "" || hb.secret != "" {
		cfg = &visorconfig.HostBridgeConfig{Secret: hb.secret, URL: hb.url, Forwards: []visorconfig.HostBridgeForwardConfig{}}
		for _, f := range hb.forwards {
			cfg.Forwards = append(cfg.Forwards, visorconfig.HostBridgeForwardConfig{Host: f.host, Local: f.local})
		}
		sort.Slice(cfg.Forwards, func(i, j int) bool { return cfg.Forwards[i].Host < cfg.Forwards[j].Host })
	}
	hb.mu.Unlock()
	if err := v.conf.UpdateHostBridge(cfg); err != nil {
		v.hostBridgeLog().WithError(err).Warn("Could not save the host bridge in the config")
	}
}

// hostBridgeDisconnect ends the session and its forwards.
func (v *Visor) hostBridgeDisconnect() error {
	hb := &v.hostBridge
	hb.mu.Lock()
	defer hb.mu.Unlock()
	hb.gen++
	if hb.client != nil {
		_ = hb.client.Close() //nolint:errcheck
	}
	hb.client, hb.url, hb.err, hb.forwards = nil, "", "", nil
	return nil
}

// HostBridgeStatus reports the session and its forwards.
func (v *Visor) HostBridgeStatus() (visorapi.HostBridgeStatus, error) {
	hb := &v.hostBridge
	hb.mu.Lock()
	defer hb.mu.Unlock()
	st := visorapi.HostBridgeStatus{URL: hb.url, Connected: hb.client != nil, Error: hb.err, Forwards: []visorapi.HostBridgeForward{}}
	for _, f := range hb.forwards {
		st.Forwards = append(st.Forwards, f.status())
	}
	sort.Slice(st.Forwards, func(i, j int) bool { return st.Forwards[i].Host < st.Forwards[j].Host })
	return st, nil
}

// hostBridgeForward listens on host on the bridge's host and passes each
// connection to local, an address the visor can dial. The forward is kept
// across reconnects.
func (v *Visor) hostBridgeForward(host, local string) (visorapi.HostBridgeForward, error) {
	hb := &v.hostBridge
	hb.mu.Lock()
	defer hb.mu.Unlock()
	if hb.client == nil {
		return visorapi.HostBridgeForward{}, errHostBridgeNotConnected
	}
	if old := hb.forwards[host]; old != nil && old.l != nil {
		_ = old.l.Close() //nolint:errcheck
	}
	f := &hostBridgeFwd{host: host, local: local}
	v.hostBridgeListen(hb.client, f)
	if f.l == nil {
		return f.status(), errors.New(f.err)
	}
	if hb.forwards == nil {
		hb.forwards = map[string]*hostBridgeFwd{}
	}
	hb.forwards[host] = f
	return f.status(), nil
}

// hostBridgeUnforward stops the forward from host.
func (v *Visor) hostBridgeUnforward(host string) error {
	hb := &v.hostBridge
	hb.mu.Lock()
	defer hb.mu.Unlock()
	f := hb.forwards[host]
	if f == nil {
		return errors.New("no forward from " + host)
	}
	if f.l != nil {
		_ = f.l.Close() //nolint:errcheck
	}
	delete(hb.forwards, host)
	return nil
}

// hostBridgeListen opens f's listener on c. Must be called with the state
// locked.
func (v *Visor) hostBridgeListen(c *hostbridge.Client, f *hostBridgeFwd) {
	l, err := c.Listen("tcp", f.host)
	if err != nil {
		f.l, f.bound, f.err = nil, "", err.Error()
		return
	}
	f.l, f.bound, f.err = l, l.Addr().String(), ""
	log := &logging.Logger{FieldLogger: v.hostBridgeLog().WithField("host", f.bound).WithField("local", f.local)}
	log.Info("Forwarding from the bridge's host")
	go func(local string) {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			go forwardRawTCP(log, conn, local)
		}
	}(f.local)
}

func (f *hostBridgeFwd) status() visorapi.HostBridgeForward {
	return visorapi.HostBridgeForward{Host: f.host, Bound: f.bound, Local: f.local, Error: f.err}
}

// HostBridgeDisconnect ends the session and its forwards, and removes them
// from the config.
func (v *Visor) HostBridgeDisconnect() error {
	err := v.hostBridgeDisconnect()
	v.hostBridge.sockets.attach(nil)
	v.hostBridgeSave()
	return err
}

// HostBridgeForward listens on host on the bridge's host and passes each
// connection to local, an address the visor can dial. The forward is kept
// across reconnects and saved in the config.
func (v *Visor) HostBridgeForward(host, local string) (visorapi.HostBridgeForward, error) {
	host, local = loopbackDefault(host), loopbackDefault(local)
	f, err := v.hostBridgeForward(host, local)
	if err == nil {
		v.hostBridgeSave()
	}
	return f, err
}

// HostBridgeUnforward stops the forward from host.
func (v *Visor) HostBridgeUnforward(host string) error {
	host = loopbackDefault(host)
	err := v.hostBridgeUnforward(host)
	if err == nil {
		v.hostBridgeSave()
	}
	return err
}

// hostBridgeAttached lends the new session's sockets to the transports. On
// the first session of a browser visor it starts stcpr and sudph, which a
// browser cannot run on its own.
func (v *Visor) hostBridgeAttached(c *hostbridge.Client) {
	hb := &v.hostBridge
	hb.sockets.attach(c)
	if runtime.GOOS != "js" || v.tpM == nil || v.conf == nil || v.conf.Transport == nil {
		return
	}
	hb.mu.Lock()
	start := !hb.transportsStarted
	hb.transportsStarted = true
	hb.mu.Unlock()
	if !start {
		return
	}
	v.hostBridgeLog().Info("Starting stcpr and sudph on the bridge's host")
	v.tpM.InitClient(v.ctx, types.STCPR, v.conf.Transport.StcprPort)
	v.tpM.InitClient(v.ctx, types.SUDPH, v.conf.Transport.SudphPort)
}

// loopbackDefault puts an address with no host, ":1080" or "1080", on the
// loopback. A forward reaches every interface only when asked to, unlike
// docker -p, whose default publishes past the host's firewall.
func loopbackDefault(addr string) string {
	if !strings.Contains(addr, ":") {
		addr = ":" + addr
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || host != "" {
		return addr
	}
	return net.JoinHostPort("127.0.0.1", port)
}
