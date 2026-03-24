// api_tps.go contains embedded Transport Setup Node (TPS) API methods.
package visor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/rpc"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/tpviz"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TPSStatus returns the status of the embedded Transport Setup Node.
func (v *Visor) TPSStatus() (*TPSStatus, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	status := &TPSStatus{
		Enabled: tps != nil,
	}
	if tps != nil {
		status.PubKey = tps.pk
	}
	return status, nil
}

// TPSAddTransport uses the embedded TPS to add a transport on a target visor.
func (v *Visor) TPSAddTransport(targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := tps.AddTransport(ctx, targetPK, remotePK, types.Type(tpType))
	if err != nil {
		return nil, err
	}

	return &TPSTransportResponse{
		ID:     resp.ID,
		Local:  resp.Local,
		Remote: resp.Remote,
		Type:   string(resp.Type),
	}, nil
}

// TPSRemoveTransport uses the embedded TPS to remove a transport on a target visor.
func (v *Visor) TPSRemoveTransport(targetPK cipher.PubKey, tpID uuid.UUID) error {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	return tps.RemoveTransport(ctx, targetPK, tpID)
}

// TPSGetTransports uses the embedded TPS to get transports from a target visor.
func (v *Visor) TPSGetTransports(targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	v.initLock.RLock()
	tps := v.embeddedTPS
	v.initLock.RUnlock()

	if tps == nil {
		return nil, fmt.Errorf("embedded TPS not configured (tps_sk not set)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := tps.GetTransports(ctx, targetPK)
	if err != nil {
		return nil, err
	}

	result := make([]TPSTransportResponse, len(resp))
	for i, tr := range resp {
		result[i] = TPSTransportResponse{
			ID:     tr.ID,
			Local:  tr.Local,
			Remote: tr.Remote,
			Type:   string(tr.Type),
		}
	}
	return result, nil
}

// GetTransportSetupNodes returns the whitelisted transport setup node public keys.
func (v *Visor) GetTransportSetupNodes() ([]cipher.PubKey, error) {
	return v.conf.Transport.TransportSetupPKs, nil
}

// GetTransportSetupNodesSorted returns TPS nodes sorted by health (healthy first, then by latency).
func (v *Visor) GetTransportSetupNodesSorted() ([]cipher.PubKey, error) {
	if v.nodeHealthTracker == nil {
		// Fall back to unsorted list if health tracker not initialized
		return v.conf.Transport.TransportSetupPKs, nil
	}
	return v.nodeHealthTracker.GetTPSNodesSorted(), nil
}

// GetRouteSetupNodesSorted returns RSN nodes sorted by health (healthy first, then by latency).
func (v *Visor) GetRouteSetupNodesSorted() ([]cipher.PubKey, error) {
	if v.nodeHealthTracker == nil {
		// Fall back to unsorted list if health tracker not initialized
		return v.conf.Routing.RouteSetupNodes, nil
	}
	return v.nodeHealthTracker.GetRSNNodesSorted(), nil
}

// GetTPSHealth returns health status for all configured TPS nodes.
func (v *Visor) GetTPSHealth() ([]NodeHealth, error) {
	if v.nodeHealthTracker == nil {
		return nil, fmt.Errorf("node health tracker not initialized")
	}
	return v.nodeHealthTracker.GetTPSHealth(), nil
}

// GetRSNHealth returns health status for all configured RSN nodes.
func (v *Visor) GetRSNHealth() ([]NodeHealth, error) {
	if v.nodeHealthTracker == nil {
		return nil, fmt.Errorf("node health tracker not initialized")
	}
	return v.nodeHealthTracker.GetRSNHealth(), nil
}

// TPSExternalHealthCheck dials an external TPS over dmsg and performs a health check.
func (v *Visor) TPSExternalHealthCheck(tpsPK cipher.PubKey) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	var reply TPSHealthCheckReply
	if err := client.Call("SetupRPCGateway.HealthCheck", &TPSHealthCheckArgs{}, &reply); err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}

	if reply.Status != "OK" {
		return fmt.Errorf("health check returned non-OK status: %s", reply.Status)
	}
	return nil
}

