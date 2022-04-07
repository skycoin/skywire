package update

import (
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"
	coinCipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

var pkg bool

func init() {
	RootCmd.AddCommand(hyperVisorUpdateCmd)
	hyperVisorUpdateCmd.Flags().SortFlags = false
	hyperVisorUpdateCmd.Flags().StringVarP(&addHypervisorPKs, "add-pks", "+", "", "public keys of hypervisors that should be added to this visor")
	hyperVisorUpdateCmd.Flags().BoolVarP(&resetHypervisor, "reset", "r", false, "resets hypervisor configuration")

	RootCmd.AddCommand(skySocksClientUpdateCmd)
	skySocksClientUpdateCmd.Flags().SortFlags = false
	skySocksClientUpdateCmd.Flags().StringVarP(&addSkysocksClientSrv, "add-server", "+", "", "add skysocks server address to skysock-client")
	skySocksClientUpdateCmd.Flags().BoolVarP(&resetSkysocksClient, "reset", "r", false, "reset skysocks-client configuration")

	RootCmd.AddCommand(skySocksServerUpdateCmd)
	skySocksServerUpdateCmd.Flags().SortFlags = false
	skySocksServerUpdateCmd.Flags().StringVarP(&skysocksPasscode, "passwd", "s", "", "add passcode to skysocks server")
	skySocksServerUpdateCmd.Flags().BoolVarP(&resetSkysocks, "reset", "r", false, "reset skysocks configuration")

	RootCmd.AddCommand(vpnClientUpdateCmd)
	vpnClientUpdateCmd.Flags().SortFlags = false
	vpnClientUpdateCmd.Flags().StringVarP(&setVPNClientKillswitch, "killsw", "x", "", "change killswitch status of vpn-client")
	vpnClientUpdateCmd.Flags().StringVar(&addVPNClientSrv, "add-server", "", "add server address to vpn-client")
	vpnClientUpdateCmd.Flags().StringVarP(&addVPNClientPasscode, "pass", "s", "", "add passcode of server if needed")
	vpnClientUpdateCmd.Flags().BoolVarP(&resetVPNclient, "reset", "r", false, "reset vpn-client configurations")

	RootCmd.AddCommand(vpnServerUpdateCmd)
	vpnServerUpdateCmd.Flags().SortFlags = false
	vpnServerUpdateCmd.Flags().StringVarP(&addVPNServerPasscode, "passwd", "s", "", "add passcode to vpn-server")
	vpnServerUpdateCmd.Flags().StringVar(&setVPNServerSecure, "secure", "", "change secure mode status of vpn-server")
	vpnServerUpdateCmd.Flags().StringVar(&setVPNServerAutostart, "autostart", "", "change autostart of vpn-server")
	vpnServerUpdateCmd.Flags().BoolVarP(&resetVPNServer, "reset", "r", false, "reset vpn-server configurations")
}

var hyperVisorUpdateCmd = &cobra.Command{
	Use:   "hv",
	Short: "update hypervisor config",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		conf, ok := visorconfig.ReadFile(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}
		if addHypervisorPKs != "" {
			keys := strings.Split(addHypervisorPKs, ",")
			for _, key := range keys {
				keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(key))
				if err != nil {
					logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", key)
				}
				conf.Hypervisors = append(conf.Hypervisors, cipher.PubKey(keyParsed))
			}
		}
		if resetHypervisor {
			conf.Hypervisors = []cipher.PubKey{}
		}
		saveConfig(conf)
	},
}

var skySocksClientUpdateCmd = &cobra.Command{
	Use:   "sc",
	Short: "update skysocks-client config",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		conf, ok := visorconfig.ReadFile(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}
		if addSkysocksClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addSkysocksClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addSkysocksClientSrv)
			}
			changeAppsConfig(conf, "skysocks-client", "--srv", keyParsed.Hex())
		}
		if resetSkysocksClient {
			resetAppsConfig(conf, "skysocks-client")
		}
		saveConfig(conf)
	},
}

var skySocksServerUpdateCmd = &cobra.Command{
	Use:   "ss",
	Short: "update skysocks-server config",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)
		conf, ok := visorconfig.ReadFile(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}
		if skysocksPasscode != "" {
			changeAppsConfig(conf, "skysocks", "--passcode", skysocksPasscode)
		}
		if resetSkysocks {
			resetAppsConfig(conf, "skysocks")
		}
		saveConfig(conf)
	},
}

var vpnClientUpdateCmd = &cobra.Command{
	Use:   "vpnc",
	Short: "update vpn-client config",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		conf, ok := visorconfig.ReadFile(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}
		switch setVPNClientKillswitch {
		case "true":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		case "false":
			changeAppsConfig(conf, "vpn-client", "--killswitch", setVPNClientKillswitch)
		case "":
			break
		default:
			logger.Fatal("Unrecognized killswitch value: ", setVPNClientKillswitch)
		}
		if addVPNClientSrv != "" {
			keyParsed, err := coinCipher.PubKeyFromHex(strings.TrimSpace(addVPNClientSrv))
			if err != nil {
				logger.WithError(err).Fatalf("Failed to parse hypervisor private key: %s.", addVPNClientSrv)
			}
			changeAppsConfig(conf, "vpn-client", "--srv", keyParsed.Hex())
		}

		if addVPNClientPasscode != "" {
			changeAppsConfig(conf, "vpn-client", "--passcode", addVPNClientPasscode)
		}
		if resetVPNclient {
			resetAppsConfig(conf, "vpn-client")
		}
		saveConfig(conf)
	},
}

var vpnServerUpdateCmd = &cobra.Command{
	Use:   "vpns",
	Short: "update vpn-server config",
	PreRun: func(_ *cobra.Command, _ []string) {
		var err error
		if output, err = filepath.Abs(output); err != nil {
			logger.WithError(err).Fatal("Invalid config output.")
		}
	},
	Run: func(_ *cobra.Command, _ []string) {
		mLog := logging.NewMasterLogger()
		mLog.SetLevel(logrus.InfoLevel)

		conf, ok := visorconfig.ReadFile(input)
		if ok != nil {
			mLog.WithError(ok).Fatal("Failed to parse config.")
		}
		if addVPNServerPasscode != "" {
			changeAppsConfig(conf, "vpn-server", "--passcode", addVPNServerPasscode)
		}
		switch setVPNServerSecure {
		case "true":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		case "false":
			changeAppsConfig(conf, "vpn-server", "--secure", setVPNServerSecure)
		case "":
			break
		default:
			logger.Fatal("Unrecognized vpn server secure value: ", setVPNServerSecure)
		}
		switch setVPNServerAutostart {
		case "true":
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = true
				}
			}
		case "false":
			for i, app := range conf.Launcher.Apps {
				if app.Name == "vpn-server" {
					conf.Launcher.Apps[i].AutoStart = false
				}
			}
		case "":
			break
		default:
			logger.Fatal("Unrecognized vpn server autostart value: ", setVPNServerSecure)
		}
		if resetVPNServer {
			resetAppsConfig(conf, "vpn-server")
		}
		saveConfig(conf)
	},
}
