// Package visor pkg/visor/config_refresh.go c3-vis-core
package visor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
	"github.com/skycoin/skywire/pkg/visor/visorcore"
)

const configRefreshInterval = 1 * time.Hour

// deploymentServicesFile is where, under local_path, the visor keeps the
// services config it last applied. It is how a later refresh tells a value
// the deployment set (still equal to it) from one the operator set.
const deploymentServicesFile = "deployment_services.json"

// startConfigRefresh periodically fetches the deployment's services config
// from the conf service over dmsg-HTTP and applies it. It is the fallback
// for the conf service's CXO feed (conf_cxo.go), which delivers the same
// document as soon as it changes; the two share applyDeploymentServices.
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
			if services := v.fetchServicesConfig(ctx, log); services != nil {
				v.applyDeploymentServices(services, "conf service (dmsg-http)", log)
			}
		}
	}
}

// applyDeploymentServices applies next, the deployment's current services
// config, to the visor config (see visorconfig.ApplyDeploymentServices),
// writes the config file when anything changed, and records next as the
// config last applied. The deployment's dmsg servers go into the dmsg-server
// cache, which bootstrap merges over the configured list.
//
// The key sets take effect at once; their readers go through the Effective*
// accessors on every use. A changed service address is saved but its client
// was built at startup, so it takes effect when the visor restarts.
func (v *Visor) applyDeploymentServices(next *visorconfig.Services, source string, log *logging.Logger) {
	v.deploySvcMu.Lock()
	defer v.deploySvcMu.Unlock()

	next.BackfillClearnetFromDmsg()
	path := filepath.Join(v.conf.LocalPath, deploymentServicesFile)
	prev := v.deploySvcLast
	if prev == nil {
		prev = loadDeploymentServices(path, log)
	}

	if changed := v.conf.ApplyDeploymentServices(next, prev); len(changed) > 0 {
		log.WithField("source", source).WithField("fields", strings.Join(changed, ",")).
			Info("Deployment services config updated; changed service addresses take effect after restart")
		if err := v.conf.Flush(); err != nil && !errors.Is(err, visorconfig.ErrNoConfigPath) {
			log.WithError(err).Warn("Failed to write the updated config")
		}
	}

	if v.dmsgServersCache != nil {
		for _, e := range deployment.DmsgServerEntriesToDisc(next.DmsgServers) {
			if e.Server == nil || e.Server.Address == "" {
				continue
			}
			if err := v.dmsgServersCache.Set(e); err != nil {
				log.WithError(err).Debug("Failed to cache a deployment dmsg server")
			}
		}
	}

	if prev == nil || !servicesEqual(prev, next) {
		saveDeploymentServices(path, next, log)
	}
	v.deploySvcLast = next
}

// loadDeploymentServices reads the services config last applied, or returns
// nil when none has been.
func loadDeploymentServices(path string, log *logging.Logger) *visorconfig.Services {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.WithError(err).Debug("Failed to read the last applied services config")
		}
		return nil
	}
	var s visorconfig.Services
	if err := json.Unmarshal(data, &s); err != nil {
		log.WithError(err).Debug("Failed to parse the last applied services config")
		return nil
	}
	return &s
}

func saveDeploymentServices(path string, s *visorconfig.Services, log *logging.Logger) {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0600); err != nil {
		log.WithError(err).Debug("Failed to save the applied services config")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.WithError(err).Debug("Failed to save the applied services config")
	}
}

func servicesEqual(a, b *visorconfig.Services) bool {
	ja, errA := json.Marshal(a)
	jb, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(ja) == string(jb)
}

// fetchServicesConfig fetches the services config from the conf service
// over dmsg-HTTP. Deployment services are dmsg-only; there is no clearnet
// fallback.
func (v *Visor) fetchServicesConfig(ctx context.Context, log *logging.Logger) *visorconfig.Services {
	confDmsg := visorcore.ResolveServices(v.conf).ConfDmsg
	if confDmsg == "" || v.dmsgC == nil {
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	transport := dmsghttp.MakeHTTPTransport(fetchCtx, v.dmsgC)
	client := &http.Client{Transport: transport, Timeout: 25 * time.Second}

	resp, err := client.Get(confDmsg)
	if err != nil {
		log.WithError(err).Debug("Config refresh via DMSG failed")
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		log.WithError(err).Warn("Failed to read conf service response")
		return nil
	}
	services, err := parseServicesConfig(data)
	if err != nil {
		log.WithError(err).Warn("Failed to parse conf service response")
		return nil
	}
	return services
}

// parseServicesConfig parses a services config document. The conf service
// serves a flat Services object; the {"prod": …, "test": …} envelope of
// older conf services is accepted too, and its prod half is used.
func parseServicesConfig(data []byte) (*visorconfig.Services, error) {
	var env visorconfig.EnvServices
	if err := json.Unmarshal(data, &env); err == nil && len(env.Prod) > 0 {
		data = env.Prod
	}
	var services visorconfig.Services
	if err := json.Unmarshal(data, &services); err != nil {
		return nil, err
	}
	if services.ConfDmsg == "" && services.DmsgDiscoveryDmsg == "" && services.DmsgDiscovery == "" &&
		len(services.DmsgServers) == 0 {
		return nil, errors.New("no deployment services in the document")
	}
	return &services, nil
}
