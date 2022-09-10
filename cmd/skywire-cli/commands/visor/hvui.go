package clivisor

import (
	"github.com/spf13/cobra"
	"github.com/toqueteos/webbrowser"
)

func init() {
	RootCmd.AddCommand(hvuiCmd)
}

var hvuiCmd = &cobra.Command{
	Use:   "hvui",
	Short: "Hypervisor UI",
	Run: func(_ *cobra.Command, _ []string) {
		//TODO: get the actual port from config instead of using default value here
		if err := webbrowser.Open("http://127.0.0.1:8000/"); err != nil {
			logger.Fatal("Failed to open hypervisor UI in browser:", err)
		}
	},
}
