// Package clisurvey cmd/skywire-cli/commands/survey/root.go
package clisurvey

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/skycoin/src/cipher"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var (
	logger   = logging.MustGetLogger("skywire-visor") //nolint:unused
	mLog     = logging.NewMasterLogger()
	log      = mLog.PackageLogger("survey")
	confPath string
	dmsgDisc string
	//	stdin                bool
	//	confArg              string
	pkg              bool
	usr              bool
	pkgconfigexists  bool
	userconfigexists bool
	conf             *visorconfig.V1
)

func init() {
	surveyCmd.Flags().SortFlags = false
	surveyCmd.Flags().StringVarP(&confPath, "config", "c", "", "optionl config file to use (i.e.: "+visorconfig.ConfigName+")")
	surveyCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", skyenv.DmsgDiscAddr, "value of dmsg discovery")
	//	surveyCmd.Flags().StringVarP(&confArg, "confarg", "C", "", "supply config as argument")
	//	surveyCmd.Flags().BoolVarP(&stdin, "stdin", "n", false, "read config from stdin")
	if _, err := os.Stat(visorconfig.SkywirePath + "/" + visorconfig.ConfigJSON); err == nil {
		pkgconfigexists = true
	}
	if _, err := os.Stat(visorconfig.HomePath() + "/" + visorconfig.ConfigName); err == nil {
		userconfigexists = true
	}
	if pkgconfigexists {
		surveyCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "use package config "+visorconfig.SkywirePath+"/"+visorconfig.ConfigJSON)
	}
	if userconfigexists {
		surveyCmd.Flags().BoolVarP(&usr, "user", "u", false, "use config at: "+visorconfig.HomePath()+"/"+visorconfig.ConfigName)
	}
}

// RootCmd is surveyCmd
var RootCmd = surveyCmd

var surveyCmd = &cobra.Command{
	Use:                   "survey",
	DisableFlagsInUseLine: true,
	Short:                 "system survey",
	Long:                  "print the system survey",
	Run: func(cmd *cobra.Command, args []string) {
		if pkg {
			confPath = visorconfig.SkywirePath + "/" + visorconfig.ConfigJSON
		}
		if usr {
			confPath = visorconfig.HomePath() + "/" + visorconfig.ConfigName
		}

		if confPath != "" {
			confJSON, err := os.ReadFile(confPath) //nolint
			if err != nil {
				log.WithError(err).Fatal("Failed to read config file")
			}
			err = json.Unmarshal(confJSON, &conf)
			if err != nil {
				log.WithError(err).Fatal("Failed to unmarshal old config json")
			}
		}
		if conf != nil {
			dmsgDisc = conf.Dmsg.Discovery
		}
		survey, err := visorconfig.SystemSurvey(dmsgDisc)
		if err != nil {
			internal.Catch(cmd.Flags(), fmt.Errorf("Failed to generate system survey: %v", err))
		}
		skyaddr, err := os.ReadFile(visorconfig.PackageConfig().LocalPath + "/" + visorconfig.RewardFile) //nolint
		if err == nil {
			survey.SkycoinAddress = string(skyaddr)
		}
		if conf != nil {
			survey.PubKey = conf.PK
			survey.ServicesURLs.DmsgDiscovery = conf.Dmsg.Discovery
			survey.ServicesURLs.TransportDiscovery = conf.Transport.Discovery
			survey.ServicesURLs.AddressResolver = conf.Transport.AddressResolver
			survey.ServicesURLs.RouteFinder = conf.Routing.RouteFinder
			survey.ServicesURLs.RouteSetupNodes = conf.Routing.RouteSetupNodes
			survey.ServicesURLs.TransportSetupPKs = conf.Transport.TransportSetupPKs
			survey.ServicesURLs.UptimeTracker = conf.UptimeTracker.Addr
			survey.ServicesURLs.ServiceDiscovery = conf.Launcher.ServiceDisc
			survey.ServicesURLs.SurveyWhitelist = conf.SurveyWhitelist
			survey.ServicesURLs.StunServers = conf.StunServers
			//survey.DmsgServers = v.dmsgC.ConnectedServersPK()
		}
		if dmsgdisc != "" {
			ipAddr, err = FetchIP(dmsgDisc)
			if err == nil {
				survey.IPAddr = ipAddr.String()
			}
		}

		s, err := json.MarshalIndent(survey, "", "\t")
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("Could not marshal json: %v", err))
		}
		fmt.Printf("%s", s)
	},
}

// FetchIP fetches the ip address by dmsg servers
func FetchIP(dmsgDisc string) (string, error) {
	log := logging.MustGetLogger("ip_skycoin_fetch_dmsg")
	ctx, cancel := cmdutil.SignalContext(context.Background(), nil)
	defer cancel()

	pk, sk := cipher.GenerateKeyPair()

	dmsgC, closeDmsg, err := startDmsg(ctx, log, pk, sk, dmsgDisc)
	if err != nil {
		return "", fmt.Errorf("failed to start dmsg")
	}
	defer closeDmsg()

	ip, err := dmsgC.LookupIP(ctx, nil)
	return ip.String(), err
}

func startDmsg(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey, dmsgDisc string) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), &dmsg.Config{MinSessions: dmsg.DefaultMinSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		log.WithError(err).Debug("Disconnected from dmsg network.")
		fmt.Printf("\n")
	}
	log.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
		Debug("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		log.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}
