// cmd/skywire-deployment/skywire-deployment.go
/*
skywire deployment
*/
package main

import (
	"fmt"

	cc "github.com/ivanpirog/coloredcobra"
	dmsgdisc "github.com/skycoin/dmsg/cmd/dmsg-discovery/commands"
	dmsgserver "github.com/skycoin/dmsg/cmd/dmsg-server/commands"
	dmsgcurl "github.com/skycoin/dmsg/cmd/dmsgcurl/commands"
	dmsghttp "github.com/skycoin/dmsg/cmd/dmsghttp/commands"
	dmsgptycli "github.com/skycoin/dmsg/cmd/dmsgpty-cli/commands"
	dmsgptyhost "github.com/skycoin/dmsg/cmd/dmsgpty-host/commands"
	dmsgptyui "github.com/skycoin/dmsg/cmd/dmsgpty-ui/commands"
	sd "github.com/skycoin/skycoin-service-discovery/cmd/service-discovery/commands"
	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire-services/cmd/address-resolver/commands"
	confbs "github.com/skycoin/skywire-services/cmd/config-bootstrapper/commands"
	dmsgm "github.com/skycoin/skywire-services/cmd/dmsg-monitor/commands"
	kg "github.com/skycoin/skywire-services/cmd/keys-gen/commands"
	lc "github.com/skycoin/skywire-services/cmd/liveness-checker/commands"
	nv "github.com/skycoin/skywire-services/cmd/node-visualizer/commands"
	pvm "github.com/skycoin/skywire-services/cmd/public-visor-monitor/commands"
	rf "github.com/skycoin/skywire-services/cmd/route-finder/commands"
	se "github.com/skycoin/skywire-services/cmd/sw-env/commands"
	tpdm "github.com/skycoin/skywire-services/cmd/tpd-monitor/commands"
	tpd "github.com/skycoin/skywire-services/cmd/transport-discovery/commands"
	tps "github.com/skycoin/skywire-services/cmd/transport-setup/commands"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	setupnode "github.com/skycoin/skywire/cmd/setup-node/commands"
	skywirecli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
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
		dmsgdisc.RootCmd,
		dmsgserver.RootCmd,
		dmsghttp.RootCmd,
		dmsgcurl.RootCmd,
	)
	svcCmd.AddCommand(
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
		dmsgm.RootCmd,
		sd.RootCmd,
	)
	visor.RootCmd.Long= `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─`
	rootCmd.AddCommand(
		visor.RootCmd,
		skywirecli.RootCmd,
		setupnode.RootCmd,
		svcCmd,
		dmsgCmd,
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

// RootCmd contains all subcommands
var svcCmd = &cobra.Command{
	Use:   "svc",
	Short: "Skywire services",
	Long: `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┌─┐┌─┐┬─┐┬  ┬┬┌─┐┌─┐┌─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└─┐├┤ ├┬┘└┐┌┘││  ├┤ └─┐
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘  └─┘└─┘┴└─ └┘ ┴└─┘└─┘└─┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// RootCmd contains all binaries which may be separately compiled as subcommands
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

func main() {
commands := []*cobra.Command{
	dmsgptycli.RootCmd,
	dmsgptyhost.RootCmd,
	dmsgptyui.RootCmd,
	dmsgptyCmd,
	dmsgdisc.RootCmd,
	dmsgserver.RootCmd,
	dmsghttp.RootCmd,
	dmsgcurl.RootCmd,
	dmsgCmd,
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
	dmsgm.RootCmd,
	sd.RootCmd,
	svcCmd,
	setupnode.RootCmd,
	visor.RootCmd,
	skywirecli.RootCmd,
	rootCmd,
	}
for _, cmd := range commands {
  cc.Init(&cc.Config{
      RootCmd:         cmd,
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
}

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
