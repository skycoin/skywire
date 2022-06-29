package hv

import (
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"
)

func init() {
	RootCmd.AddCommand(hvuiCmd)
}
// RootCmd contains commands that interact with the skywire-visor
var hvuiCmd = &cobra.Command{
	Use:   "ui",
	Short: "hypervisor UI",
	Run: func(_ *cobra.Command, _ []string) {
		//TODO: get the actual port from config instead of using default value here
		if err := webbrowser.Open("http://127.0.0.1:8000/"); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}
