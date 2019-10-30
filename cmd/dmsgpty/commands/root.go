package commands

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/spf13/cobra"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty"
)

var ptyCLI dmsgpty.CLI
var dstAddr string

func init() {
	ptyCLI.SetDefaults()
	dstAddr = fmt.Sprintf("%s:%d", ptyCLI.DstPK, ptyCLI.DstPort)

	rootCmd.PersistentFlags().StringVar(&ptyCLI.Net, "cli-net", ptyCLI.Net, "network to use for dialing to dmsgpty-server")
	rootCmd.PersistentFlags().StringVar(&ptyCLI.Addr, "cli-addr", ptyCLI.Addr, "address to use for dialing to dmsgpty-server")

	rootCmd.Flags().StringVarP(&dstAddr, "addr", "a", dstAddr, "destination address of pty request")
	rootCmd.Flags().StringVarP(&ptyCLI.Cmd, "cmd", "c", ptyCLI.Cmd, "command to execute")
	rootCmd.Flags().StringArrayVar(&ptyCLI.Arg, "arg", ptyCLI.Arg, "argument for command")
}

var rootCmd = &cobra.Command{
	Use:   "dmsgpty",
	Short: "Run commands over dmsg",
	PreRunE: func(*cobra.Command, []string) error {
		return readDstAddr()
	},
	RunE: func(*cobra.Command, []string) error {
		return ptyCLI.RequestPty()
	},
}

func readDstAddr() error {
	parts := strings.Split(dstAddr, ":")
	for i, part := range parts {
		parts[i] = strings.TrimSpace(part)
	}

	switch len(parts) {
	case 0:
		return nil
	case 1:
		var pk cipher.PubKey
		if err := pk.Set(parts[0]); err != nil {
			return err
		}
		ptyCLI.DstPK = pk
		ptyCLI.DstPort = skyenv.DefaultDmsgPtyPort
		return nil
	case 2:
		var pk cipher.PubKey
		if len(parts[0]) > 0 && parts[0] != pk.String() {
			if err := pk.Set(parts[0]); err != nil {
				return err
			}
		}
		var port uint16
		if _, err := fmt.Fscan(strings.NewReader(parts[1]), &port); err != nil {
			return err
		}
		ptyCLI.DstPK = pk
		ptyCLI.DstPort = port
		return nil
	default:
		return errors.New("invalid addr")
	}
}

// Execute executes the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
