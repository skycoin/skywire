// init_dmsg.go contains DMSG initialization logic.
package visor

import (
	"context"
	crand "crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/dmsg/direct"
	dmsgdisc "github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgpty"
	"github.com/skycoin/skywire/pkg/dmsgc"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/util/osutil"
	"github.com/skycoin/skywire/pkg/visor/dmsgtracker"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func initDmsgHTTP(ctx context.Context, v *Visor, _ *logging.Logger) error {
	var keys cipher.PubKeys
	servers := shuffleServers(v.conf.Dmsg.Servers)

	if len(servers) == 0 {
		return nil
	}

	log := v.MasterLogger().PackageLogger("dmsg_http")
	keys = append(keys, v.conf.PK)
	entries := direct.GetAllEntries(keys, servers)
	dClient := direct.NewClient(entries, v.MasterLogger().PackageLogger("dmsg_http:direct_client"))

	// Set dClient immediately for direct discovery access.
	v.initLock.Lock()
	v.dClient = dClient
	v.initLock.Unlock()

	// Start DMSG HTTP connection in background so it doesn't block visor startup.
	// Downstream modules check v.dmsgHTTP != nil before using DMSG transport
	// and fall back to plain HTTP if it's not ready yet.
	go func() {
		dmsgDC, closeDmsgDC, err := direct.StartDmsg(ctx, v.MasterLogger().PackageLogger("dmsg_http:dmsgDC"),
			v.conf.PK, v.conf.SK, dClient, dmsg.DefaultConfig())
		if err != nil {
			log.WithError(err).Warn("DMSG HTTP transport unavailable")
			return
		}

		dmsgHTTP := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}

		v.pushCloseStack("dmsg_http", func() error {
			closeDmsgDC()
			return nil
		})

		v.initLock.Lock()
		v.dmsgHTTP = &dmsgHTTP
		v.dmsgDC = dmsgDC
		v.initLock.Unlock()
		close(v.dmsgHTTPReady)

		log.Info("DMSG HTTP transport ready")
	}()

	return nil
}

func shuffleServers(in []*dmsgdisc.Entry) []*dmsgdisc.Entry {
	n := len(in)
	for i := n - 1; i > 0; i-- {
		jBig, err := crand.Int(crand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			panic(err)
		}
		j := int(jBig.Int64())
		in[i], in[j] = in[j], in[i]
	}
	return in
}

/*
func rotateServers(servers []*dmsgdisc.Entry) {
	if len(servers) == 0 {
		return
	}
	first := servers[0]
	copy(servers, servers[1:])
	servers[len(servers)-1] = first
}
*/

func initDmsg(ctx context.Context, v *Visor, log *logging.Logger) (err error) {
	if v.conf.Dmsg == nil {
		return fmt.Errorf("cannot initialize dmsg: empty configuration")
	}

	// Prefer DMSG-HTTP for discovery if configured (more private, no DNS dependency),
	// fall back to plain HTTP URL. If HTTP URL is empty (DMSG-only deployment),
	// DMSG is required — not optional.
	discURL := v.conf.Dmsg.Discovery
	if v.conf.Dmsg.DiscoveryDmsg != "" && v.dmsgHTTP != nil {
		if _, err := getHTTPClient(ctx, v, v.conf.Dmsg.DiscoveryDmsg); err == nil {
			discURL = v.conf.Dmsg.DiscoveryDmsg
			log.Info("Using DMSG-HTTP for dmsg discovery")
		} else if discURL != "" {
			log.WithError(err).Warn("DMSG-HTTP discovery failed, using plain HTTP")
		} else {
			return fmt.Errorf("DMSG-only deployment but DMSG discovery unreachable: %w", err)
		}
	} else if discURL == "" && v.conf.Dmsg.DiscoveryDmsg != "" {
		// DMSG URL set but dmsgHTTP not ready — can't proceed without either
		discURL = v.conf.Dmsg.DiscoveryDmsg
		log.Warn("HTTP discovery URL empty, attempting DMSG discovery without dmsgHTTP transport")
	}

	httpC, err := getHTTPClient(ctx, v, discURL)
	if err != nil {
		return err
	}
	// Override the discovery URL used by the DMSG client
	dmsgConf := *v.conf.Dmsg
	dmsgConf.Discovery = discURL
	dmsgC := dmsgc.New(v.conf.PK, v.conf.SK, v.ebc, &dmsgConf, httpC, v.MasterLogger())
	wg := new(sync.WaitGroup)
	wg.Add(1)
	go func() {
		defer wg.Done()
		dmsgC.Serve(ctx)
	}()

	v.pushCloseStack("dmsg", func() error {
		if err := dmsgC.Close(); err != nil {
			return err
		}
		wg.Wait()
		return nil
	})

	v.initLock.Lock()
	v.dmsgC = dmsgC
	v.initLock.Unlock()

	// Wait for DMSG to connect before returning. All modules that depend on
	// dmsgC will only start after this, ensuring DMSG is ready before any
	// service tries to use it. Without this, services start dialing over DMSG
	// before sessions are established, causing unnecessary HTTP fallbacks.
	const dmsgInitTimeout = 30 * time.Second
	select {
	case <-dmsgC.Ready():
		log.Info("DMSG client connected and ready.")
	case <-time.After(dmsgInitTimeout):
		log.Warn("DMSG client not ready after timeout, continuing (services may fall back to HTTP)")
	case <-ctx.Done():
		return ctx.Err()
	}

	// Start periodic config refresh for dynamic key sets
	go v.startConfigRefresh(ctx) //nolint:errcheck,gosec

	return nil
}

