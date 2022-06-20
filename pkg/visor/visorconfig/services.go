package visorconfig

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

// Fetch fetches the service URLs & ip:ports from the config service endpoint
func Fetch(mLog *logging.MasterLogger, serviceConfURL string, stdout bool) (services *Services) {

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

/*
// Online retrieves the online status from the uptime tracker
func Online(pk string) (bool, error) {
	var uptime *Uptime
	urlstr := []string{"http://ut.skywire.skycoin.com/uptimes?visors=", pk}
	url := strings.Join(urlstr, "")
	client := http.Client{
		Timeout: time.Second * 2, // Timeout after 2 seconds
	}
	//create the http request
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	//check for errors in the response
	res, err := client.Do(req)
	if err != nil {
		return false, err
	}

	if res.Body != nil {
		defer res.Body.Close() //nolint
	}
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return false, err
	}
	//fill in services struct with the response
	err = json.Unmarshal(body, &uptime)
	if err != nil {
		return false, err
	}
	var online bool
	for i := range uptime {
		 online = uptime[i].Online
		 break
	}
	return online, nil
}
*/

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

/*
type Uptime []struct {
	Key        string  `json:"key"`
	Uptime     int     `json:"uptime"`
	Downtime   int     `json:"downtime"`
	Percentage float64 `json:"percentage"`
	Online     bool    `json:"online"`
}
*/
