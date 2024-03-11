// cmd/skywire-deployment/skywire-deployment.go
/*
skywire deployment
*/
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitfield/script"
	cc "github.com/ivanpirog/coloredcobra"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
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
	RootCmd.AddCommand(
		visor.RootCmd,
		scli.RootCmd,
		svcCmd,
		dmsgCmd,
		appsCmd,
		treeCmd,
		docCmd,
	)
	visor.RootCmd.Long = `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─`
	dmsgcurl.RootCmd.Use = "curl"
	dmsgweb.RootCmd.Use = "web"
	dmsgptycli.RootCmd.Use = "cli"
	dmsgptyhost.RootCmd.Use = "host"
	dmsgptyui.RootCmd.Use = "ui"
	dmsgdisc.RootCmd.Use = "disc"
	dmsgserver.RootCmd.Use = "server"
	dmsghttp.RootCmd.Use = "http"
	dmsgcurl.RootCmd.Use = "curl"
	dmsgweb.RootCmd.Use = "web"
	dmsgsocks.RootCmd.Use = "socks"
	dmsgmon.RootCmd.Use = "mon"
	tpd.RootCmd.Use = "tpd"
	tps.RootCmd.Use = "tps"
	ar.RootCmd.Use = "ar"
	rf.RootCmd.Use = "rf"
	confbs.RootCmd.Use = "cb"
	kg.RootCmd.Use = "kg"
	lc.RootCmd.Use = "lc"
	nv.RootCmd.Use = "nv"
	vpnmon.RootCmd.Use = "vpnm"
	pvmon.RootCmd.Use = "pvm"
	ssmon.RootCmd.Use = "ssm"
	nwmon.RootCmd.Use = "nwmon"
	se.RootCmd.Use = "se"
	sd.RootCmd.Use = "sd"
	sn.RootCmd.Use = "sn"
	scli.RootCmd.Use = "cli"
	visor.RootCmd.Use = "visor"
	vpns.RootCmd.Use = "vpns"
	vpnc.RootCmd.Use = "vpnc"
	ssc.RootCmd.Use = "ssc"
	ss.RootCmd.Use = "ss"
	sc.RootCmd.Use = "sc"

	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for "+RootCmd.Use)
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
	RootCmd.CompletionOptions.DisableDefaultCmd = true
	RootCmd.SetUsageTemplate(help)

}

// RootCmd contains literally every 'command' from four repos here
var RootCmd = &cobra.Command{
	Use: func() string {
		return strings.Split(filepath.Base(strings.ReplaceAll(strings.ReplaceAll(fmt.Sprintf("%v", os.Args), "[", ""), "]", "")), " ")[0]
	}(),
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

var treeCmd = &cobra.Command{
	Use:                   "tree",
	Short:                 "subcommand tree",
	Long:                  `subcommand tree`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		// You can use a LeveledList here, for easy generation.
		leveledList := pterm.LeveledList{}
		leveledList = append(leveledList, pterm.LeveledListItem{Level: 0, Text: RootCmd.Use})
		for _, j := range RootCmd.Commands() {
			use := strings.Split(j.Use, " ")
			leveledList = append(leveledList, pterm.LeveledListItem{Level: 1, Text: use[0]})
			for _, k := range j.Commands() {
				use := strings.Split(k.Use, " ")
				leveledList = append(leveledList, pterm.LeveledListItem{Level: 2, Text: use[0]})
				for _, l := range k.Commands() {
					use := strings.Split(l.Use, " ")
					leveledList = append(leveledList, pterm.LeveledListItem{Level: 3, Text: use[0]})
					for _, m := range l.Commands() {
						use := strings.Split(m.Use, " ")
						leveledList = append(leveledList, pterm.LeveledListItem{Level: 4, Text: use[0]})
						for _, n := range m.Commands() {
							use := strings.Split(n.Use, " ")
							leveledList = append(leveledList, pterm.LeveledListItem{Level: 5, Text: use[0]})
							for _, o := range n.Commands() {
								use := strings.Split(o.Use, " ")
								leveledList = append(leveledList, pterm.LeveledListItem{Level: 6, Text: use[0]})
								for _, p := range o.Commands() {
									use := strings.Split(p.Use, " ")
									leveledList = append(leveledList, pterm.LeveledListItem{Level: 7, Text: use[0]})
								}
							}
						}
					}
				}
			}
		}
		// Generate tree from LeveledList.
		r := putils.TreeFromLeveledList(leveledList)

		// Render TreePrinter
		err := pterm.DefaultTree.WithRoot(r).Render()
		if err != nil {
			log.Fatal("render subcommand tree: ", err)
		}
	},
}