// TPSExternalAddTransport dials an external TPS over dmsg and requests transport setup.
func (v *Visor) TPSExternalAddTransport(tpsPK, targetPK, remotePK cipher.PubKey, tpType string) (*TPSTransportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	req := &TPSSetupRequest{
		TargetPK: targetPK,
		RemotePK: remotePK,
		Type:     tpType,
	}
	var reply TPSSetupResponse
	if err := client.Call("SetupRPCGateway.AddTransport", req, &reply); err != nil {
		return nil, fmt.Errorf("AddTransport RPC failed: %w", err)
	}

	return &TPSTransportResponse{
		ID:     reply.ID,
		Local:  reply.Local,
		Remote: reply.Remote,
		Type:   reply.Type,
	}, nil
}

// TPSExternalGetTransports dials an external TPS over dmsg and requests transport list.
func (v *Visor) TPSExternalGetTransports(tpsPK, targetPK cipher.PubKey) ([]TPSTransportResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := v.dmsgC.Dial(ctx, dmsg.Addr{PK: tpsPK, Port: skyenv.DmsgTransportSetupServicePort})
	if err != nil {
		return nil, fmt.Errorf("failed to dial TPS %s: %w", tpsPK, err)
	}
	defer conn.Close() //nolint:errcheck

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck

	req := &TPSGetTransportsRequest{TargetPK: targetPK}
	var reply TPSGetTransportsResponse
	if err := client.Call("SetupRPCGateway.GetTransports", req, &reply); err != nil {
		return nil, fmt.Errorf("GetTransports RPC failed: %w", err)
	}

	result := make([]TPSTransportResponse, len(reply.Transports))
	for i, tr := range reply.Transports {
		result[i] = TPSTransportResponse(tr)
	}
	return result, nil
}

// StartUIServer starts the embedded UI server on the specified address.
// If addr is empty, uses the configured LocalAddr or defaults to localhost:8081.
func (v *Visor) StartUIServer(addr string) error {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	if v.uiServerRunning {
		return fmt.Errorf("UI server is already running on %s", v.uiServerAddr)
	}

	// Determine address
	if addr == "" {
		if v.conf.UIServer != nil && v.conf.UIServer.LocalAddr != "" {
			addr = v.conf.UIServer.LocalAddr
		} else {
			addr = "localhost:8081"
		}
	}

	// Create tpviz server
	tpvizCfg := tpviz.DefaultConfig()
	tpvizServer := tpviz.NewServer(tpvizCfg)

	// Set visor API directly via adapter
	adapter := &visorAPIAdapter{v: v}
	tpvizServer.SetVisorAPI(adapter, v.conf.PK.Hex())

	// Start cache refresh
	tpvizServer.Start()

	// Start HTTP server
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		tpvizServer.Stop()
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	srv := &http.Server{
		Handler:           tpvizServer.Handler(),
		ReadTimeout:       skyenv.HTTPReadTimeout,
		WriteTimeout:      skyenv.HTTPWriteTimeout,
		IdleTimeout:       skyenv.HTTPIdleTimeout,
		ReadHeaderTimeout: skyenv.HTTPReadHeaderTimeout,
	}

	go func() {
		v.log.Infof("UI server started on http://%s", addr)
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			v.log.WithError(err).Error("UI server exited with error")
		}
	}()

	v.uiServer = srv
	v.uiServerAddr = addr
	v.uiServerRunning = true

	return nil
}

// StopUIServer stops the embedded UI server if running.
func (v *Visor) StopUIServer() error {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	if !v.uiServerRunning {
		return fmt.Errorf("UI server is not running")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := v.uiServer.Shutdown(ctx); err != nil {
		v.log.WithError(err).Warn("UI server shutdown error")
	}

	v.uiServer = nil
	v.uiServerAddr = ""
	v.uiServerRunning = false

	v.log.Info("UI server stopped")
	return nil
}

// UIServerStatus returns the current status of the UI server.
func (v *Visor) UIServerStatus() (*UIServerStatus, error) {
	v.uiServerMu.Lock()
	defer v.uiServerMu.Unlock()

	status := &UIServerStatus{
		Running: v.uiServerRunning,
	}

	if v.uiServerRunning {
		status.LocalAddr = v.uiServerAddr
	}

	// Include configured DMSG port if UI server config exists
	if v.conf.UIServer != nil {
		status.DmsgPort = v.conf.UIServer.DmsgPort
	}

	return status, nil
}