func initDmsgCtrl(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// DMSG should already be ready (initDmsg waits for it).
	// Initialize the transport manager's DMSG client.
	logger := dmsgC.Logger()
	logger.Debug("Initializing DMSG transport client...")
	v.tpM.InitDmsgClient(ctx, dmsgC)

	// dmsgctrl setup — listen for incoming control streams (ping/pong).
	// Each accepted Control is self-serving (handles ping/pong in its own goroutine).
	// We drain the channel so the listener doesn't block on a full buffer.
	cl, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
	if err != nil {
		return err
	}
	v.pushCloseStack("dmsgctrl", cl.Close)

	ctrlCh := dmsgctrl.ServeListener(cl, 16)
	go func() {
		for ctrl := range ctrlCh {
			// Each control is already self-serving via ctrl.serve().
			// We just hold a reference so the GC doesn't collect it prematurely.
			_ = ctrl
		}
	}()
	return nil
}

func initDmsgHTTPLogServer(ctx context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return fmt.Errorf("cannot initialize dmsg log server: dmsg not configured")
	}
	logger := v.MasterLogger().PackageLogger("dmsghttp_logserver")

	var printLog bool
	if v.MasterLogger().GetLevel() == logrus.DebugLevel || v.MasterLogger().GetLevel() == logrus.TraceLevel {
		printLog = true
	}

	//whitelist access to the surveys for the hypervisor, dmsggpty whitelist, and for the surveywhitelist of keys which is fetched from the conf service
	var whitelistedPKs []cipher.PubKey
	if sw := v.conf.EffectiveSurveyWhitelist(); sw != nil {
		whitelistedPKs = append(whitelistedPKs, sw...)
	}
	if v.conf.Hypervisors != nil {
		whitelistedPKs = append(whitelistedPKs, v.conf.Hypervisors...)
	}
	if v.conf.Dmsgpty != nil {
		if v.conf.Dmsgpty.Whitelist != nil {
			whitelistedPKs = append(whitelistedPKs, v.conf.Dmsgpty.Whitelist...)
		}
	}

	lsAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, "", whitelistedPKs, &v.survey.data, printLog)

	// Set visor as health stats provider for /health endpoint
	lsAPI.SetHealthStatsProvider(v)

	// Store the log server API reference for public autocheck to use later
	v.initLock.Lock()
	v.logServer.api = lsAPI
	v.initLock.Unlock()

	lis, err := dmsgC.Listen(visorconfig.DmsgHTTPPort)
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		if err := lis.Close(); err != nil {
			logger.WithError(err).Error("Failed to close DMSG HTTP listener")
		}
	}()

	logger.WithField("dmsg_addr", fmt.Sprintf("dmsg://%v", lis.Addr().String())).
		Debug("Serving...")
	// Increased timeouts for dmsg latency characteristics
	// DMSG has higher latency than direct TCP due to multi-hop routing
	srv := &http.Server{
		ReadTimeout:       skyenv.HTTPReadTimeout,
		WriteTimeout:      skyenv.HTTPWriteTimeout,
		IdleTimeout:       skyenv.HTTPIdleTimeout,
		ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
		Handler:           lsAPI,
	}

	wg := new(sync.WaitGroup)
	wg.Add(1)

	go func() {
		defer wg.Done()
		err = srv.Serve(lis)
		if errors.Is(err, dmsg.ErrEntityClosed) {
			return
		}
		if errors.Is(err, http.ErrServerClosed) {
			return
		}
		if err != nil {
			logger.WithError(err).Error("Logserver exited with error.")
		}
	}()
	v.pushCloseStack("dmsghttp.logserver", func() error {
		// Graceful shutdown
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.WithError(err).Warn("HTTP server shutdown error")
		}
		wg.Wait()
		return nil
	})

	// Optionally serve on localhost as well (no whitelist authentication)
	if v.conf.LogServer != nil && v.conf.LogServer.LocalAddr != "" {
		localAddr := v.conf.LogServer.LocalAddr
		logger.WithField("local_addr", localAddr).Info("Starting localhost log server")

		// Create a separate API without whitelist authentication for localhost
		localAPI := logserver.New(logger, v.conf.Transport.LogStore.Location, v.conf.LocalPath, "", nil, &v.survey.data, printLog)

		// Set visor as health stats provider for /health endpoint
		localAPI.SetHealthStatsProvider(v)

		// Store the localhost API for potential future use
		v.logServer.localAPI = localAPI

		localLis, err := net.Listen("tcp", localAddr)
		if err != nil {
			logger.WithError(err).WithField("local_addr", localAddr).Warn("Failed to start localhost log server")
		} else {
			localSrv := &http.Server{
				ReadTimeout:       5 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       60 * time.Second,
				ReadHeaderTimeout: 5 * time.Second,
				Handler:           localAPI,
			}

			localWg := new(sync.WaitGroup)
			localWg.Add(1)

			go func() {
				defer localWg.Done()
				if err := localSrv.Serve(localLis); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.WithError(err).Error("Localhost logserver exited with error")
				}
			}()

			v.pushCloseStack("localhost.logserver", func() error {
				shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := localSrv.Shutdown(shutdownCtx); err != nil {
					logger.WithError(err).Warn("Localhost HTTP server shutdown error")
				}
				localWg.Wait()
				return nil
			})

			logger.WithField("local_addr", localAddr).Info("Localhost log server started")
		}
	}

	return nil
}

