// init_apps.go contains app launcher and CLI initialization logic.
package visor

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/soheilhy/cmux"
	"google.golang.org/grpc"

	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/cipher"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
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

// visorPingAdapter wraps a Visor to implement rpcgrpc.VisorAPI.
// The adapter is shared across both PingServer instances (local CLI
// gRPC + dmsg-RPC gRPC) so any counter on the adapter aggregates
// across both transports.
type visorPingAdapter struct {
	v *Visor
}

// IncGroupStreamSend implements rpcgrpc.VisorAPI. Called once per
// successful stream.Send by StreamGroupMessages; surfaced back into
// pkg/visor.GroupInfo.StreamSendCount via Visor.groupStreamSendCount.
// Subscribed sentinels are NOT counted — operators care about data
// events.
func (a *visorPingAdapter) IncGroupStreamSend() {
	a.v.groupStreamSendCounter.Add(1)
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

func (a *visorPingAdapter) SubscribeLogs(f logging.Filter, capacity int) (<-chan *logrus.Entry, func() uint64) {
	return a.v.SubscribeLogs(f, capacity)
}

// SubscribeGroupMessages bridges visor's group inbox into rpcgrpc's
// import-cycle-safe channel type. Spawns one goroutine per subscriber
// that drains the inbox channel, converts each GroupMessage to the
// rpcgrpc-local GroupMessageData mirror, and forwards on the returned
// channel. The forwarding goroutine exits when the underlying inbox
// channel closes (the cancel func calls inbox.unsubscribe, which
// closes that channel) — which then closes the outbound forwarding
// channel and signals stream end to the gRPC handler.
func (a *visorPingAdapter) SubscribeGroupMessages(capacity int) (<-chan rpcgrpc.GroupMessageData, func() uint64) {
	src, cancel := a.v.SubscribeGroupMessages(capacity)
	if src == nil {
		return nil, cancel
	}
	out := make(chan rpcgrpc.GroupMessageData, capacity)
	go func() {
		defer close(out)
		for m := range src {
			out <- rpcgrpc.GroupMessageData{
				TimestampNs: m.TS.UnixNano(),
				GroupID:     m.GroupID,
				SenderPK:    m.SenderPK.Hex(),
				Body:        m.Text,
			}
		}
	}()
	return out, cancel
}

// SnapshotGroupMessagesAfterNs bridges Visor.SnapshotGroupMessagesAfter
// (time.Time arg, returns []GroupMessage) into rpcgrpc's import-cycle-
// safe shape (UnixNano arg, returns []GroupMessageData). Empty slice
// when grouping isn't initialized — matches the SubscribeGroupMessages
// (nil channel) convention.
func (a *visorPingAdapter) SnapshotGroupMessagesAfterNs(sinceNs int64) []rpcgrpc.GroupMessageData {
	since := time.Unix(0, sinceNs)
	src := a.v.SnapshotGroupMessagesAfter(since)
	if len(src) == 0 {
		return nil
	}
	out := make([]rpcgrpc.GroupMessageData, 0, len(src))
	for _, m := range src {
		out = append(out, rpcgrpc.GroupMessageData{
			TimestampNs: m.TS.UnixNano(),
			GroupID:     m.GroupID,
			SenderPK:    m.SenderPK.Hex(),
			Body:        m.Text,
		})
	}
	return out
}

// SnapshotGroupHistoryAfterNs bridges Visor.SnapshotGroupHistoryAfter
// into rpcgrpc's import-cycle-safe shape (UnixNano arg, []GroupMessageData
// return). Empty slice when grouping is uninitialized or no
// GroupHistoryDB is configured — the StreamGroupMessages handler
// treats nil as "no history backfill available, stick with inbox-only"
// which preserves the existing behavior for operators who haven't
// opted into persistence.
func (a *visorPingAdapter) SnapshotGroupHistoryAfterNs(groupID string, sinceNs int64) []rpcgrpc.GroupMessageData {
	since := time.Unix(0, sinceNs)
	src := a.v.SnapshotGroupHistoryAfter(groupID, since)
	if len(src) == 0 {
		return nil
	}
	out := make([]rpcgrpc.GroupMessageData, 0, len(src))
	for _, m := range src {
		out = append(out, rpcgrpc.GroupMessageData{
			TimestampNs: m.TS.UnixNano(),
			GroupID:     m.GroupID,
			SenderPK:    m.SenderPK.Hex(),
			Body:        m.Text,
		})
	}
	return out
}

func (a *visorPingAdapter) LocalPK() cipher.PubKey {
	return a.v.LocalPK()
}

func (a *visorPingAdapter) FetchAllTransportEntries(ctx context.Context) ([]*transport.Entry, error) {
	return a.v.FetchAllTransportEntries(ctx)
}

func (a *visorPingAdapter) DialDmsgRPC(pk cipher.PubKey) (net.Conn, error) {
	return a.v.DialDmsgRPC(pk)
}

func initCLI(ctx context.Context, v *Visor, log *logging.Logger) error {
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

	// Start gRPC server on DMSG for remote access (gotop, stats, etc.)
	// Access is restricted to hypervisor PKs and dmsgpty whitelist PKs.
	if v.dmsgC != nil {
		dmsgGRPCLog := v.MasterLogger().PackageLogger("dmsg_grpc")
		dmsgGRPCServer := grpc.NewServer()
		dmsgPingServer := rpcgrpc.NewPingServer(pingAdapter, dmsgGRPCLog)
		rpcgrpc.RegisterPingServiceServer(dmsgGRPCServer, dmsgPingServer)

		// Build authorized PK set: hypervisor PKs + dmsgpty whitelist
		authorizedPKs := make(map[cipher.PubKey]bool)
		for _, pk := range v.conf.Hypervisors {
			authorizedPKs[pk] = true
		}
		if v.conf.Dmsgpty != nil {
			for _, pk := range v.conf.Dmsgpty.Whitelist {
				authorizedPKs[pk] = true
			}
		}

		if len(authorizedPKs) == 0 {
			dmsgGRPCLog.Info("No hypervisor PKs or dmsgpty whitelist configured; DMSG gRPC server disabled")
		} else if dmsgGRPCL, err := v.dmsgC.Listen(skyenv.DmsgGRPCPort); err != nil {
			log.WithError(err).Warn("Failed to listen on DMSG gRPC port")
		} else {
			// Wrap listener with access control
			authL := &authorizedDmsgListener{
				Listener:      dmsgGRPCL,
				authorizedPKs: authorizedPKs,
				log:           dmsgGRPCLog,
			}

			v.pushCloseStack("dmsg.grpc.listener", dmsgGRPCL.Close)
			v.pushCloseStack("dmsg.grpc.server", func() error {
				dmsgGRPCServer.GracefulStop()
				return nil
			})

			go func() {
				dmsgGRPCLog.Infof("DMSG gRPC server listening on port %d (%d authorized PKs)", skyenv.DmsgGRPCPort, len(authorizedPKs))
				if err := dmsgGRPCServer.Serve(authL); err != nil {
					if !errors.Is(err, net.ErrClosed) &&
						!strings.Contains(err.Error(), "closed") {
						dmsgGRPCLog.WithError(err).Error("DMSG gRPC server error")
					}
				}
			}()

			// Skynet mirror of the gRPC server at the same port.
			// authorizedDmsgListener wraps the skynet listener too —
			// PK extraction uses the transport-agnostic remotePK
			// helper, so the same whitelist enforcement runs on both
			// transports.
			goServeSkynetMirror(ctx, v.conf.PK, skyenv.DmsgGRPCPort, "dmsg_grpc", dmsgGRPCLog,
				func(skyLis net.Listener) {
					authSky := &authorizedDmsgListener{
						Listener:      skyLis,
						authorizedPKs: authorizedPKs,
						log:           dmsgGRPCLog,
					}
					if err := dmsgGRPCServer.Serve(authSky); err != nil &&
						!errors.Is(err, net.ErrClosed) &&
						!strings.Contains(err.Error(), "closed") {
						dmsgGRPCLog.WithError(err).Debug("Skynet gRPC server exited")
					}
				})
		}
	}

	// Serve visor RPC over transports (route ID 0, VisorRPCPacket).
	// Uses the same whitelist as DMSG gRPC: hypervisor PKs + dmsgpty whitelist.
	if v.tpM != nil {
		whitelistPKs := append([]cipher.PubKey{}, v.conf.Hypervisors...)
		if v.conf.Dmsgpty != nil {
			whitelistPKs = append(whitelistPKs, v.conf.Dmsgpty.Whitelist...)
		}
		if len(whitelistPKs) > 0 {
			tpRPCLog := v.MasterLogger().PackageLogger("transport_rpc")
			tpRPCS, tpRPCErr := newRPCServer(v, "TransportRPC")
			if tpRPCErr != nil {
				log.WithError(tpRPCErr).Warn("Failed to create transport RPC server")
			} else {
				tpRPCSrv := NewTransportRPCServer(tpRPCLog, tpRPCS, whitelistPKs, v.tpM)
				v.pushCloseStack("transport_rpc", tpRPCSrv.Close)
				go tpRPCSrv.Serve()
				tpRPCLog.WithField("whitelist_pks", len(whitelistPKs)).
					Info("Transport RPC server started (VisorRPCPacket on route ID 0)")
			}
		}
	}

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
			if !errors.Is(err, net.ErrClosed) &&
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
				if errors.Is(err, net.ErrClosed) ||
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
			if !errors.Is(err, net.ErrClosed) &&
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

		ctx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
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

		// Auto-discover LAN DMSG server from this hypervisor
		go v.discoverLANDmsgServer() //nolint:gosec
	}

	return nil
}

