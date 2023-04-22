// Package cli contains code for the cli service of  inputports
package cli

import (
	"fmt"
	"log"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/cmd/apps/skychat/internal/app"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports"
	clichat "github.com/skycoin/skywire/cmd/apps/skychat/internal/inputports/cli/chat"
	"github.com/skycoin/skywire/cmd/apps/skychat/internal/interfaceadapters"
)

var httpport string
var rpcport string

var rootCmd = &cobra.Command{
	Use:   "skychat",
	Short: "Command Line Interface for skychat",
	Long: `
	┌─┐┬┌─┬ ┬         ┌─┐┬  ┬
	└─┐├┴┐└┬┘ CHAT  ──│  │  │
	└─┘┴ ┴ ┴          └─┘┴─┘┴`,
	SilenceErrors:      true,
	SilenceUsage:       true,
	DisableSuggestions: true,
	Run: func(cmd *cobra.Command, args []string) {

		//TODO: Setup Databases depending on flags/attributes

		interfaceadapters.InterfaceAdapterServices = interfaceadapters.NewServices()
		defer func() {
			err := interfaceadapters.InterfaceAdapterServices.Close()
			if err != nil {
				fmt.Println(err.Error())
			}
		}()

		app.AppServices = app.NewServices(
			interfaceadapters.InterfaceAdapterServices.ClientRepository,
			interfaceadapters.InterfaceAdapterServices.UserRepository,
			interfaceadapters.InterfaceAdapterServices.VisorRepository,
			interfaceadapters.InterfaceAdapterServices.NotificationService,
			interfaceadapters.InterfaceAdapterServices.MessengerService)

		inputports.InputportsServices = inputports.NewServices(app.AppServices)

		//messengerService listen
		go interfaceadapters.InterfaceAdapterServices.MessengerService.Listen()

		//rpc-server
		rpcport = ":4040"
		go inputports.InputportsServices.RPCServer.ListenAndServe(&rpcport)

		//http-server
		inputports.InputportsServices.HTTPServer.ListenAndServe(&httpport)
	},
}

func init() {
	rootCmd.AddCommand(
		clichat.RootCmd,
	)
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint

	rootCmd.Flags().StringVar(&httpport, "httpport", ":8001", "port to bind")
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
