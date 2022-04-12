package main

import (
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"runtime"
	"syscall"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType = appnet.TypeSkynet
	vpnPort = routing.Port(skyenv.VPNServerPort)
)

var (
	log = logrus.New()
)

var (
	localPKStr string
	localSKStr string
	passcode   string
	secure     bool
)

func init() {
	if runtime.GOOS == "windows" {
		fmt.Println("OS is not supported")
		os.Exit(1)
	}
	thisUser, err := user.Current()
	if err != nil {
		panic(err)
	}
	if (thisUser.Username != "root") && ((skyenv.OS == "linux") || (skyenv.OS == "mac")) {
		fmt.Println("vpn server must be run as root")
		os.Exit(1)
	}

	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&localPKStr, "pk", "p", "", "Local PubKey")
	rootCmd.Flags().StringVarP(&localSKStr, "sk", "s", "", "Local SecKey")
	rootCmd.Flags().StringVarP(&passcode, "passcode", "r", "", "Passcode to authenticate connection")
	rootCmd.Flags().BoolVarP(&secure, "secure", "k", true, "Forbid connections from clients to server local network")
}

var rootCmd = &cobra.Command{
	Use:   "vpn-server",
	Short: "Skywire VPN Server",
	Long: `
	┬  ┬┌─┐┌┐┌   ┌─┐┌─┐┬─┐┬  ┬┌─┐┬─┐
	└┐┌┘├─┘│││───└─┐├┤ ├┬┘└┐┌┘├┤ ├┬┘
	 └┘ ┴  ┘└┘   └─┘└─┘┴└─ └┘ └─┘┴└─`,
	Run: func(_ *cobra.Command, _ []string) {
		localPK := cipher.PubKey{}
		if localPKStr != "" {
			if err := localPK.UnmarshalText([]byte(localPKStr)); err != nil {
				log.WithError(err).Fatalln("Invalid local PK")
			}
		}

		localSK := cipher.SecKey{}
		if localSKStr != "" {
			if err := localSK.UnmarshalText([]byte(localSKStr)); err != nil {
				log.WithError(err).Fatalln("Invalid local SK")
			}
		}

		appClient := app.NewClient(nil)
		defer appClient.Close()

		osSigs := make(chan os.Signal, 2)

		sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
		for _, sig := range sigs {
			signal.Notify(osSigs, sig)
		}

		l, err := appClient.Listen(netType, vpnPort)
		if err != nil {
			log.WithError(err).Errorf("Error listening network %v on port %d", netType, vpnPort)
			return
		}

		log.Infof("Got app listener, bound to %d", vpnPort)

		srvCfg := vpn.ServerConfig{
			Passcode: passcode,
			Secure:   secure,
		}
		srv, err := vpn.NewServer(srvCfg, log)
		if err != nil {
			log.WithError(err).Fatalln("Error creating VPN server")
		}
		defer func() {
			if err := srv.Close(); err != nil {
				log.WithError(err).Errorln("Error closing server")
			}
		}()

		errCh := make(chan error)
		go func() {
			if err := srv.Serve(l); err != nil {
				errCh <- err
			}

			close(errCh)
		}()

		select {
		case <-osSigs:
		case err := <-errCh:
			log.WithError(err).Errorln("Error serving")
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}

func main() {
	Execute()
}
