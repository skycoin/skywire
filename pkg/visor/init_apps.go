// init_apps.go contains app launcher and CLI initialization logic.
package visor

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor/rpcgrpc"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/vpn"
)

func initLauncher(_ context.Context, v *Visor, _ *logging.Logger) error {
	conf := v.conf.Launcher

	// Prepare proc manager.
	procM, err := appserver.NewProcManager(v.MasterLogger(), &v.serviceDisc, v.ebc, conf.ServerAddr, v.conf.LocalPath)
	if err != nil {
		err := fmt.Errorf("failed to start proc_manager: %w", err)
		return err
	}

	v.pushCloseStack("launcher.proc_manager", procM.Close)

	// Prepare launcher.
	launchConf := launcher.AppLauncherConfig{
		VisorPK:       v.conf.PK,
		Apps:          conf.Apps,
		ServerAddr:    conf.ServerAddr,
		BinPath:       conf.BinPath,
		LocalPath:     v.conf.LocalPath,
		DisplayNodeIP: conf.DisplayNodeIP,
		MuxRoutes:     v.conf.Routing.MuxRoutes,
	}

	launchLog := v.MasterLogger().PackageLogger("launcher")

	launch, err := launcher.NewLauncher(launchLog, launchConf, v.dmsgC, v.router, procM)
	if err != nil {
		err := fmt.Errorf("failed to start launcher: %w", err)
		return err
	}

	err = launch.AutoStart(launcher.EnvMap{
		skyenv.VPNClientName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, v.tpM.STCPRRemoteAddrs()),
		skyenv.VPNServerName: vpnEnvMaker(v.conf, v.dmsgC, v.dmsgDC, nil),
	})

	if err != nil {
		err := fmt.Errorf("failed to autostart apps: %w", err)
		return err
	}

	v.initLock.Lock()
	v.procM = procM
	v.appL = launch
	v.initLock.Unlock()

	return nil
}

// Make an env maker function for vpn application
func vpnEnvMaker(conf *visorconfig.V1, dmsgC, dmsgDC *dmsg.Client, tpRemoteAddrs []string) launcher.EnvMaker {
	return func() ([]string, error) {
		var envCfg vpn.DirectRoutesEnvConfig

		if conf.Dmsg != nil {
			envCfg.DmsgDiscovery = conf.Dmsg.Discovery

			log := conf.MasterLogger().PackageLogger("vpn_env_maker")
			r := netutil.NewRetrier(log, 1*time.Second, 10*time.Second, 0, 1)
			err := r.Do(context.Background(), func() error {
				for _, ses := range dmsgC.AllSessions() {
					envCfg.DmsgServers = append(envCfg.DmsgServers, ses.RemoteTCPAddr().String())
				}

				if len(envCfg.DmsgServers) == 0 {
					return errors.New("no dmsg servers found")
				}

				if dmsgDC != nil {
					for _, ses := range dmsgDC.AllSessions() {
						envCfg.DmsgServers = append(envCfg.DmsgServers, ses.RemoteTCPAddr().String())
					}
				}
				return nil
			})

			if err != nil {
				return nil, fmt.Errorf("error getting Dmsg servers: %w", err)
			}
		}

		if conf.Transport != nil {
			envCfg.TPDiscovery = conf.Transport.Discovery
			envCfg.AddressResolver = conf.Transport.AddressResolver
		}

		if conf.Routing != nil {
			envCfg.RF = conf.Routing.RouteFinder
		}

		if conf.UptimeTracker != nil {
			envCfg.UptimeTracker = conf.UptimeTracker.Addr
		}

		if conf.STCP != nil && len(conf.STCP.PKTable) != 0 {
			envCfg.STCPTable = conf.STCP.PKTable
		}

		envCfg.TPRemoteIPs = tpRemoteAddrs

		envMap := vpn.AppEnvArgs(envCfg)

		envs := make([]string, 0, len(envMap))
		for k, v := range envMap {
			envs = append(envs, fmt.Sprintf("%s=%s", k, v))
		}

		return envs, nil
	}
}

// cliRPCStats tracks CLI RPC connection statistics for diagnostics
type cliRPCStats struct {
	mu            sync.Mutex
	activeConns   int32
	totalConns    uint64
	totalErrors   uint64
	lastError     string
	lastErrorTime time.Time
	peakConns     int32
	connSemaphore chan struct{}
	maxConns      int
}

func (s *cliRPCStats) acquire() bool {
	select {
	case s.connSemaphore <- struct{}{}:
		s.mu.Lock()
		s.activeConns++
		if s.activeConns > s.peakConns {
			s.peakConns = s.activeConns
		}
		s.totalConns++
		s.mu.Unlock()
		return true
	default:
		return false
	}
}

