// Package clidmsg cmd/skywire-cli/commands/dmsg/root.go c4-vis-cli
package clidmsg

import (
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/cipher"
)

var (
	rpcAddr string
	sk      cipher.SecKey
	logLvl  string
)

// RootCmd is the command that contains sub-commands which use dmsg.
var RootCmd = &cobra.Command{
	Use:   "dmsg",
	Short: "Dmsg utilities",
	Long: `Dmsg utilities.

These commands fall into three families:

  this-visor dmsg client (needs a running visor on --rpc)
    sessions      list the dmsg servers each visor dmsg client is on
    converge      re-dial sessions onto their preferred carrier
    connect-all   open a session to every known server (one-shot)
    set-sessions  persist dmsg.sessions_count + connect-all
    port-hits     inbound hits to ports with no listener
    diag          runtime diagnostics (porter / reconnect)

  standalone dmsg tools (bootstrap their own dmsg client; work with
  no local visor — pass --sk for a stable identity)
    curl          fetch data over dmsg (HTTP-over-dmsg)
    cat           splice stdio with a peer over dmsg or skynet
    scp           copy a file to/from a remote visor's dmsgscp host
    iperf         bulk throughput / RTT measurement over a stream
    chat          interactive 1:1 chat over dmsg
    probe         test a remote port's reachability
    sub           standalone UDP-over-dmsg bridge
    smb           standalone SMTP-over-dmsg bridge

Query the dmsg discovery itself with 'skywire cli mdisc'. Remote
visor terminals live under 'skywire cli pty' (formerly 'dmsg pty').`,
}

func init() {
	// PtyCmd's subcommands are re-parented onto `cli pty` (see the pty
	// package); they are no longer registered under `cli dmsg`.
	RootCmd.AddCommand(
		curlCmd,
		chatCmd,
		scpCmd,
		catCmd,
		iperfCmd,
	)
}
