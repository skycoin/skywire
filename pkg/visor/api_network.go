// api_network.go contains network connection, port forwarding, and module reinitialization API methods.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"

	clirewardsserver "github.com/skycoin/skywire/cmd/skywire-cli/commands/rewards/server"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// refreshWebsiteHandler (re)applies the right website handler to the
// dmsghttp logserver based on current config + forwarded-port state.
// Called at startup AND on every register/update/deregister of port 80
// so a runtime ProxyAddr change takes effect without a visor restart.
//
// Precedence: rewards UI (config-driven) > forwarded-port reverse proxy
// (runtime-driven) > nil (default landing page).
func (v *Visor) refreshWebsiteHandler(log *logging.Logger) {
	v.initLock.RLock()
	lsAPI := v.logServer.api
	v.initLock.RUnlock()
	if lsAPI == nil {
		return // logserver not yet constructed
	}

	// Rewards UI wins when configured — it's a global on/off switch.
	if rw := v.conf.Rewards; rw != nil && rw.Enable {
		log.Info("Mounting reward system UI on port 80")
		lsAPI.SetWebsiteHandler(clirewardsserver.ConfigureAndBuild(clirewardsserver.RewardConfig{
			WorkDir:         rw.WorkDir,
			WhitelistPKs:    rw.Whitelist,
			CanonicalDomain: rw.CanonicalDomain,
			SkycoinNode:     rw.SkycoinNode,
			LoginNode:       rw.LoginNode,
			DisableTpVizAPI: true,
		}))
		return
	}

	// Reverse-proxy mode: a forwarded port for 80 with proxy_addr (or
	// local_port as a fallback). Port 80 is owned by the dmsghttp
	// logserver — raw-TCP forwarding can't bind it, so the only way
	// to expose a local service on the visor's port-80 face is the
	// reverse proxy. If a user registers port 80 with --local-port
	// instead of --proxy-addr, treat that as their intent and
	// derive the proxy target from local_port; otherwise the entry
	// would silently do nothing and the visor landing page would
	// keep showing.
	if fp := v.forwardedPorts.Get(int(visorconfig.DmsgHTTPPort)); fp != nil {
		addr := fp.ProxyAddr
		if addr == "" && fp.LocalPort > 0 {
			addr = fmt.Sprintf("127.0.0.1:%d", fp.LocalPort)
			log.WithField("local_port", fp.LocalPort).
				Info("Port 80 has --local-port but no --proxy-addr; using localhost:LocalPort as reverse-proxy target")
		}
		if addr != "" {
			target, err := url.Parse("http://" + addr)
			if err != nil {
				log.WithError(err).Warn("Invalid forwarded port target; clearing website handler")
				lsAPI.SetWebsiteHandler(nil)
				return
			}
			log.WithField("addr", addr).Info("Reverse-proxying custom website (port 80)")
			lsAPI.SetWebsiteHandler(httputil.NewSingleHostReverseProxy(target))
			return
		}
	}

	// Default: clear the handler so the built-in landing page shows.
	lsAPI.SetWebsiteHandler(nil)
}

// ConnectRawTCP implements API. Establishes a raw TCP port forwarding connection over skywire.
func (v *Visor) ConnectRawTCP(remotePK cipher.PubKey, remotePort, localPort int) (uuid.UUID, error) {
	ok := isPortAvailable(v.log, localPort)
	if !ok {
		return uuid.UUID{}, fmt.Errorf(":%v local port already in use", localPort)
	}
	connApp := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: remotePK,
		Port:   routing.Port(skyenv.SkyForwardingServerPort),
	}
	conn, err := appnet.Dial(connApp)
	if err != nil {
		return uuid.UUID{}, err
	}
	remoteConn, err := appnet.WrapConn(conn)
	if err != nil {
		return uuid.UUID{}, err
	}

	cMsg := clientMsg{
		Port: remotePort,
	}
	clientMsgBytes, err := json.Marshal(cMsg)
	if err != nil {
		return uuid.UUID{}, err
	}
	_, err = remoteConn.Write(clientMsgBytes)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Raw TCP msg sent %s", clientMsgBytes)

	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		return uuid.UUID{}, err
	}
	var sReply serverReply
	err = json.Unmarshal(buf[:n], &sReply)
	if err != nil {
		return uuid.UUID{}, err
	}
	v.log.Debugf("Received: %v", sReply)

	if sReply.Error != nil {
		sErr := *sReply.Error
		v.log.WithError(fmt.Errorf("%s", sErr)).Error("Server closed with error")
		return uuid.UUID{}, fmt.Errorf("%s", sErr)
	}

	forwardConn, err := appnet.NewRawTCPForwardConn(v.log, remoteConn, remotePort, localPort)
	if err != nil {
		_ = remoteConn.Close() //nolint:errcheck
		return uuid.UUID{}, err
	}
	forwardConn.Serve()
	return forwardConn.ID, nil
}

