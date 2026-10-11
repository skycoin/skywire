// Package visor pkg/visor/hostbridge.go c3-vis-wasm
package visor

import (
	"context"
	"errors"
	"net"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/hostbridge"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
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
// and keeps redialing it until HostBridgeDisconnect.
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
	v.hostBridgeLog().WithField("url", url).Info("Connected to the host bridge")
	go v.hostBridgeSupervise(gen, c)
	return nil
}

func (v *Visor) dialHostBridge(url string) (*hostbridge.Client, error) {
	ctx, cancel := context.WithTimeout(v.ctx, hostBridgeDialTimeout)
	defer cancel()
	conn, err := dialHostBridgeConn(ctx, url)
	if err != nil {
		return nil, err
	}
	c, err := hostbridge.NewClient(conn)
	if err != nil {
		_ = conn.Close() //nolint:errcheck
		return nil, err
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

// hostBridgeSupervise redials the bridge with backoff whenever the session
// of generation gen ends.
func (v *Visor) hostBridgeSupervise(gen int, c *hostbridge.Client) {
	hb := &v.hostBridge
	log := v.hostBridgeLog()
	for {
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
		url := hb.url
		hb.client, hb.err = nil, "session to the bridge ended"
		for _, f := range hb.forwards {
			f.l, f.bound = nil, ""
		}
		hb.mu.Unlock()
		log.WithField("url", url).Warn("Session to the host bridge ended, redialing")

		for wait := hostBridgeMinRetry; ; wait = min(2*wait, hostBridgeMaxRetry) {
			select {
			case <-time.After(wait):
			case <-v.ctx.Done():
				return
			}
			hb.mu.Lock()
			current := hb.gen == gen
			hb.mu.Unlock()
			if !current {
				return
			}
			next, err := v.dialHostBridge(url)
			if err != nil {
				hb.mu.Lock()
				if hb.gen == gen {
					hb.err = err.Error()
				}
				hb.mu.Unlock()
				continue
			}
			if !v.hostBridgeAttach(gen, next) {
				_ = next.Close() //nolint:errcheck
				return
			}
			log.WithField("url", url).Info("Reconnected to the host bridge")
			c = next
			break
		}
	}
}

// HostBridgeDisconnect ends the session and its forwards.
func (v *Visor) HostBridgeDisconnect() error {
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

// HostBridgeForward listens on host on the bridge's host and passes each
// connection to local, an address the visor can dial. The forward is kept
// across reconnects.
func (v *Visor) HostBridgeForward(host, local string) (visorapi.HostBridgeForward, error) {
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

// HostBridgeUnforward stops the forward from host.
func (v *Visor) HostBridgeUnforward(host string) error {
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
