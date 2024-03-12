// Package clichat contains code for chat command of cli inputports
package clichat

import (
	"github.com/spf13/cobra"

	clichatsend "github.com/skycoin/skywire/cmd/apps/skychat/cli/chat/send"
)

// RootCmd is the sub-command so interact with the chats
var RootCmd = &cobra.Command{
	Use:   "chat",
	Short: "Send messages or query a chat",
}

func init() {
	RootCmd.AddCommand(clichatsend.SendCmd)
}
