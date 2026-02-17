// Package visor pkg/visor/public_autocheck.go
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// PublicAutocheckDelay defines the interval for checking transport latencies.
	PublicAutocheckDelay = 1 * time.Hour

	// LatenciesFile is the filename for latency results.
	LatenciesFile = "latencies.json"
)

// TransportLatency holds latency information for a single transport.
type TransportLatency struct {
	TPID      string        `json:"tp_id"`
	RemotePK  string        `json:"remote_pk"`
	Type      string        `json:"type"`
	Label     string        `json:"label"`
	Latency   time.Duration `json:"latency_ns"`
	LatencyMs float64       `json:"latency_ms"`
	Success   bool          `json:"success"`
	Error     string        `json:"error,omitempty"`
	CheckedAt time.Time     `json:"checked_at"`
}

// LatencyReport holds the complete latency report for all transports.
type LatencyReport struct {
	VisorPK     string             `json:"visor_pk"`
	CheckedAt   time.Time          `json:"checked_at"`
	Transports  []TransportLatency `json:"transports"`
	TotalCount  int                `json:"total_count"`
	OnlineCount int                `json:"online_count"`
}

// Autochecker continuously checks transport latencies.
type Autochecker struct {
	log       *logging.Logger
	tm        *transport.Manager
	localPath string
	localPK   cipher.PubKey

	report   *LatencyReport
	reportMu sync.RWMutex
}

// NewAutochecker creates a new Autochecker.
func NewAutochecker(log *logging.Logger, tm *transport.Manager, localPath string, localPK cipher.PubKey) *Autochecker {
	return &Autochecker{
		log:       log,
		tm:        tm,
		localPath: localPath,
		localPK:   localPK,
	}
}

// Run starts the autocheck loop.
func (ac *Autochecker) Run(ctx context.Context, v *Visor) error {
	// Initial check after a short delay
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		ac.checkAllTransports(ctx, v)
	}()

	// Periodic checks
	ticker := time.NewTicker(PublicAutocheckDelay)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return context.Canceled
		case <-ticker.C:
			ac.checkAllTransports(ctx, v)
		}
	}
}

// checkAllTransports checks latency of all automatic/skycoin transports.
func (ac *Autochecker) checkAllTransports(ctx context.Context, v *Visor) {
	ac.log.Info("Starting public autocheck cycle")

	// Get all transports with automatic or skycoin labels
	automaticTps := ac.tm.GetTransportsByLabel(transport.LabelAutomatic)
	skycoinTps := ac.tm.GetTransportsByLabel(transport.LabelSkycoin)

	// Combine transports (deduplicate by ID)
	tpMap := make(map[string]*transport.ManagedTransport)
	for _, tp := range automaticTps {
		tpMap[tp.Entry.ID.String()] = tp
	}
	for _, tp := range skycoinTps {
		tpMap[tp.Entry.ID.String()] = tp
	}

	if len(tpMap) == 0 {
		ac.log.Debug("No automatic/skycoin transports to check")
		return
	}

	ac.log.WithField("count", len(tpMap)).Info("Checking transports")

	var latencies []TransportLatency
	onlineCount := 0
	checkedAt := time.Now()

	for tpID, tp := range tpMap {
		select {
		case <-ctx.Done():
			ac.log.Debug("Context canceled, stopping autocheck")
			return
		default:
		}

		remotePK := tp.Remote()
		tpType := tp.Type()
		label := string(tp.Entry.Label)

		latency := TransportLatency{
			TPID:      tpID,
			RemotePK:  remotePK.String(),
			Type:      string(tpType),
			Label:     label,
			CheckedAt: checkedAt,
		}

		// Ping the transport
		pingStart := time.Now()
		var pingErr error

		switch tpType {
		case tptypes.STCPR, tptypes.SUDPH:
			// Use route ping for direct transports
			pingErr = ac.pingViaRoute(ctx, v, remotePK)
		case tptypes.DMSG:
			// Use dmsg ping for dmsg transports
			pingErr = ac.pingViaDMSG(ctx, v, remotePK)
		default:
			pingErr = fmt.Errorf("unsupported transport type: %s", tpType)
		}

		pingDuration := time.Since(pingStart)

		if pingErr != nil {
			latency.Success = false
			latency.Error = pingErr.Error()
			ac.log.WithField("tp_id", tpID[:8]).
				WithField("remote", remotePK.String()[:16]).
				WithError(pingErr).
				Debug("Transport ping failed")

			// Remove failed transport
			ac.tm.DeleteTransport(tp.Entry.ID)
			ac.log.WithField("tp_id", tpID[:8]).
				Info("Removed failed transport")
		} else {
			latency.Success = true
			latency.Latency = pingDuration
			latency.LatencyMs = float64(pingDuration.Microseconds()) / 1000.0
			onlineCount++
			ac.log.WithField("tp_id", tpID[:8]).
				WithField("remote", remotePK.String()[:16]).
				WithField("latency_ms", latency.LatencyMs).
				Debug("Transport ping succeeded")
		}

		latencies = append(latencies, latency)
	}

	// Update the report
	report := &LatencyReport{
		VisorPK:     ac.localPK.String(),
		CheckedAt:   checkedAt,
		Transports:  latencies,
		TotalCount:  len(latencies),
		OnlineCount: onlineCount,
	}

	ac.reportMu.Lock()
	ac.report = report
	ac.reportMu.Unlock()

	// Save to file
	if err := ac.saveReport(report); err != nil {
		ac.log.WithError(err).Warn("Failed to save latency report")
	}

	ac.log.WithField("total", len(latencies)).
		WithField("online", onlineCount).
		Info("Completed public autocheck cycle")
}