func initDmsgTrackers(_ context.Context, v *Visor, _ *logging.Logger) error {
	dmsgC := v.dmsgC

	dtm := dmsgtracker.NewDmsgTrackerManager(v.MasterLogger(), dmsgC, 0, 0)
	v.pushCloseStack("dmsg_tracker_manager", func() error {
		return dtm.Close()
	})
	v.initLock.Lock()
	v.dmsgTracker.manager = dtm
	v.initLock.Unlock()
	v.dmsgTracker.readyOnce.Do(func() { close(v.dmsgTracker.ready) })
	return nil
}

// nolint: gocyclo
//
//gocyclo:ignore
func initDmsgpty(ctx context.Context, v *Visor, log *logging.Logger) error {
	conf := v.conf.Dmsgpty

	if conf == nil {
		log.Debug("'dmsgpty' is not configured, skipping.")
		return nil
	}

	// Unlink dmsg socket files (just in case).
	if conf.CLINet == "unix" {
		if runtime.GOOS == "windows" {
			conf.CLIAddr = dmsgpty.ParseWindowsEnv(conf.CLIAddr)
		}

		if err := osutil.UnlinkSocketFiles(v.conf.Dmsgpty.CLIAddr); err != nil {
			log.WithError(err).Errorf("Insufficient permissions to unlink socket file %q", v.conf.Dmsgpty.CLIAddr)
			return err
		}
	}

	wl := dmsgpty.NewMemoryWhitelist()

	// Initialize the dmsgpty whitelist
	if err := wl.Add(v.conf.Dmsgpty.Whitelist...); err != nil {
		return err
	}

	// Ensure hypervisors are added to the whitelist.
	if err := wl.Add(v.conf.Hypervisors...); err != nil {
		return err
	}
	// add the visor's own public key to the whitelist to allow local pty
	if err := wl.Add(v.conf.PK); err != nil {
		v.log.Errorf("Cannot add itself to the pty whitelist: %s", err)
	}

	dmsgC := v.dmsgC
	if dmsgC == nil {
		err := errors.New("cannot create dmsgpty with nil dmsg client")
		return err
	}

	pty := dmsgpty.NewHost(dmsgC, wl)

	if ptyPort := conf.DmsgPort; ptyPort != 0 {
		serveCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ListenAndServe(serveCtx, ptyPort); err != nil {
				runtimeErrors <- fmt.Errorf("listen and serve stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			wg.Wait()
			return nil
		})

	}

	if conf.CLINet != "" {

		if conf.CLINet == "unix" {
			if err := os.MkdirAll(filepath.Dir(conf.CLIAddr), ownerRWX); err != nil {
				err := fmt.Errorf("failed to prepare unix file for dmsgpty cli listener: %w", err)
				return err
			}
		}

		cliL, err := net.Listen(conf.CLINet, conf.CLIAddr)
		if err != nil {
			err := fmt.Errorf("failed to start dmsgpty cli listener: %w", err)
			return err
		}

		serveCtx, cancel := context.WithCancel(context.Background()) //nolint:gosec // cancel is called in pushCloseStack
		wg := new(sync.WaitGroup)
		wg.Add(1)

		go func() {
			defer wg.Done()
			runtimeErrors := getErrors(ctx)
			if err := pty.ServeCLI(serveCtx, cliL); err != nil {
				runtimeErrors <- fmt.Errorf("serve cli stopped: %w", err)
			}
		}()

		v.pushCloseStack("router.serve", func() error {
			cancel()
			err := cliL.Close()
			wg.Wait()
			return err
		})
	}

	return nil
}

