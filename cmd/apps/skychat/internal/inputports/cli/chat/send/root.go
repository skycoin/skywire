package clichatsend

import "github.com/spf13/cobra"

// SendCmd is the sub-command to send messages
var SendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send messages",
}

func init() {
	SendCmd.AddCommand(textMessageCmd)
}
