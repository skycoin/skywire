//go:build js && wasm

// Package main — peer-interface parity for the browser visor.
//
// A native visor automatically serves a handful of dmsg "peer interfaces" so
// other visors / hypervisors / the CLI can reach, health-check, latency-probe,
// and (with authorization) control it. A browser tab is a real visor too, but it
// only opened the router await-setup (136) and hypervisor-mirror (46) listeners.
// This wires the rest of the parity set so a wasm visor is a first-class peer:
//
//	dmsg:80  HTTP — /health (httputil.HealthCheckResponse, identical wire to the
//	               native log-server) and a default landing page naming the tab's
//	               PK. Reachable via fetchDmsg(<pk>:80, "/health") and
//	               `skywire-cli svc health <pk>`. (Liveness/RTT is NOT an HTTP
//	               endpoint here — it lives on dmsg:7/dmsg:8 below, matching native.)
//	dmsg:7   dmsgctrl ping/pong — what dmsgtracker (and the HV UI's dmsg latency)
//	               uses to see this visor as alive + measure RTT.
//	dmsg:8   dmsg ping protocol — `skywire-cli dmsg ping <pk>` RTT/bandwidth echo.
//	dmsg:47  transport-setup ACCEPT — a hypervisor / transport-setup node can
//	               command this visor to add/remove transports (gated by the
//	               TransportSetupNodes trust list). Accept side only; the tab does
//	               not act as a transport-setup *node* (no outbound orchestration).
//
// NOT served (deliberately): dmsg:36 route-setup *node* (orchestrating routes for
// others — a browser leaf accepts route rules on 136 but doesn't orchestrate),
// and dmsg:22 dmsgpty (no shell in a browser; a limited console is a follow-up).
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
	ts "github.com/skycoin/skywire/pkg/transport/setup"
	"github.com/skycoin/skywire/pkg/visor/logserver/landingpage"
)

// peerStartedAt is this visor's boot time, reported on /health (StartedAt). Set
// once by startPeerServices before any handler can read it.
var peerStartedAt time.Time

// startPeerServices opens the dmsg peer-interface listeners (ports 80, 7, 8, 47).
// Called once near the end of bootEdge, after dmsg + the transport manager + the
// router are up. Uses the package vars ctx, selfPK, dmsgC, tpM. tsNodes is the
// transport-setup trust list (visorcore.Services.TransportSetupNodes).
func startPeerServices(mLog *logging.MasterLogger, tsNodes []cipher.PubKey) {
	peerStartedAt = time.Now().UTC()

	// Port 80: install the dynamic /health handler, register a default landing
	// page, and start the dmsg:80 HTTP server. serveContent() from the page can
	// still add paths on top (dynamicHandler wins for /health).
	dynamicHandler = peerDynamicEndpoint
	registerDefaultLanding()
	contentMu.Lock()
	started := !servingPorts[contentPort]
	servingPorts[contentPort] = true
	contentMu.Unlock()
	if started {
		go serveContentOverDmsg(contentPort)
	}

	go servePeerCtrl()                    // dmsg:7
	go servePeerPing(mLog)                // dmsg:8
	go serveTransportSetup(mLog, tsNodes) // dmsg:47
}

// peerDynamicEndpoint serves /health on port 80 with live data, matching the
// native visor's log-server wire (httputil.HealthCheckResponse).
func peerDynamicEndpoint(port uint16, path string) (string, []byte, bool) {
	if port != contentPort {
		return "", nil, false
	}
	switch path {
	case "/health":
		resp := httputil.HealthCheckResponse{
			ServiceName: "visor",
			BuildInfo:   buildinfo.Get(),
			StartedAt:   peerStartedAt,
			// dmsg_address carries the PK (host part) + the HTTP port, exactly as the
			// native /health does; PublicKey stays empty (omitempty), shown on the page.
			DmsgAddr: fmt.Sprintf("%s:%d", selfPK.Hex(), contentPort),
		}
		b, err := json.Marshal(resp)
		if err != nil {
			return "", nil, false
		}
		return "application/json", b, true
	}
	return "", nil, false
}

