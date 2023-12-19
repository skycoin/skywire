// /* cmd/skywire-visor/skywire-visor.go
/*
skywire visor
*/
package main

import (
	"fmt"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	skychat "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	skysocksclient "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	skysocks "github.com/skycoin/skywire/cmd/apps/skysocks/commands"
	vpnclient "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	vpnserver "github.com/skycoin/skywire/cmd/apps/vpn-server/commands"
	setupnode "github.com/skycoin/skywire/cmd/setup-node/commands"
	skywirecli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	rootCmd.AddCommand(
		visor.RootCmd,
		skywirecli.RootCmd,
		setupnode.RootCmd,
		appsCmd,
	)
	appsCmd.AddCommand(
		vpnserver.RootCmd,
		vpnclient.RootCmd,
		skysocksclient.RootCmd,
		skysocks.RootCmd,
		skychat.RootCmd,
	)
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
	rootCmd.CompletionOptions.DisableDefaultCmd = true

}

var rootCmd = &cobra.Command{
	Use:   "skywire",
	Short: "building a new internet",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

var appsCmd = &cobra.Command{
	Use:   "app",
	Short: "skywire native applications",
	Long: `
	┌─┐┌─┐┌─┐┬  ┬┌─┐┌─┐┌┬┐┬┌─┐┌┐┌┌─┐
	├─┤├─┘├─┘│  ││  ├─┤ │ ││ ││││└─┐
	┴ ┴┴  ┴  ┴─┘┴└─┘┴ ┴ ┴ ┴└─┘┘└┘└─┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

func main() {
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

const help = "{{if gt (len .Aliases) 0}}" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
