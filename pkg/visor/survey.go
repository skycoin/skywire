// Package visor pkg/visor/survey.go
package visor

import (
	"fmt"
	"os"
	"strings"
	"time"

	coincipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/util/pathutil"
	visconf "github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// GenerateSurvey generate survey handler
func GenerateSurvey(v *Visor, log *logging.Logger, routine bool) {
	if visconf.IsRoot() {
		for {
			// check for valid reward address set as prerequisite for generating the system survey
			rewardAddressBytes, err := os.ReadFile(v.conf.LocalPath + "/" + visconf.RewardFile) //nolint
			if err == nil || true {
				//remove any newline from rewardAddress string
				rewardAddress := strings.TrimSuffix(string(rewardAddressBytes), "\n")
				// //validate the skycoin address
				cAddr, err := coincipher.DecodeBase58Address(rewardAddress)
				if err != nil {
					log.WithError(err).Error("Invalid skycoin reward address.")
					return
				}
				log.Info("Skycoin reward address: ", cAddr.String())
				//generate the system survey
				pathutil.EnsureDir(v.conf.LocalPath) //nolint
				generatedSurvey, err := visconf.SystemSurvey()
				if err != nil {
					log.WithError(err).Error("Could not read system info.")
					return
				}
				generatedSurvey.PubKey = v.conf.PK
				// generatedSurvey.SkycoinAddress = cAddr.String()

				// TODO: add connected dmsg servers and services URL to survey
				generatedSurvey.ServicesURLs.TransportDiscovery = v.conf.Transport.Discovery
				generatedSurvey.ServicesURLs.AddressResolver = v.conf.Transport.AddressResolver
				generatedSurvey.ServicesURLs.RouteFinder = v.conf.Routing.RouteFinder
				generatedSurvey.ServicesURLs.RouteSetupNodes = v.conf.Routing.RouteSetupNodes
				generatedSurvey.ServicesURLs.UptimeTracker = v.conf.UptimeTracker.Addr
				generatedSurvey.ServicesURLs.ServiceDiscovery = v.conf.Launcher.ServiceDisc
				generatedSurvey.DmsgServers = v.dmsgC.ConnectedServersPK()

				log.Info("Generating system survey")
				v.surveyLock.Lock()
				v.survey = generatedSurvey
				v.surveyLock.Unlock()
			} else {
				v.surveyLock.Lock()
				v.survey = visconf.Survey{}
				v.surveyLock.Unlock()
				log.Debug("Removed hadware survey for visor not seeking rewards")
			}
			// break loop for generate each 24hours if just reward address chenged
			if !routine {
				break
			}
			fmt.Println(v.survey)
			time.Sleep(time.Hour)
		}
	}
}
