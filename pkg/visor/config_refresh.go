// Package visor pkg/visor/config_refresh.go
package visor

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

const configRefreshInterval = 1 * time.Hour

// startConfigRefresh periodically refreshes dynamic key sets from the conf service.
// This allows route_setup_nodes, transport_setup, and survey_whitelist to be
// updated without restarting the visor or regenerating the config.
//
// Only the deployment-managed fields are touched. User-added keys live in
// the separate user_route_setup_nodes / user_transport_setup / user_survey_whitelist
// fields and are merged at use time via the Effective* accessors. This preserves
// any manually added keys across refreshes.
func (v *Visor) startConfigRefresh(ctx context.Context) {
	log := v.MasterLogger().PackageLogger("config_refresh")

	// Initial delay to let DMSG connect
	select {
	case <-time.After(2 * time.Minute):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(configRefreshInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			v.refreshKeySets(ctx, log)
		}
	}
}

func (v *Visor) refreshKeySets(ctx context.Context, log *logging.Logger) {
	services := v.fetchServicesConfig(ctx, log)
	if services == nil {
		return
	}

	updated := false

	// Update route setup nodes
	if len(services.RouteSetupNodes) > 0 && !pubKeysEqual(v.conf.Routing.RouteSetupNodes, services.RouteSetupNodes) {
		log.Infof("Updating route_setup_nodes: %d keys", len(services.RouteSetupNodes))
		v.conf.Routing.RouteSetupNodes = services.RouteSetupNodes
		updated = true
	}

	// Update transport setup PKs
	if len(services.TransportSetupPKs) > 0 && !pubKeysEqual(v.conf.Transport.TransportSetupPKs, services.TransportSetupPKs) {
		log.Infof("Updating transport_setup: %d keys", len(services.TransportSetupPKs))
		v.conf.Transport.TransportSetupPKs = services.TransportSetupPKs
		updated = true
	}

	// Update survey whitelist
	if len(services.SurveyWhitelist) > 0 && !pubKeysEqual(v.conf.SurveyWhitelist, services.SurveyWhitelist) {
		log.Infof("Updating survey_whitelist: %d keys", len(services.SurveyWhitelist))
		v.conf.SurveyWhitelist = services.SurveyWhitelist
		updated = true
	}

	// Update GeoIP URL
	if services.GeoIP != "" && services.GeoIP != v.conf.GeoIP {
		log.Infof("Updating geoip: %s", services.GeoIP)
		v.conf.GeoIP = services.GeoIP
		updated = true
	}

	if updated {
		log.Info("Dynamic key sets refreshed from conf service")
	}
}

func (v *Visor) fetchServicesConfig(ctx context.Context, log *logging.Logger) *visorconfig.Services {
	// Prefer URLs from visor config; fall back to embedded deployment defaults.
	confDmsg := v.conf.ConfServiceDmsg
	if confDmsg == "" {
		confDmsg = deployment.Prod.ConfDmsg
	}
	confHTTP := v.conf.ConfService
	if confHTTP == "" {
		confHTTP = deployment.ProdConf.Conf
	}

	// Try DMSG first
	if confDmsg != "" && v.dmsgC != nil {
		fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		transport := dmsghttp.MakeHTTPTransport(fetchCtx, v.dmsgC)
		client := &http.Client{Transport: transport, Timeout: 25 * time.Second}

		resp, err := client.Get(confDmsg)
		if err == nil {
			defer resp.Body.Close() //nolint:errcheck
			return parseServicesResponse(resp.Body, log)
		}
		log.WithError(err).Debug("Config refresh via DMSG failed, trying HTTP")
	}

	// HTTP fallback
	if confHTTP != "" {
		client := &http.Client{Timeout: 15 * time.Second}
		resp, err := client.Get(confHTTP) //nolint:gosec
		if err == nil {
			defer resp.Body.Close() //nolint:errcheck
			return parseServicesResponse(resp.Body, log)
		}
		log.WithError(err).Debug("Config refresh via HTTP also failed")
	}

	return nil
}

func parseServicesResponse(body io.Reader, log *logging.Logger) *visorconfig.Services {
	data, err := io.ReadAll(body)
	if err != nil {
		log.WithError(err).Warn("Failed to read conf service response")
		return nil
	}

	// The conf service returns {prod: {...}, test: {...}}
	var envServices visorconfig.EnvServices
	if err := json.Unmarshal(data, &envServices); err != nil {
		log.WithError(err).Warn("Failed to parse conf service response")
		return nil
	}

	var services visorconfig.Services
	if err := json.Unmarshal(envServices.Prod, &services); err != nil {
		log.WithError(err).Warn("Failed to parse prod services config")
		return nil
	}

	return &services
}

func pubKeysEqual(a, b []cipher.PubKey) bool {
	if len(a) != len(b) {
		return false
	}
	set := make(map[cipher.PubKey]struct{}, len(a))
	for _, k := range a {
		set[k] = struct{}{}
	}
	for _, k := range b {
		if _, ok := set[k]; !ok {
			return false
		}
	}
	return true
}