// registerDefaultLanding installs a minimal landing page at "/" on port 80 that
// names the tab's PK (and links the peer endpoints), so a visitor to
// http://<pk>.dmsg/ sees a real page — like the native visor's landing page. A
// later serveContent({"/":...}) call from the page overrides it.
func registerDefaultLanding() {
	// Render with the SAME shared package the native visor's log-server uses, so a
	// browser wasm-visor and a native visor present an identical landing page at
	// http://<pk>.dmsg/ (and skywire.dmsg). /health is the one HTTP endpoint the
	// wasm-visor serves on port 80 that native also serves (liveness/RTT lives on
	// the dedicated dmsg:7 dmsgctrl + dmsg:8 ping ports, not an HTTP endpoint); the
	// frame is shared and native adds its own whitelist-gated links.
	links := []string{
		`<a href="/health">/health</a> - visor health status`,
	}
	html := landingpage.Render(selfPK.Hex(), links)
	contentMu.Lock()
	cm := contentByPort[contentPort]
	if cm == nil {
		cm = map[string]contentEntry{}
		contentByPort[contentPort] = cm
	}
	if _, exists := cm["/"]; !exists {
		// enabled:true is REQUIRED — handleContentStream serves any entry with
		// enabled==false (the contentEntry zero value) as 404, so without this the
		// default landing page silently 404s and skywire.dmsg (→ this visor's PK)
		// shows nothing, unlike native visors.
		cm["/"] = contentEntry{ct: "text/html; charset=utf-8", body: []byte(html), enabled: true}
	}
	contentMu.Unlock()
}

// servePeerCtrl serves dmsgctrl ping/pong on dmsg:7. Each accepted Control is
// self-serving (handles ping/pong in its own goroutine); we drain the channel so
// the accept loop never blocks. This is what dmsgtracker dials to measure RTT.
func servePeerCtrl() {
	lis, err := dmsgC.Listen(skyenv.DmsgCtrlPort)
	if err != nil {
		vlog(fmt.Sprintf("peer: dmsgctrl listen :%d: %s", skyenv.DmsgCtrlPort, err.Error()))
		return
	}
	go func() {
		<-ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	vlog(fmt.Sprintf("peer: dmsgctrl serving on dmsg:%d", skyenv.DmsgCtrlPort))
	for range dmsgctrl.ServeListener(lis, 16) { //nolint:revive
	}
}

// pingSizeMsg is the dmsg ping protocol header (matches the native visor's
// PingSizeMsg wire). The client sends it, the server acks "ok", reads Size bytes,
// then echoes "pong" (or the full payload when EchoFull, for bandwidth tests).
type pingSizeMsg struct {
	Size     int  `json:"size"`
	EchoFull bool `json:"echo_full,omitempty"`
}

// servePeerPing serves the dmsg ping protocol on dmsg:8 (`cli dmsg ping`).
func servePeerPing(mLog *logging.MasterLogger) {
	log := mLog.PackageLogger("dmsg_ping")
	lis, err := dmsgC.Listen(skyenv.DmsgPingPort)
	if err != nil {
		vlog(fmt.Sprintf("peer: dmsg ping listen :%d: %s", skyenv.DmsgPingPort, err.Error()))
		return
	}
	go func() {
		<-ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	vlog(fmt.Sprintf("peer: dmsg ping serving on dmsg:%d", skyenv.DmsgPingPort))
	for {
		conn, err := lis.Accept()
		if err != nil {
			return
		}
		go handlePingConn(log, conn)
	}
}

func handlePingConn(log *logging.Logger, conn net.Conn) {
	defer conn.Close() //nolint:errcheck
	for {
		buf := make([]byte, 32*1024)
		n, err := conn.Read(buf)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				log.WithError(err).Debug("dmsg ping read")
			}
			return
		}
		var size pingSizeMsg
		if err := json.Unmarshal(buf[:n], &size); err != nil {
			log.WithError(err).Debug("dmsg ping size unmarshal")
			return
		}
		if _, err := conn.Write([]byte("ok")); err != nil {
			return
		}
		var ping []byte
		for len(ping) < size.Size {
			n, err = conn.Read(buf)
			if err != nil {
				return
			}
			ping = append(ping, buf[:n]...)
		}
		if size.EchoFull {
			if _, err := conn.Write(ping); err != nil {
				return
			}
		} else if _, err := conn.Write([]byte("pong")); err != nil {
			return
		}
	}
}

// serveTransportSetup serves the transport-setup ACCEPT side on dmsg:47: a
// hypervisor / transport-setup node in tsNodes can command this visor to add or
// remove transports (TransportGateway RPC). Reuses the shared, build-clean
// pkg/transport/setup listener — the same one the native visor runs. tsNodes is
// the trust list; a peer not in it is rejected at accept time.
func serveTransportSetup(mLog *logging.MasterLogger, tsNodes []cipher.PubKey) {
	tl, err := ts.NewTransportListener(ctx, selfPK, tsNodes, dmsgC, tpM, mLog)
	if err != nil {
		vlog(fmt.Sprintf("peer: transport-setup listener: %s", err.Error()))
		return
	}
	vlog(fmt.Sprintf("peer: transport-setup serving on dmsg:%d (%d trusted node(s))", skyenv.DmsgTransportSetupPort, len(tsNodes)))
	tl.Serve(ctx)
}
