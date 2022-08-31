// Package clidmsgpty hvdmsg.go
package clidmsgpty

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/cmd/skywire-cli/internal"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(
		dmsgUICmd,
		dmsgURLCmd,
	)
	dmsgUICmd.Flags().StringVarP(&path, "input", "i", "", "read from specified config file")
	dmsgUICmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from "+visorconfig.Pkgpath)
	dmsgUICmd.Flags().StringVarP(&pk, "visor", "v", "", "public key of visor to connect to")
	dmsgURLCmd.Flags().StringVarP(&path, "input", "i", "", "read from specified config file")
	dmsgURLCmd.Flags().BoolVarP(&pkg, "pkg", "p", false, "read from "+visorconfig.Pkgpath)
	dmsgURLCmd.Flags().StringVarP(&pk, "visor", "v", "", "public key of visor to connect to")
}

var dmsgUICmd = &cobra.Command{
	Use:   "ui",
	Short: "Open dmsgpty UI in default browser",
	Run: func(_ *cobra.Command, _ []string) {
		if pk == "" {
			if pkg {
				path = visorconfig.Pkgpath
			}
			if path != "" {
				conf, err := visorconfig.ReadFile(path)
				if err != nil {
					log.Fatal("Failed to read in config file:", err)
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := rpcClient()
				overview, err := client.Overview()
				if err != nil {
					log.Fatal("Failed to connect; is skywire running?\n", err)
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", pk)
		}
		if err := webbrowser.Open(url); err != nil {
			log.Fatal("Failed to open dmsgpty UI in browser:", err)
		}
	},
}

var dmsgURLCmd = &cobra.Command{
	Use:   "url",
	Short: "Show dmsgpty UI URL",
	Run: func(cmd *cobra.Command, _ []string) {
		if pk == "" {
			if pkg {
				path = visorconfig.Pkgpath
			}
			if path != "" {
				conf, err := visorconfig.ReadFile(path)
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to read in config file: %v", err))
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := rpcClient()
				overview, err := client.Overview()
				if err != nil {
					internal.Catch(cmd.Flags(), fmt.Errorf("Failed to connect; is skywire running?: %v", err))
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", pk)
		}

		output := struct {
			URL string `json:"url"`
		}{
			URL: url,
		}

		internal.PrintOutput(cmd.Flags(), output, fmt.Sprintln(url))
	},
}