// for toc generation use: https://github.com/ekalinin/github-markdown-toc.go
var docCmd = &cobra.Command{
	Use:   "doc",
	Short: "generate markdown docs",
	Long: `generate markdown docs

	UNHIDEFLAGS=1 go run cmd/skywire-deployment/skywire.go doc

	UNHIDEFLAGS=1 go run cmd/skywire-deployment/skywire.go doc > cmd/skywire-deployment/README1.md

	generate toc:

	cat cmd/skywire-deployment/README1.md | gh-md-toc`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("\n# %s\n", "skywire documentation")
		fmt.Printf("\n## %s\n", "subcommand tree")
		fmt.Printf("\n%s\n", "A tree representation of the skywire subcommands")
		fmt.Printf("\n```\n")
		_, err := script.Exec(os.Args[0] + " tree").Stdout() //nolint
		if err != nil {
			fmt.Println(err.Error())
		}
		fmt.Printf("\n```\n")

		var use string
		for _, j := range RootCmd.Commands() {
			use = strings.Split(j.Use, " ")[0]
			fmt.Printf("\n### %s\n", use)
			fmt.Printf("\n```\n")
			j.Help() //nolint
			fmt.Printf("\n```\n")
			if j.Name() == "cli" {
				fmt.Printf("\n%s\n", "skywire command line interface")
				fmt.Printf("\n## %s\n", RootCmd.Use)
				fmt.Printf("\n```\n")
				RootCmd.Help() //nolint
				fmt.Printf("\n```\n")
				fmt.Printf("\n## %s\n", "global flags")
				fmt.Printf("\n%s\n", "The skywire-cli interacts with the running visor via rpc calls. By default the rpc server is available on localhost:3435. The rpc address and port the visor is using may be changed in the config file, once generated.")

				fmt.Printf("\n%s\n", "It is not recommended to expose the rpc server on the local network. Exposing the rpc allows unsecured access to the machine over the local network")
				fmt.Printf("\n```\n")
				fmt.Printf("\n%s\n", "Global Flags:")
				fmt.Printf("\n%s\n", "			--rpc string   RPC server address (default \"localhost:3435\")")
				fmt.Printf("\n%s\n", "			--json bool   print output as json")
				fmt.Printf("\n```\n")
			}
			for _, k := range j.Commands() {
				use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0]
				fmt.Printf("\n#### %s\n", use)
				fmt.Printf("\n```\n")
				k.Help() //nolint
				fmt.Printf("\n```\n")
				if k.Name() == "survey" {
					fmt.Printf("\n```\n")
					_, err = script.Exec("sudo " + os.Args[0] + ` survey`).Stdout() //nolint
					if err != nil {
						fmt.Println(err.Error())
					}
					fmt.Printf("\n```\n")
				}
				for _, l := range k.Commands() {
					use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0]
					fmt.Printf("\n##### %s\n", use)
					fmt.Printf("\n```\n")
					l.Help() //nolint
					fmt.Printf("\n```\n")
					if l.Name() == "gen" {
						fmt.Printf("\n##### Example for package / msi\n")
						fmt.Printf("\n```\n")
						fmt.Printf("$ skywire cli config gen -bpirxn\n")
						_, err = script.Exec(os.Args[0] + ` cli config gen -bpirxn`).Stdout() //nolint
						if err != nil {
							fmt.Println(err.Error())
						}
						fmt.Printf("\n```\n")
					}
					for _, m := range l.Commands() {
						use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0] + " " + strings.Split(m.Use, " ")[0]
						fmt.Printf("\n###### %s\n", use)
						fmt.Printf("\n```\n")
						m.Help() //nolint
						fmt.Printf("\n```\n")
						for _, n := range m.Commands() {
							use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0] + " " + strings.Split(m.Use, " ")[0] + " " + strings.Split(n.Use, " ")[0]
							fmt.Printf("\n###### %s\n", use)
							fmt.Printf("\n```\n")
							m.Help() //nolint
							fmt.Printf("\n```\n")
							for _, o := range n.Commands() {
								use = strings.Split(j.Use, " ")[0] + " " + strings.Split(k.Use, " ")[0] + " " + strings.Split(l.Use, " ")[0] + " " + strings.Split(m.Use, " ")[0] + " " + strings.Split(n.Use, " ")[0] + " " + strings.Split(o.Use, " ")[0]
								fmt.Printf("\n###### %s\n", use)
								fmt.Printf("\n```\n")
								m.Help() //nolint
								fmt.Printf("\n```\n")
							}
						}
					}
				}
			}
		}
	},
}

func main() {
	cc.Init(&cc.Config{
		RootCmd:         RootCmd,
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
	if err := RootCmd.Execute(); err != nil {
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