func initHypervisor(ctx context.Context, v *Visor, log *logging.Logger) error {
	if v.conf.Hypervisor == nil {
		v.log.Debug("hypervisor config not present, skipping")
		return nil
	}

	conf := *v.conf.Hypervisor
	conf.PK = v.conf.PK
	conf.SK = v.conf.SK
	conf.DmsgDiscovery = v.conf.Dmsg.Discovery

	// Prepare hypervisor.
	hv, err := NewHypervisor(conf, v, v.dmsgC)
	if err != nil {
		return fmt.Errorf("failed to start hypervisor: %w", err)
	}

	// Store instance on visor for runtime enable/disable via RPC
	v.hvInstance = hv

	// Start LAN DMSG server if configured
	if conf.LANDmsgServer != nil && conf.LANDmsgServer.Enable {
		lanServer, err := startLANDmsgServer(conf.LANDmsgServer, v.conf, v.MasterLogger())
		if err != nil {
			v.log.WithError(err).Warn("Failed to start LAN DMSG server")
		} else {
			hv.lanDmsg = lanServer
			v.log.WithField("pk", lanServer.PK).WithField("addr", lanServer.Address).Info("LAN DMSG server started")
			v.pushCloseStack("lan_dmsg_server", func() error {
				return lanServer.Server.Close()
			})

			go func() {
				lanEntry := &dmsgdisc.Entry{
					Static: lanServer.PK,
					Server: &dmsgdisc.Server{
						Address:           lanServer.Address,
						AvailableSessions: 100,
					},
				}
				if err := v.dmsgC.EnsureSession(ctx, lanEntry); err != nil {
					v.log.WithError(err).Warn("Failed to connect hypervisor DMSG client to LAN server")
				} else {
					v.log.Info("Hypervisor DMSG client connected to LAN DMSG server")
				}
			}()
		}
	}

	// Needed for modern browsers (correct MIME type for JavaScript).
	if err := mime.AddExtensionType(".js", "application/javascript"); err != nil {
		log.WithError(err).Warn("Unable to register js mime type")
	}

	// Enable the hypervisor if configured to auto-start
	if conf.Enable {
		if err := hv.Enable(ctx); err != nil {
			return fmt.Errorf("failed to enable hypervisor: %w", err)
		}
	} else {
		v.log.Info("Hypervisor configured but not enabled (use 'skywire cli visor hv enable' to start)")
	}

	v.pushCloseStack("hypervisor", func() error {
		return hv.Disable()
	})

	return nil
}

