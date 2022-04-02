package visorconfig

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	utilenv "github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

var (
	services *Services
	svcconf  = strings.ReplaceAll(utilenv.ServiceConfAddr, "http://", "")
)

//Fetch fetches the service URLs & ip:ports from the config service endpoint
func Fetch(mLog *logging.MasterLogger, serviceConfURL string, stdout bool) *Services {

	urlstr := []string{"http://", serviceConfURL}
	serviceConf := strings.Join(urlstr, "")
	client := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}
	//create the http request
	req, err := http.NewRequest(http.MethodGet, serviceConf, nil)
	if err != nil {
		mLog.WithError(err).Fatal("Failed to create http request\n")
	}
	//check for errors in the response
	res, err := client.Do(req)
	if err != nil {
		if serviceConfURL != svcconf {
			//if serviceConfURL was changed this error should be fatal
			mLog.WithError(err).Fatal("Failed to fetch servers\n")
		} else { //otherwise just error and continue
			//silence errors for stdout
			if !stdout {
				mLog.WithError(err).Error("Failed to fetch servers\n")
				mLog.Warn("Falling back on hardcoded servers")
			}
		}
	} else {
		// nil error from client.Do(req)
		if res.Body != nil {
			defer res.Body.Close() //nolint
		}
		body, err := ioutil.ReadAll(res.Body)
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
	SetupNodes         []cipher.PubKey `json:"setup_nodes"`
	UptimeTracker      string          `json:"uptime_tracker"`
	ServiceDiscovery   string          `json:"service_discovery"`
	StunServers        []string        `json:"stun_servers"`
}