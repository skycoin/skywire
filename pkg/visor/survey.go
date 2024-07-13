// Package visor pkg/visor/survey.go
package visor

import (
	"encoding/json"
	"os"
	"strings"
	"time"
	"context"

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
				survey, err := visconf.SystemSurvey(v.conf.Dmsg.Discovery)
				if err != nil {
					log.WithError(err).Error("Could not read system info.")
					return
				}
				survey.PubKey = v.conf.PK
				survey.SkycoinAddress = cAddr.String()
				survey.ServicesURLs.DmsgDiscovery = v.conf.Dmsg.Discovery
				survey.ServicesURLs.TransportDiscovery = v.conf.Transport.Discovery
				survey.ServicesURLs.AddressResolver = v.conf.Transport.AddressResolver
				survey.ServicesURLs.RouteFinder = v.conf.Routing.RouteFinder
				survey.ServicesURLs.RouteSetupNodes = v.conf.Routing.RouteSetupNodes
				survey.ServicesURLs.TransportSetupPKs = v.conf.Transport.TransportSetupPKs
				survey.ServicesURLs.UptimeTracker = v.conf.UptimeTracker.Addr
				survey.ServicesURLs.ServiceDiscovery = v.conf.Launcher.ServiceDisc
				survey.ServicesURLs.SurveyWhitelist = v.conf.SurveyWhitelist
				survey.ServicesURLs.StunServers = v.conf.StunServers
				survey.DmsgServers = v.dmsgC.ConnectedServersPK()

				//use the existing dmsg client of the visor to get ip from dmsg server
				tries := 8
				for tries > 0 {
					ipAddr, err := v.dmsgC.LookupIP(context.Background(), nil)
					if err != nil {
						tries--
						continue
					}
					survey.IPAddr = ipAddr.String()
					break
				}

				log.Info("Generating system survey")
				v.surveyLock.Lock()
				v.survey = survey
				v.surveyLock.Unlock()

				// Save survey as file
				s, err := json.MarshalIndent(survey, "", "\t")
				if err != nil {
					log.WithError(err).Error("Could not marshal json.")
					return
				}
				err = os.WriteFile(v.conf.LocalPath+"/"+visconf.NodeInfo, []byte(s), 0644) //nolint
				if err != nil {
					log.WithError(err).Error("Failed to write system hardware survey to file.")
					return
				}
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
			time.Sleep(24 * time.Hour)
		}
	}
}