// DisconnectRawTCP implements API.
func (v *Visor) DisconnectRawTCP(id uuid.UUID) error {
	forwardConn := appnet.GetRawTCPForwardConn(id)
	if forwardConn == nil {
		return fmt.Errorf("raw TCP forward connection not found: %s", id)
	}
	return forwardConn.Close()
}

// ListRawTCP implements API.
func (v *Visor) ListRawTCP() (map[uuid.UUID]*appnet.RawTCPForwardConn, error) {
	return appnet.GetAllRawTCPForwardConns(), nil
}

// RegisterTCPPort implements API (legacy — wraps RegisterForwardedPort with skynet-only defaults).
func (v *Visor) RegisterTCPPort(localPort int) error {
	return v.RegisterForwardedPort(ForwardedPort{
		Port:          localPort,
		Skynet:        true,
		ShowOnLanding: true,
	})
}

// DeregisterTCPPort implements API.
func (v *Visor) DeregisterTCPPort(localPort int) error {
	if v.forwardedPorts.Get(localPort) == nil {
		return fmt.Errorf("port :%v not registered", localPort)
	}
	// Stop DMSG listener if running.
	v.stopDmsgForwarder(localPort)
	// Remove from both the rich store and legacy allowed map.
	v.allowed.mu.Lock()
	delete(v.allowed.ports, localPort)
	v.allowed.mu.Unlock()
	if err := v.forwardedPorts.Deregister(localPort); err != nil {
		return err
	}
	if localPort == int(visorconfig.DmsgHTTPPort) {
		v.refreshWebsiteHandler(v.log)
	}
	return nil
}

// ListTCPPorts implements API (legacy — returns port numbers only).
func (v *Visor) ListTCPPorts() ([]int, error) {
	ports := v.forwardedPorts.AllSkynetPorts()
	// Also include legacy allowed ports not yet in forwardedPorts
	v.allowed.mu.RLock()
	for p := range v.allowed.ports {
		found := false
		for _, fp := range ports {
			if fp == p {
				found = true
				break
			}
		}
		if !found {
			ports = append(ports, p)
		}
	}
	v.allowed.mu.RUnlock()
	return ports, nil
}

// RegisterForwardedPort registers a port with full metadata.
func (v *Visor) RegisterForwardedPort(p ForwardedPort) error {
	if err := v.forwardedPorts.Register(p); err != nil {
		return err
	}
	// Keep legacy allowed map in sync for sky-forwarding server.
	if p.Skynet {
		v.allowed.mu.Lock()
		v.allowed.ports[p.Port] = true
		v.allowed.mu.Unlock()
	}
	// Create a DMSG listener for this port so .dmsg:<port> works.
	if p.DMSG {
		v.startDmsgForwarder(p.Port, p.LocalPort)
	}
	// If this is the dmsghttp port (80) entry, refresh the logserver's
	// website handler so a new ProxyAddr takes effect immediately.
	if p.Port == int(visorconfig.DmsgHTTPPort) {
		v.refreshWebsiteHandler(v.log)
	}
	return nil
}

// UpdateForwardedPort updates metadata for an existing forwarded port.
func (v *Visor) UpdateForwardedPort(p ForwardedPort) error {
	if v.forwardedPorts.Get(p.Port) == nil {
		return fmt.Errorf("port %d not registered", p.Port)
	}
	if err := v.forwardedPorts.Register(p); err != nil {
		return err
	}
	// Sync legacy allowed map.
	v.allowed.mu.Lock()
	if p.Skynet {
		v.allowed.ports[p.Port] = true
	} else {
		delete(v.allowed.ports, p.Port)
	}
	v.allowed.mu.Unlock()
	if p.Port == int(visorconfig.DmsgHTTPPort) {
		v.refreshWebsiteHandler(v.log)
	}
	return nil
}

// ListForwardedPorts returns all forwarded ports with metadata.
func (v *Visor) ListForwardedPorts() ([]ForwardedPort, error) {
	return v.forwardedPorts.List(), nil
}

func isPortAvailable(log *logging.Logger, port int) bool {
	timeout := time.Second
	conn, err := net.DialTimeout("tcp", fmt.Sprintf(":%v", port), timeout)
	if err != nil {
		return true
	}
	if conn != nil {
		defer closeConn(log, conn)
		return false
	}
	return true
}

func isPortRegistered(port int, v *Visor) bool {
	ports, err := v.ListTCPPorts()
	if err != nil {
		return false
	}
	for _, p := range ports {
		if p == port {
			return true
		}
	}
	return false
}

// ReinitiateModule implements API.
func (v *Visor) ReinitiateModule(module string) error {
	ctx := context.Background()
	ctx = context.WithValue(ctx, runtimeErrsKey, v.runtimeErrors)
	switch module {
	case "dmsg":
		return reinitiateDmsg(ctx, v)
	case "stcpr":
		reinitiateStpcr(ctx, v)
		return nil
	default:
		return fmt.Errorf("this module no allowed to reinitiate")
	}
}