// pingViaRoute pings a remote visor via transport route.
func (ac *Autochecker) pingViaRoute(ctx context.Context, v *Visor, remotePK cipher.PubKey) error {
	// Create ping config
	conf := PingConfig{
		PK:       remotePK,
		Tries:    1,
		PcktSize: 2, // 2 KB packet
	}

	// Dial first to establish connection
	err := v.DialPing(conf)
	if err != nil {
		return err
	}
	defer func() {
		_ = v.StopPing(remotePK) //nolint:errcheck
	}()

	// Check context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Perform a single ping test
	_, err = v.Ping(conf)
	return err
}

// pingViaDMSG pings a remote visor via DMSG.
func (ac *Autochecker) pingViaDMSG(ctx context.Context, v *Visor, remotePK cipher.PubKey) error {
	// Use the visor's dmsg ping functionality
	err := v.DialDmsgPing(remotePK)
	if err != nil {
		return err
	}
	defer func() {
		_ = v.StopDmsgPing(remotePK) //nolint:errcheck
	}()

	// Check context
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	// Perform a single ping test
	conf := PingConfig{
		PK:       remotePK,
		Tries:    1,
		PcktSize: 2, // 2 KB packet
	}
	_, err = v.DmsgPing(conf)
	return err
}

// saveReport saves the latency report to a JSON file.
func (ac *Autochecker) saveReport(report *LatencyReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal report: %w", err)
	}

	filePath := filepath.Join(ac.localPath, LatenciesFile)
	if err := os.WriteFile(filePath, data, 0644); err != nil { //nolint:gosec
		return fmt.Errorf("failed to write report: %w", err)
	}

	return nil
}

// GetReport returns the current latency report.
func (ac *Autochecker) GetReport() *LatencyReport {
	ac.reportMu.RLock()
	defer ac.reportMu.RUnlock()
	return ac.report
}

// GetLatenciesHTML returns an auto-refreshing HTML page with latency data.
func (ac *Autochecker) GetLatenciesHTML() string {
	ac.reportMu.RLock()
	report := ac.report
	ac.reportMu.RUnlock()

	if report == nil {
		return `<!DOCTYPE html>
<html>
<head>
    <title>Transport Latencies</title>
    <meta http-equiv="refresh" content="60">
    <style>
        body { font-family: monospace; margin: 20px; background: #1a1a2e; color: #eee; }
        h1 { color: #0f3460; }
        .loading { color: #888; }
    </style>
</head>
<body>
    <h1>Transport Latencies</h1>
    <p class="loading">No data available yet. Waiting for first check...</p>
    <p><small>Auto-refresh: 60 seconds</small></p>
</body>
</html>`
	}

	// Build HTML table
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Transport Latencies - %s</title>
    <meta http-equiv="refresh" content="60">
    <style>
        body { font-family: monospace; margin: 20px; background: #1a1a2e; color: #eee; }
        h1 { color: #16213e; }
        .summary { margin: 15px 0; padding: 10px; background: #16213e; border-radius: 5px; }
        table { border-collapse: collapse; width: 100%%; margin-top: 15px; }
        th, td { border: 1px solid #0f3460; padding: 8px; text-align: left; }
        th { background: #0f3460; }
        tr:nth-child(even) { background: #16213e; }
        .success { color: #00ff88; }
        .failed { color: #ff4444; }
        .latency { color: #44aaff; }
        .timestamp { color: #888; font-size: 0.9em; }
    </style>
</head>
<body>
    <h1>Transport Latencies</h1>
    <div class="summary">
        <strong>Visor:</strong> %s<br>
        <strong>Last Check:</strong> <span class="timestamp">%s</span><br>
        <strong>Total:</strong> %d | <strong>Online:</strong> <span class="success">%d</span> | <strong>Offline:</strong> <span class="failed">%d</span>
    </div>
    <table>
        <tr>
            <th>Transport ID</th>
            <th>Remote PK</th>
            <th>Type</th>
            <th>Label</th>
            <th>Status</th>
            <th>Latency</th>
        </tr>`,
		report.VisorPK[:16],
		report.VisorPK[:16],
		report.CheckedAt.Format(time.RFC3339),
		report.TotalCount,
		report.OnlineCount,
		report.TotalCount-report.OnlineCount,
	)

	for _, tp := range report.Transports {
		statusClass := "success"
		status := "Online"
		latencyStr := fmt.Sprintf("%.2f ms", tp.LatencyMs)
		if !tp.Success {
			statusClass = "failed"
			status = "Offline"
			latencyStr = tp.Error
			if len(latencyStr) > 50 {
				latencyStr = latencyStr[:50] + "..."
			}
		}

		html += fmt.Sprintf(`
        <tr>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
            <td>%s</td>
            <td class="%s">%s</td>
            <td class="latency">%s</td>
        </tr>`,
			tp.TPID[:8],
			tp.RemotePK[:16],
			tp.Type,
			tp.Label,
			statusClass,
			status,
			latencyStr,
		)
	}

	html += `
    </table>
    <p class="timestamp"><small>Auto-refresh: 60 seconds</small></p>
</body>
</html>`

	return html
}
