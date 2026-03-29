// Package visor pkg/visor/survey.go
package visor

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/util/pathutil"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
	visconf "github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// GenerateSurvey generates the system survey for reward eligibility verification.
// The survey is only generated if a valid reward address is set.
// Does not require root — only needs write access to the visor's local path.
func GenerateSurvey(v *Visor, log *logging.Logger, routine bool) {
	for {
		// Check for valid reward address as prerequisite
		rewardAddressBytes, err := os.ReadFile(v.conf.LocalPath + "/" + skyenv.RewardFile) //nolint:gosec
		if err != nil {
			v.surveyLock.Lock()
			v.survey = visconf.Survey{}
			v.surveyLock.Unlock()
			log.Debug("No reward address set — survey not generated")
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		rewardAddress := strings.TrimSpace(string(rewardAddressBytes))
		if rewardAddress == "" {
			log.Debug("Reward address is empty — survey not generated")
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		canonical, _, err := rewardconfig.ValidateRewardAddress(rewardAddress)
		if err != nil {
			log.WithError(err).Warn("Invalid reward address — survey not generated")
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		log.Info("Reward address: ", canonical)

		// Ensure the local directory exists and is writable
		if err := pathutil.EnsureDir(v.conf.LocalPath); err != nil {
			log.WithError(err).Errorf("Cannot create survey directory %s", v.conf.LocalPath)
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		// Generate the system survey (does not require root)
		survey, err := visconf.SystemSurvey()
		if err != nil {
			log.WithError(err).Error("Could not read system info for survey")
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		survey.PubKey = v.conf.PK
		survey.SkycoinAddress = canonical
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

		// Get public IP from DMSG server
		for tries := 8; tries > 0; tries-- {
			ipAddr, err := v.dmsgC.LookupIP(context.Background(), nil)
			if err != nil {
				continue
			}
			survey.IPAddr = ipAddr.String()
			break
		}

		log.Info("Generating system survey")
		v.surveyLock.Lock()
		v.survey = survey
		v.surveyLock.Unlock()

		// Save survey to file
		s, err := json.MarshalIndent(survey, "", "\t")
		if err != nil {
			log.WithError(err).Error("Could not marshal survey to JSON")
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		surveyPath := v.conf.LocalPath + "/" + skyenv.NodeInfo
		if err := os.WriteFile(surveyPath, s, 0644); err != nil { //nolint:gosec
			log.WithError(err).Errorf("Failed to write survey to %s (check directory permissions)", surveyPath)
			if !routine {
				return
			}
			time.Sleep(24 * time.Hour)
			continue
		}

		log.Infof("Survey written to %s", surveyPath)

		if !routine {
			return
		}
		time.Sleep(24 * time.Hour)
	}
}
