// /* cmd/skywire-visor/skywire-visor.go
/*
skywire visor
*/
package main

import (
	"fmt"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire/cmd/address-resolver/commands"
	confbs "github.com/skycoin/skywire/cmd/config-bootstrapper/commands"
	dmsgd "github.com/skycoin/skywire/cmd/dmsg-discovery/commands"
	dmsgm "github.com/skycoin/skywire/cmd/dmsg-monitor/commands"
	dmsgserver "github.com/skycoin/skywire/cmd/dmsg-server/commands"
	dmsgget "github.com/skycoin/skywire/cmd/dmsgget/commands"
	dmsghttp "github.com/skycoin/skywire/cmd/dmsghttp/commands"
	dmsgptycli "github.com/skycoin/skywire/cmd/dmsgpty-cli/commands"
	dmsgptyhost "github.com/skycoin/skywire/cmd/dmsgpty-host/commands"
	dmsgptyui "github.com/skycoin/skywire/cmd/dmsgpty-ui/commands"
	kg "github.com/skycoin/skywire/cmd/keys-gen/commands"
	lc "github.com/skycoin/skywire/cmd/liveness-checker/commands"
	nv "github.com/skycoin/skywire/cmd/node-visualizer/commands"
	pvm "github.com/skycoin/skywire/cmd/public-visor-monitor/commands"
	rf "github.com/skycoin/skywire/cmd/route-finder/commands"
	sd "github.com/skycoin/skywire/cmd/service-discovery/commands"
	setupnode "github.com/skycoin/skywire/cmd/setup-node/commands"
	skywirecli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
	se "github.com/skycoin/skywire/cmd/sw-env/commands"
	tpdm "github.com/skycoin/skywire/cmd/tpd-monitor/commands"
	tpd "github.com/skycoin/skywire/cmd/transport-discovery/commands"
	tps "github.com/skycoin/skywire/cmd/transport-setup/commands"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	dmsgptyCmd.AddCommand(
		dmsgptycli.RootCmd,
		dmsgptyhost.RootCmd,
		dmsgptyui.RootCmd,
	)
	dmsgCmd.AddCommand(
		dmsgptyCmd,
		dmsgd.RootCmd,
		dmsgm.RootCmd,
		dmsgserver.RootCmd,
		dmsgget.RootCmd,
		dmsghttp.RootCmd,
	)
	servicesCmd.AddCommand(
		sd.RootCmd,
		setupnode.RootCmd,
		tpd.RootCmd,
		tps.RootCmd,
		tpdm.RootCmd,
		ar.RootCmd,
		rf.RootCmd,
		confbs.RootCmd,
		kg.RootCmd,
		lc.RootCmd,
		nv.RootCmd,
		pvm.RootCmd,
		se.RootCmd,
	)

	rootCmd.AddCommand(
		visor.RootCmd,
		skywirecli.RootCmd,
		dmsgCmd,
		servicesCmd,
	)
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
	rootCmd.CompletionOptions.DisableDefaultCmd = true

}

var rootCmd = &cobra.Command{
	Use: "skywire",
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

var dmsgCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "Dmsg services & utilities",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐
	 │││││└─┐│ ┬
	─┴┘┴ ┴└─┘└─┘ `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}
var dmsgptyCmd = &cobra.Command{
	Use:   "pty",
	Short: "Dmsg pseudoterminal (pty)",
	Long: `
	┌─┐┌┬┐┬ ┬
	├─┘ │ └┬┘
	┴   ┴  ┴ `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

var servicesCmd = &cobra.Command{
	Use:   "svc",
	Short: "Skywire services & service discovery",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘`,
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
