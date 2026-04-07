// Package clivisor cmd/skywire-cli/commands/visor/user.go
package clivisor

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"

	"github.com/bitfield/script"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	clirpc "github.com/skycoin/skywire/cmd/skywire-cli/commands/rpc"
)

func init() {
	userCmd.Flags().StringVar(&clirpc.Addr, "rpc", clirpc.DefaultRPCAddr, "RPC server address (env: SKYWIRE_RPC)")
	RootCmd.AddCommand(userCmd)
}

var userCmd = &cobra.Command{
	Use:   "user",
	Short: "Show the user the visor process is running as",
	Run: func(cmd *cobra.Command, _ []string) {
		// First check if visor is running via RPC
		rpcClient, err := clirpc.Client(cmd.Flags())
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor not running: %v", err))
		}
		// Verify visor is reachable
		_, err = rpcClient.Health()
		if err != nil {
			internal.PrintFatalError(cmd.Flags(), fmt.Errorf("visor not responding: %v", err))
		}

		// Find the skywire visor process and its user
		// Try to find PID via process list
		pidStr, err := script.Exec("pgrep -f 'skywire visor'").First(1).String()
		if err != nil || strings.TrimSpace(pidStr) == "" {
			// Fallback: report the current user (CLI is likely running as same user or root)
			u, err := user.Current()
			if err != nil {
				internal.PrintFatalError(cmd.Flags(), fmt.Errorf("cannot determine user: %v", err))
			}
			internal.PrintOutput(cmd.Flags(), u.Username, fmt.Sprintf("%s (uid=%s)\n", u.Username, u.Uid))
			return
		}

		pid := strings.TrimSpace(pidStr)
		// Read /proc/<pid>/status for the UID
		uidLine, err := script.File(fmt.Sprintf("/proc/%s/status", pid)).Match("Uid:").String()
		if err != nil {
			// Non-Linux fallback
			out, _ := script.Exec(fmt.Sprintf("ps -o user= -p %s", pid)).String() //nolint:errcheck
			username := strings.TrimSpace(out)
			if username == "" {
				username = "unknown"
			}
			internal.PrintOutput(cmd.Flags(), username, username+"\n")
			return
		}

		// Parse UID from /proc status (format: "Uid:\t1000\t1000\t1000\t1000")
		fields := strings.Fields(uidLine)
		if len(fields) >= 2 {
			uid, atoiErr := strconv.Atoi(fields[1])
			if atoiErr != nil {
				internal.PrintOutput(cmd.Flags(), fields[1], fmt.Sprintf("uid=%s\n", fields[1]))
				return
			}
			u, err := user.LookupId(strconv.Itoa(uid))
			if err != nil {
				internal.PrintOutput(cmd.Flags(), fields[1], fmt.Sprintf("uid=%s\n", fields[1]))
			} else {
				type userInfo struct {
					Username string `json:"username"`
					UID      string `json:"uid"`
					GID      string `json:"gid"`
					HomeDir  string `json:"home_dir"`
				}
				info := userInfo{Username: u.Username, UID: u.Uid, GID: u.Gid, HomeDir: u.HomeDir}
				internal.PrintOutput(cmd.Flags(), info, fmt.Sprintf("%s (uid=%s)\n", u.Username, u.Uid))
			}
		} else {
			fmt.Fprintln(os.Stderr, "could not parse UID from /proc status")
		}
	},
}
