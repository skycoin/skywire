package hv

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

func init() {
	RootCmd.AddCommand(dmsgCmd)
	dmsgCmd.PersistentFlags().StringVarP(&rpcAddr, "rpc", "", "localhost:3435", "RPC server address")
	dmsgCmd.AddCommand(
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

var dmsgCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "dmsgpty UI",
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
					log.Fatal("Failed:", err)
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := rpcClient()
				overview, err := client.Overview()
				if err != nil {
					log.Fatal("Failed to connect:", err)
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
	Run: func(_ *cobra.Command, _ []string) {
		if pk == "" {
			if pkg {
				path = visorconfig.Pkgpath
			}
			if path != "" {
				conf, err := visorconfig.ReadFile(path)
				if err != nil {
					log.Fatal("Failed:", err)
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", conf.PK.Hex())
			} else {
				client := rpcClient()
				overview, err := client.Overview()
				if err != nil {
					logger.Fatal("Failed to connect:", err)
				}
				url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", overview.PubKey.Hex())
			}
		} else {
			url = fmt.Sprintf("http://127.0.0.1:8000/pty/%s", pk)
		}
		fmt.Println(url)
	},
}
