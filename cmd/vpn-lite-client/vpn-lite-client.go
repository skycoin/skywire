// Package main cmd/vpn-lite-client/vpn-lite-client.go
package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/internal/vpn"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	serverPKStr string
)

func init() {
	rootCmd.Flags().StringVarP(&serverPKStr, "srv", "k", "", "PubKey of the server to connect to\033[0m")
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
}

var rootCmd = &cobra.Command{
	Use:   "vpn-lite-client",
	Short: "Vpn lite client",
	Long: `
	┬  ┬┌─┐┌┐┌   ┬  ┬┌┬┐┌─┐  ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
	└┐┌┘├─┘│││───│  │ │ ├┤───│  │  │├┤ │││ │
	 └┘ ┴  ┘└┘   ┴─┘┴ ┴ └─┘  └─┘┴─┘┴└─┘┘└┘ ┴ `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {

		eventSub := appevent.NewSubscriber()

		appCl := app.NewClient(eventSub)
		defer appCl.Close()

		if serverPKStr == "" {
			err := errors.New("VPN server pub key is missing")
			print(fmt.Sprintf("%v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}

		serverPK := cipher.PubKey{}
		if err := serverPK.UnmarshalText([]byte(serverPKStr)); err != nil {
			print(fmt.Sprintf("Invalid local SK: %v\n", err))
			setAppErr(appCl, err)
			os.Exit(1)
		}

		fmt.Printf("Connecting to VPN server %s\n", serverPK.String())

		vpnLiteClientCfg := vpn.ClientConfig{
			ServerPK: serverPK,
		}
		vpnLiteClient, err := vpn.NewLiteClient(vpnLiteClientCfg, appCl)
		if err != nil {
			print(fmt.Sprintf("Error creating VPN lite client: %v\n", err))
			setAppErr(appCl, err)
		}

		osSigs := make(chan os.Signal, 2)
		sigs := []os.Signal{syscall.SIGTERM, syscall.SIGINT}
		for _, sig := range sigs {
			signal.Notify(osSigs, sig)
		}

		go func() {
			<-osSigs
			vpnLiteClient.Close()
		}()

		defer setAppStatus(appCl, appserver.AppDetailedStatusStopped)

		if err := vpnLiteClient.Serve(); err != nil {
			print(fmt.Sprintf("Failed to serve VPN lite client: %v\n", err))
		}

	},
}

func setAppErr(appCl *app.Client, err error) {
	if appErr := appCl.SetError(err.Error()); appErr != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", err, appErr))
	}
}

func setAppStatus(appCl *app.Client, status appserver.AppDetailedStatus) {
	if err := appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func main() {
	Execute()
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       rootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := rootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
