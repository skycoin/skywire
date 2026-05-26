//go:build !js

// Package visorconfig pkg/visor/visorconfig/services_native.go
//
// Native (non-WASM) implementation of Fetch, the HTTP helper that
// retrieves a Services payload from a conf-service endpoint. Lives
// here (rather than in services.go) so the WASM build graph for
// genvisor/autoconfigcmd/install-page doesn't pull in net/http.
// See services.go's package doc for context.
package visorconfig

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/logging"
)

// EnvServices is the wrapper struct for the outer JSON. Aliased to
// deployment.EnvServices (only defined under !js — see deployment's
// config.go package doc for the WASM-stripping rationale). Consumers
// using visorconfig.EnvServices keep working under non-WASM builds.
type EnvServices = deployment.EnvServices

// Fetch fetches the service URLs & ip:ports from the config service
// endpoint over HTTP. Used by the visor's CLI config-gen path; not
// reachable from the WASM-clean genvisor.Generate flow (which uses
// the embedded deployment.Prod instead).
func Fetch(mLog *logging.MasterLogger, serviceConf string, stdout bool) (services *Services) {
	client := http.Client{
		Timeout: time.Second * 15, // Timeout after 15 seconds
	}
	//create the http request
	req, err := http.NewRequest(http.MethodGet, serviceConf, nil)
	if err != nil {
		mLog.WithError(err).Fatal("Failed to create http request\n")
	}
	req.Header.Add("Cache-Control", "no-cache")
	//check for errors in the response
	res, err := client.Do(req)
	if err != nil {
		//silence errors for stdout
		if !stdout {
			mLog.WithError(err).Error("Failed to fetch servers\n")
			mLog.Warn("Falling back on hardcoded servers")
		}
	} else {
		// nil error from client.Do(req)
		if res.Body != nil {
			defer res.Body.Close() //nolint:errcheck
		}
		body, err := io.ReadAll(res.Body)
		if err != nil {
			mLog.WithError(err).Fatal("Failed to read response\n")
		}
		//fill in services struct with the response
		err = json.Unmarshal(body, &services)
		if err != nil {
			mLog.WithError(err).Fatal("Failed to unmarshal json response\n")
		}
		if !stdout {
			mLog.Infof("Fetched service endpoints from '%s'", serviceConf)
		}
	}
	return services
}
