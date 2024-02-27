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
	dmsgsocks "github.com/skycoin/dmsg/cmd/dmsg-socks5/commands"
	dmsgcurl "github.com/skycoin/dmsg/cmd/dmsgcurl/commands"
	dmsghttp "github.com/skycoin/dmsg/cmd/dmsghttp/commands"
	dmsgptycli "github.com/skycoin/dmsg/cmd/dmsgpty-cli/commands"
	dmsgptyhost "github.com/skycoin/dmsg/cmd/dmsgpty-host/commands"
	dmsgptyui "github.com/skycoin/dmsg/cmd/dmsgpty-ui/commands"
	dmsgweb "github.com/skycoin/dmsg/cmd/dmsgweb/commands"
	sd "github.com/skycoin/skycoin-service-discovery/cmd/service-discovery/commands"
	"github.com/spf13/cobra"

	ar "github.com/skycoin/skywire-services/cmd/address-resolver/commands"
	confbs "github.com/skycoin/skywire-services/cmd/config-bootstrapper/commands"
	dmsgmon "github.com/skycoin/skywire-services/cmd/dmsg-monitor/commands"
	kg "github.com/skycoin/skywire-services/cmd/keys-gen/commands"
	lc "github.com/skycoin/skywire-services/cmd/liveness-checker/commands"
	nwmon "github.com/skycoin/skywire-services/cmd/network-monitor/commands"
	nv "github.com/skycoin/skywire-services/cmd/node-visualizer/commands"
	pvmon "github.com/skycoin/skywire-services/cmd/public-visor-monitor/commands"
	rf "github.com/skycoin/skywire-services/cmd/route-finder/commands"
	ssmon "github.com/skycoin/skywire-services/cmd/skysocks-monitor/commands"
	se "github.com/skycoin/skywire-services/cmd/sw-env/commands"
	tpd "github.com/skycoin/skywire-services/cmd/transport-discovery/commands"
	tps "github.com/skycoin/skywire-services/cmd/transport-setup/commands"
	vpnmon "github.com/skycoin/skywire-services/cmd/vpn-monitor/commands"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	sc "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	ssc "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	ss "github.com/skycoin/skywire/cmd/apps/skysocks/commands"
	vpnc "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
	vpns "github.com/skycoin/skywire/cmd/apps/vpn-server/commands"
	sn "github.com/skycoin/skywire/cmd/setup-node/commands"
	scli "github.com/skycoin/skywire/cmd/skywire-cli/commands"
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
		dmsgweb.RootCmd,
		dmsgsocks.RootCmd,
		dmsgmon.RootCmd,
	)
	svcCmd.AddCommand(
		sn.RootCmd,
		tpd.RootCmd,
		tps.RootCmd,
		ar.RootCmd,
		rf.RootCmd,
		confbs.RootCmd,
		kg.RootCmd,
		lc.RootCmd,
		nv.RootCmd,
		se.RootCmd,
		sd.RootCmd,
		nwmon.RootCmd,
		pvmon.RootCmd,
		ssmon.RootCmd,
		vpnmon.RootCmd,
	)
	appsCmd.AddCommand(
		vpns.RootCmd,
		vpnc.RootCmd,
		ssc.RootCmd,
		ss.RootCmd,
		sc.RootCmd,
	)
	rootCmd.AddCommand(
		visor.RootCmd,
		scli.RootCmd,
		svcCmd,
		dmsgCmd,
		appsCmd,
	)
	visor.RootCmd.Long = `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─`
	dmsgcurl.RootCmd.Use = "curl"
	dmsgweb.RootCmd.Use = "web"
	sn.RootCmd.Use = "sn"
	ssmon.RootCmd.Use = "ssm"
	vpnmon.RootCmd.Use = "vpnm"
	var helpflag bool
	rootCmd.SetUsageTemplate(help)
	rootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+rootCmd.Use)
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	rootCmd.PersistentFlags().MarkHidden("help") //nolint
	rootCmd.CompletionOptions.DisableDefaultCmd = true
	rootCmd.SetUsageTemplate(help)

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
var appsCmd = &cobra.Command{
	Use:   "app",
	Short: "skywire native applications",
	Long: `
	┌─┐┌─┐┌─┐┌─┐
	├─┤├─┘├─┘└─┐
	┴ ┴┴  ┴  └─┘`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
}

var commands = []*cobra.Command{
	dmsgptycli.RootCmd,
	dmsgptyhost.RootCmd,
	dmsgptyui.RootCmd,
	dmsgptyCmd,
	dmsgdisc.RootCmd,
	dmsgserver.RootCmd,
	dmsghttp.RootCmd,
	dmsgcurl.RootCmd,
	dmsgweb.RootCmd,
	dmsgCmd,
	tpd.RootCmd,
	tps.RootCmd,
	ar.RootCmd,
	rf.RootCmd,
	confbs.RootCmd,
	kg.RootCmd,
	lc.RootCmd,
	nv.RootCmd,
	pvmon.RootCmd,
	se.RootCmd,
	sd.RootCmd,
	svcCmd,
	sn.RootCmd,
	visor.RootCmd,
	scli.RootCmd,
	vpns.RootCmd,
	vpnc.RootCmd,
	ssc.RootCmd,
	ss.RootCmd,
	sc.RootCmd,
	appsCmd,
	rootCmd,
}

func main() {
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