// authorizedDmsgListener wraps a net.Listener and rejects connections from
// unauthorized PKs. Only PKs in the authorizedPKs map are allowed to connect.
// This protects the gRPC server (gotop stats, etc.) from unauthorized access.
//
// Works with both dmsg and skynet (appnet) listeners — PK extraction
// uses the transport-agnostic remotePK helper. Despite the historical
// "Dmsg" name, the same wrapper is used for the skynet mirror.
type authorizedDmsgListener struct {
	net.Listener
	authorizedPKs map[cipher.PubKey]bool
	log           *logging.Logger
}

func (l *authorizedDmsgListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}

		pk, ok := remotePK(conn.RemoteAddr())
		if !ok {
			// Fall back to the dmsg-specific RawRemoteAddr() for older
			// listener types that don't surface PK via RemoteAddr.
			type dmsgAddrProvider interface {
				RawRemoteAddr() dmsg.Addr
			}
			if stream, dmsgOK := conn.(dmsgAddrProvider); dmsgOK {
				pk = stream.RawRemoteAddr().PK
				ok = true
			}
		}
		if !ok {
			l.log.Warn("Rejected gRPC connection: unable to determine remote PK")
			conn.Close() //nolint:errcheck,gosec
			continue
		}
		if !l.authorizedPKs[pk] {
			l.log.WithField("remote_pk", pk).Warn("Rejected unauthorized gRPC connection")
			conn.Close() //nolint:errcheck,gosec
			continue
		}
		l.log.WithField("remote_pk", pk).Debug("Accepted authorized gRPC connection")
		return conn, nil
	}
}
