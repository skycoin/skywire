// Package commands cmd/skywire/commands/root.go
package commands

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/bitfield/script"
	"github.com/pterm/pterm"
	"github.com/pterm/pterm/putils"
	dmsg "github.com/skycoin/dmsg/cmd/dmsg/commands"
	sd "github.com/skycoin/skycoin-service-discovery/cmd/service-discovery/commands"
	"github.com/spf13/cobra"

	services "github.com/skycoin/skywire-services/cmd/skywire-services/commands"
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

	appsCmd.AddCommand(
		vpns.RootCmd,
		vpnc.RootCmd,
		ssc.RootCmd,
		ss.RootCmd,
		sc.RootCmd,
	)
	services.RootCmd.AddCommand(
		sd.RootCmd,
		sn.RootCmd,
	)
	RootCmd.AddCommand(
		visor.RootCmd,
		scli.RootCmd,
		services.RootCmd,
		dmsg.RootCmd,
		appsCmd,
		treeCmd,
		docCmd,
	)
	visor.RootCmd.Long = `
	┌─┐┬┌─┬ ┬┬ ┬┬┬─┐┌─┐  ┬  ┬┬┌─┐┌─┐┬─┐
	└─┐├┴┐└┬┘││││├┬┘├┤───└┐┌┘│└─┐│ │├┬┘
	└─┘┴ ┴ ┴ └┴┘┴┴└─└─┘   └┘ ┴└─┘└─┘┴└─`
	dmsg.RootCmd.Use = "dmsg"
	services.RootCmd.Use = "svc"
	sd.RootCmd.Use = "sd"
	sn.RootCmd.Use = "sn"
	scli.RootCmd.Use = "cli"
	visor.RootCmd.Use = "visor"
	vpns.RootCmd.Use = "vpn-server"
	vpnc.RootCmd.Use = "vpn-client"
	ssc.RootCmd.Use = "skysocks-client"
	ss.RootCmd.Use = "skysocks"
	sc.RootCmd.Use = "skychat"
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
	Run: func(_ *cobra.Command, _ []string) {
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

	UNHIDEFLAGS=1 go run cmd/skywire/skywire.go doc

	UNHIDEFLAGS=1 go run cmd/skywire/skywire.go doc > cmd/skywire/README1.md

	generate toc:

	cat cmd/skywire/README1.md | gh-md-toc`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
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

// Execute executes root CLI command.
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}