func (s *cliRPCStats) release() {
	s.mu.Lock()
	s.activeConns--
	s.mu.Unlock()
	<-s.connSemaphore
}

func (s *cliRPCStats) recordError(err string) {
	s.mu.Lock()
	s.totalErrors++
	s.lastError = err
	s.lastErrorTime = time.Now()
	s.mu.Unlock()
}

func (s *cliRPCStats) snapshot() (active int32, total uint64, errors uint64, peak int32, lastErr string, lastErrTime time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeConns, s.totalConns, s.totalErrors, s.peakConns, s.lastError, s.lastErrorTime
}

// visorPingAdapter wraps a Visor to implement rpcgrpc.VisorAPI
type visorPingAdapter struct {
	v *Visor
}

func (a *visorPingAdapter) DialPing(conf rpcgrpc.PingConf) error {
	// Convert rpcgrpc.RouteHopInfo to RouteHopInfo
	var forwardHops, reverseHops []RouteHopInfo
	for _, h := range conf.ForwardHops {
		forwardHops = append(forwardHops, RouteHopInfo{
			TpID:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	for _, h := range conf.ReverseHops {
		reverseHops = append(reverseHops, RouteHopInfo{
			TpID:   h.TpID,
			From:   h.From,
			To:     h.To,
			TpType: h.TpType,
		})
	}
	return a.v.DialPing(PingConfig{
		PK:          conf.PK,
		Tries:       conf.Tries,
		PcktSize:    conf.PcktSize,
		LocalRoute:  conf.LocalRoute,
		TransportID: conf.TransportID,
		ForwardHops: forwardHops,
		ReverseHops: reverseHops,
	})
}

func (a *visorPingAdapter) PingOnce(conf rpcgrpc.PingConf) (time.Duration, error) {
	return a.v.PingOnce(PingConfig{
		PK:         conf.PK,
		Tries:      conf.Tries,
		PcktSize:   conf.PcktSize,
		LocalRoute: conf.LocalRoute,
	})
}

func (a *visorPingAdapter) StopPing(pk cipher.PubKey) error {
	return a.v.StopPing(pk)
}

func (a *visorPingAdapter) GetPingRoute(pk cipher.PubKey) []cipher.PubKey {
	return a.v.GetPingRoute(pk)
}

func (a *visorPingAdapter) GetPingRouteDetails(pk cipher.PubKey) []rpcgrpc.RouteHopInfo {
	details := a.v.GetPingRouteDetails(pk)
	if details == nil {
		return nil
	}
	// Convert router.RouteHopInfo to rpcgrpc.RouteHopInfo
	result := make([]rpcgrpc.RouteHopInfo, len(details))
	for i, d := range details {
		result[i] = rpcgrpc.RouteHopInfo{
			TpID:   d.TpID,
			From:   d.From,
			To:     d.To,
			TpType: d.TpType,
		}
	}
	return result
}

func (a *visorPingAdapter) GetLastRouteCalcTime() time.Duration {
	return a.v.GetLastRouteCalcTime()
}

func (a *visorPingAdapter) DialDmsgPing(pk cipher.PubKey) error {
	return a.v.DialDmsgPing(pk)
}

func (a *visorPingAdapter) DmsgPingOnce(conf rpcgrpc.PingConf) (time.Duration, error) {
	return a.v.DmsgPingOnce(PingConfig{
		PK:       conf.PK,
		Tries:    conf.Tries,
		PcktSize: conf.PcktSize,
	})
}

func (a *visorPingAdapter) PingOnceWithEcho(conf rpcgrpc.PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	return a.v.PingOnceWithEcho(PingConfig{
		PK:         conf.PK,
		Tries:      conf.Tries,
		PcktSize:   conf.PcktSize,
		LocalRoute: conf.LocalRoute,
	}, echoFull)
}

func (a *visorPingAdapter) DmsgPingOnceWithEcho(conf rpcgrpc.PingConf, echoFull bool) (bytesSent, bytesReceived uint64, latency time.Duration, err error) {
	return a.v.DmsgPingOnceWithEcho(PingConfig{
		PK:       conf.PK,
		Tries:    conf.Tries,
		PcktSize: conf.PcktSize,
	}, echoFull)
}

func (a *visorPingAdapter) StopDmsgPing(pk cipher.PubKey) error {
	return a.v.StopDmsgPing(pk)
}

func (a *visorPingAdapter) DialDmsgPingViaServer(pk cipher.PubKey, serverPK cipher.PubKey) error {
	return a.v.DialDmsgPingViaServer(pk, serverPK)
}

func (a *visorPingAdapter) GetDmsgPingServerPK(pk cipher.PubKey) (cipher.PubKey, error) {
	return a.v.GetDmsgPingServerPK(pk)
}

func (a *visorPingAdapter) GetRemoteDmsgServers(pk cipher.PubKey) ([]cipher.PubKey, error) {
	return a.v.GetRemoteDmsgServers(pk)
}

func (a *visorPingAdapter) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	return a.v.DialDmsgRPC(pk)
}

func initCLI(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.CLIAddr == "" {
		v.log.Debug("'cli_addr' is not configured, skipping.")
		return nil
	}

	cliL, err := net.Listen("tcp", v.conf.CLIAddr)
	if err != nil {
		return err
	}

	v.pushCloseStack("cli.listener", cliL.Close)

	rpcS, err := newRPCServer(v, "CLI")
	if err != nil {
		err := fmt.Errorf("failed to start rpc server for cli: %w", err)
		return err
	}

	// Create gRPC server for streaming operations
	grpcLog := v.MasterLogger().PackageLogger("cli_grpc")
	grpcServer := grpc.NewServer()
	pingAdapter := &visorPingAdapter{v: v}
	pingServer := rpcgrpc.NewPingServer(pingAdapter, grpcLog)
	rpcgrpc.RegisterPingServiceServer(grpcServer, pingServer)

	v.pushCloseStack("cli.grpc", func() error {
		grpcServer.GracefulStop()
		return nil
	})

	// Use cmux to multiplex gRPC and standard RPC on same port
	mux := cmux.New(cliL)
	grpcL := mux.MatchWithWriters(cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"))
	rpcL := mux.Match(cmux.Any()) // All other connections go to standard RPC

	// Connection limiting and stats for standard RPC
	const maxConcurrentConns = 50
	stats := &cliRPCStats{
		connSemaphore: make(chan struct{}, maxConcurrentConns),
		maxConns:      maxConcurrentConns,
	}

	// Start gRPC server
	go func() {
		grpcLog.Infof("CLI gRPC server listening on %s (multiplexed)", v.conf.CLIAddr)
		if err := grpcServer.Serve(grpcL); err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") &&
				!strings.Contains(err.Error(), "mux: listener closed") {
				grpcLog.WithError(err).Error("gRPC server error")
			}
		}
	}()

	// Run standard RPC accept loop with panic recovery, connection limiting, and logging
	go func() {
		rpcLog := v.MasterLogger().PackageLogger("cli_rpc")
		rpcLog.Infof("CLI RPC server listening on %s (max %d concurrent connections, multiplexed)", v.conf.CLIAddr, maxConcurrentConns)

		// Periodic stats logging
		go func() {
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				active, total, errors, peak, lastErr, lastErrTime := stats.snapshot()
				if total > 0 {
					rpcLog.Debugf("CLI RPC stats: active=%d, total=%d, errors=%d, peak=%d", active, total, errors, peak)
					if lastErr != "" && time.Since(lastErrTime) < time.Minute {
						rpcLog.Debugf("CLI RPC last error (%s ago): %s", time.Since(lastErrTime).Round(time.Second), lastErr)
					}
				}
			}
		}()

		var connID uint64
		for {
			conn, err := rpcL.Accept()
			if err != nil {
				// Check if listener was closed (normal shutdown)
				if strings.Contains(err.Error(), "use of closed network connection") ||
					strings.Contains(err.Error(), "mux: listener closed") {
					rpcLog.Debug("CLI RPC listener closed")
					return
				}
				stats.recordError(fmt.Sprintf("accept: %v", err))
				rpcLog.WithError(err).Warn("CLI RPC accept error, continuing...")
				continue
			}

			connID++
			thisConnID := connID

			// Try to acquire connection slot
			if !stats.acquire() {
				stats.recordError("connection limit reached")
				active, _, _, _, _, _ := stats.snapshot()
				rpcLog.Warnf("CLI RPC connection limit reached (%d/%d), rejecting connection", active, maxConcurrentConns)
				conn.Close() //nolint:errcheck,gosec
				continue
			}

			rpcLog.Debugf("CLI RPC conn #%d accepted from %s (active: %d)", thisConnID, conn.RemoteAddr(), stats.activeConns)

			// Handle each connection in a goroutine with panic recovery
			go func(c net.Conn, id uint64) {
				startTime := time.Now()
				defer func() {
					if r := recover(); r != nil {
						stats.recordError(fmt.Sprintf("panic: %v", r))
						rpcLog.Errorf("CLI RPC conn #%d panic recovered: %v", id, r)
					}
					c.Close() //nolint:errcheck,gosec
					stats.release()
					rpcLog.Debugf("CLI RPC conn #%d closed after %s (active: %d)", id, time.Since(startTime).Round(time.Millisecond), stats.activeConns)
				}()

				// Set keepalive and deadline to prevent hung connections.
				// If an RPC method hangs (e.g., iterating corrupt routing rules),
				// the deadline ensures the connection is killed after the timeout
				// rather than blocking a connection slot forever.
				if tc, ok := c.(*net.TCPConn); ok {
					tc.SetKeepAlive(true)                   //nolint:errcheck,gosec
					tc.SetKeepAlivePeriod(30 * time.Second) //nolint:errcheck,gosec
				}
				c.SetDeadline(time.Now().Add(5 * time.Minute)) //nolint:errcheck,gosec

				rpcS.ServeConn(c)
			}(conn, thisConnID)
		}
	}()

	// Start cmux - this must be called after setting up all listeners
	go func() {
		if err := mux.Serve(); err != nil {
			if !strings.Contains(err.Error(), "use of closed network connection") &&
				!strings.Contains(err.Error(), "mux: listener closed") {
				v.log.WithError(err).Error("cmux serve error")
			}
		}
	}()

	return nil
}

func initHypervisors(_ context.Context, v *Visor, _ *logging.Logger) error {

	hvErrs := make(map[cipher.PubKey]chan error, len(v.conf.Hypervisors))
	for _, hv := range v.conf.Hypervisors {
		hvErrs[hv] = make(chan error, 1)
	}

	for hvPK, hvErrs := range hvErrs {
		log := v.MasterLogger().PackageLogger("hypervisor_client").WithField("hypervisor_pk", hvPK)

		addr := dmsg.Addr{PK: hvPK, Port: skyenv.DmsgHypervisorPort}
		rpcS, err := newRPCServer(v, addr.PK.String()[:shortHashLen])
		if err != nil {
			err := fmt.Errorf("failed to start RPC server for hypervisor %s: %w", hvPK, err)
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func(hvErrs chan error) {
			defer wg.Done()
			//			var autoPeerIP string
			//			if v.autoPeer {
			//				autoPeerIP = v.autoPeerIP
			//			} else {
			//				autoPeerIP = ""
			//			}
			defer delete(v.connectedHypervisors, hvPK)
			v.connectedHypervisors[hvPK] = true
			ServeRPCClient(ctx, log, v.dmsgC, rpcS, addr, hvErrs)
			//			ServeRPCClient(ctx, log, autoPeerIP, v.dmsgC, rpcS, addr, hvErrs)

		}(hvErrs)

		v.pushCloseStack("hypervisor."+hvPK.String()[:shortHashLen], func() error {
			cancel()
			wg.Wait()
			return nil
		})
	}

	return nil
}

func initHypervisor(_ context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.Hypervisor == nil {
		v.log.Error("hypervisor config = nil")
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())

	conf := *v.conf.Hypervisor
	conf.PK = v.conf.PK
	conf.SK = v.conf.SK
	conf.DmsgDiscovery = v.conf.Dmsg.Discovery

	// Prepare hypervisor.
	hv, err := NewHypervisor(conf, v, v.dmsgC)
	if err != nil {
		v.log.Fatalln("Failed to start hypervisor:", err)
	}

	hv.serveDmsg(ctx, v.log)

	// Serve HTTP(s).

	// Needed to work with modern browsers when serving from windows, which need the correct mime type for javascript.
	if err := mime.AddExtensionType(".js", "application/javascript"); err != nil {
		log.Fatalln("Unable to register js mime type.")
	}

	v.log.WithField("addr", conf.HTTPAddr).
		WithField("tls", conf.EnableTLS).
		Info("Serving hypervisor...")
	tls := ""
	if conf.EnableTLS {
		tls = "s"
	}
	v.log.Info(fmt.Sprintf("Hypervisor UI: http%s://127.0.0.1%s", tls, conf.HTTPAddr))

	handler := hv.HTTPHandler()
	srv := &http.Server{
		Addr:              conf.HTTPAddr,
		Handler:           handler,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		if conf.EnableTLS {
			err = srv.ListenAndServeTLS(conf.TLSCertFile, conf.TLSKeyFile)
		} else {
			err = srv.ListenAndServe()
		}

		// don't print error if local server is closed
		if !errors.Is(err, http.ErrServerClosed) {
			v.log.WithError(err).Error("Hypervisor exited with error.")
		}
	}()

	v.pushCloseStack("hypervisor", func() error {
		err := srv.Shutdown(ctx)
		cancel()
		return err
	})

	return nil
}