func initDmsgPing(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	lis, err := dmsgC.Listen(skyenv.DmsgPingPort)
	if err != nil {
		return err
	}

	v.pushCloseStack("dmsg_ping", lis.Close)

	go func() {
		var wg sync.WaitGroup
		defer wg.Wait()
		for {
			conn, err := lis.Accept()
			if err != nil {
				if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
					log.WithError(err).Error("Failed to accept dmsg ping conn")
				}
				return
			}
			log.Debugf("Accepted dmsg ping conn from %s", conn.RemoteAddr())
			wg.Add(1)
			go func() {
				defer wg.Done()
				handleDmsgPingConn(log, conn)
			}()
		}
	}()

	log.WithField("port", skyenv.DmsgPingPort).Info("Dmsg ping listener started")
	return nil
}

func handleDmsgPingConn(log *logging.Logger, conn net.Conn) {
	defer func() {
		if err := conn.Close(); err != nil {
			log.WithError(err).Debug("Error closing dmsg ping conn")
		}
	}()

	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Error("Failed to read dmsg ping packet")
			}
			return
		}

		var size PingSizeMsg
		err = json.Unmarshal(buf[:n], &size)
		if err != nil {
			log.WithError(err).Error("Failed to unmarshal dmsg ping size")
			return
		}

		// Ack the size message
		_, err = conn.Write([]byte("ok"))
		if err != nil {
			log.WithError(err).Error("Failed to write dmsg ping ack")
			return
		}

		// Read the full ping payload
		var ping []byte
		for len(ping) < size.Size {
			n, err = conn.Read(buf)
			if err != nil {
				if !errors.Is(err, io.EOF) {
					log.WithError(err).Error("Failed to read dmsg ping data")
				}
				return
			}
			ping = append(ping, buf[:n]...)
		}

		// Echo back for RTT measurement
		// If EchoFull is set, echo the full payload for bandwidth testing
		if size.EchoFull {
			_, err = conn.Write(ping)
			if err != nil {
				log.WithError(err).Error("Failed to write full dmsg ping echo")
				return
			}
			log.Debugf("Echoed full dmsg ping response (%d bytes)", len(ping))
		} else {
			_, err = conn.Write([]byte("pong"))
			if err != nil {
				log.WithError(err).Error("Failed to write dmsg ping echo")
				return
			}
			log.Debug("Echoed dmsg ping response")
		}
	}
}

// initDmsgServerLatency initializes DMSG server latency tracking.
// It self-pings via each connected DMSG server on startup and hourly.
func initDmsgServerLatency(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg client to be ready
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	// Helper to measure latency to all connected servers
	measureServerLatencies := func() {
		servers := dmsgC.ConnectedServersPK()
		if len(servers) == 0 {
			log.Debug("No DMSG servers connected, skipping latency measurement")
			return
		}

		log.WithField("servers", len(servers)).Info("Measuring DMSG server latencies via self-ping")

		for _, serverPKStr := range servers {
			var serverPK cipher.PubKey
			if err := serverPK.Set(serverPKStr); err != nil {
				log.WithError(err).WithField("server", serverPKStr).Warn("Invalid server PK")
				continue
			}

			// Self-ping via this server (ping our own PK through the server)
			start := time.Now()
			conf := PingConfig{
				PK:       v.conf.PK,
				Tries:    3,
				PcktSize: 2, // 2KB
			}

			// Use DmsgPingViaServer to ping ourselves through this specific server
			latencies, err := v.DmsgPingViaServer(conf, serverPK)
			if err != nil {
				log.WithError(err).WithField("server", serverPKStr[:16]+"...").Warn("Failed to measure server latency")
				continue
			}

			// Calculate average latency
			var totalLatency time.Duration
			for _, lat := range latencies {
				totalLatency += lat
			}
			avgLatency := totalLatency / time.Duration(len(latencies))

			// Store the latency
			v.dmsgLatency.mu.Lock()
			v.dmsgLatency.servers[serverPK] = avgLatency
			v.dmsgLatency.mu.Unlock()

			log.WithFields(logrus.Fields{
				"server":  serverPKStr[:16] + "...",
				"latency": avgLatency.Round(time.Millisecond),
				"elapsed": time.Since(start).Round(time.Millisecond),
			}).Info("Measured DMSG server latency")
		}
	}

	// Initial measurement
	go func() {
		// Small delay to allow more servers to connect
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		measureServerLatencies()
	}()

	// Hourly measurement
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				measureServerLatencies()
			}
		}
	}()

	log.Info("DMSG server latency tracking started")
	return nil
}
