// Package visorconfig pkg/visor/visorconfig/services.go
package visorconfig

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// Fetch fetches the service URLs & ip:ports from the config service endpoint
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
			defer res.Body.Close() //nolint
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

// Services are subdomains and IP addresses of the skywire services
type Services struct {
	DmsgDiscovery      string          `json:"dmsg_discovery"`
	TransportDiscovery string          `json:"transport_discovery"`
	AddressResolver    string          `json:"address_resolver"`
	RouteFinder        string          `json:"route_finder"`
	RouteSetupNodes    []cipher.PubKey `json:"route_setup_nodes"`
	TransportSetupPKs  []cipher.PubKey `json:"transport_setup"`
	UptimeTracker      string          `json:"uptime_tracker"`
	ServiceDiscovery   string          `json:"service_discovery"`
	StunServers        []string        `json:"stun_servers"`
	DNSServer          string          `json:"dns_server"`
	SurveyWhitelist    []cipher.PubKey `json:"survey_whitelist"`
}