func reinitiateStpcr(ctx context.Context, v *Visor) {
	v.tpM.InitClient(ctx, types.STCPR, 0)
}

func reinitiateDmsg(ctx context.Context, v *Visor) error {
	v.log.Info("Starting dmsg reinitialization with new client...")

	if err := shutdownDmsgDependentComponents(v, v.log); err != nil {
		v.log.WithError(err).Warn("Error during dependent components shutdown, continuing...")
	}

	if err := initDmsg(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to initialize new dmsg client: %w", err)
	}

	if err := initDmsgCtrl(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg ctrl: %w", err)
	}

	if err := initDmsgTrackers(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg trackers: %w", err)
	}

	if err := initDmsgHTTPLogServer(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsg http log server: %w", err)
	}

	if err := initDmsgpty(ctx, v, v.log); err != nil {
		return fmt.Errorf("failed to reinitialize dmsgpty: %w", err)
	}

	v.log.Info("Dmsg reinitialization completed successfully with new client")
	return nil
}

func shutdownDmsgDependentComponents(v *Visor, log *logging.Logger) error {
	// Order matters: close dependents first, then dmsg client itself
	components := []string{
		"router.serve", // a.k.a. dmsgpty
		"dmsghttp.logserver",
		"dmsg_tracker_manager",
		"dmsgctrl",
		"dmsg", // Close the dmsg client last, after all dependents
	}

	v.closeMu.Lock()
	defer v.closeMu.Unlock()

	var errs []error
	newCloseStack := make([]closer, 0, len(v.closeStack))

	for _, c := range v.closeStack {
		shouldClose := false
		for _, component := range components {
			if c.src == component {
				shouldClose = true
				break
			}
		}

		if shouldClose {
			if err := c.fn(); err != nil {
				log.WithError(err).WithField("component", c.src).Warn("Failed to close component")
				errs = append(errs, err)
			} else {
				log.WithField("component", c.src).Debug("Successfully closed component")
			}
		} else {
			newCloseStack = append(newCloseStack, c)
		}
	}

	v.closeStack = newCloseStack

	v.closeMu.Unlock()
	v.initLock.Lock()
	v.dmsgTracker.ready = make(chan struct{})
	v.dmsgTracker.readyOnce = sync.Once{}
	v.initLock.Unlock()
	v.closeMu.Lock()

	if len(errs) > 0 {
		return fmt.Errorf("encountered %d errors during shutdown", len(errs))
	}
	return nil
}

// startDmsgForwarder creates a DMSG listener on the given port and
// forwards accepted connections to localhost:localPort. This enables
// .dmsg:<port> access to forwarded ports.
func (v *Visor) startDmsgForwarder(port, localPort int) {
	if v.dmsgC == nil {
		return
	}
	if localPort == 0 {
		localPort = port
	}

	v.dmsgFwdMu.Lock()
	defer v.dmsgFwdMu.Unlock()

	// Already running for this port.
	if _, ok := v.dmsgFwdListeners[port]; ok {
		return
	}

	lis, err := v.dmsgC.Listen(uint16(port)) //nolint:gosec
	if err != nil {
		v.log.WithError(err).WithField("port", port).Warn("Failed to create DMSG listener for forwarded port")
		return
	}

	ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel stored in dmsgFwdListeners
	v.dmsgFwdListeners[port] = cancel

	go func() {
		defer lis.Close() //nolint:errcheck
		log := v.log.WithField("dmsg_fwd", port)
		log.WithField("local", localPort).Info("DMSG forwarder started")
		for {
			conn, err := lis.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					log.Debug("DMSG forwarder stopped")
					return
				default:
				}
				log.WithError(err).Debug("DMSG forwarder accept error")
				return
			}
			go func() {
				defer conn.Close() //nolint:errcheck
				local, err := net.Dial("tcp", fmt.Sprintf("localhost:%d", localPort))
				if err != nil {
					log.WithError(err).Debug("Failed to dial local port")
					return
				}
				defer local.Close() //nolint:errcheck
				// Bidirectional pipe.
				done := make(chan struct{}, 2)
				pipe := func(dst, src net.Conn) {
					buf := make([]byte, 32*1024)
					for {
						n, rErr := src.Read(buf)
						if n > 0 {
							if _, wErr := dst.Write(buf[:n]); wErr != nil {
								break
							}
						}
						if rErr != nil {
							break
						}
					}
					done <- struct{}{}
				}
				go pipe(local, conn)
				go pipe(conn, local)
				<-done
			}()
		}
	}()
}

// stopDmsgForwarder cancels the DMSG listener goroutine for the given port.
func (v *Visor) stopDmsgForwarder(port int) {
	v.dmsgFwdMu.Lock()
	defer v.dmsgFwdMu.Unlock()
	if cancel, ok := v.dmsgFwdListeners[port]; ok {
		cancel()
		delete(v.dmsgFwdListeners, port)
	}
}
