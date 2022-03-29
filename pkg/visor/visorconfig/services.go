package visorconfig

import (
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skycoin/src/util/logging"
)

var (
	services *Services
	svcconf  = strings.ReplaceAll(serviceconfaddr, "http://", "") //skyenv.DefaultServiceConfAddr
)

const serviceconfaddr = "http://conf.skywire.skycoin.com"

//Fetch fetches the service URLs & ip:ports from the config service endpoint
func Fetch(mLog *logging.MasterLogger, serviceConfURL string, stdout bool) *Services {

	urlstr := []string{"http://", serviceConfURL, "/config"}
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
	}
	return services
}
