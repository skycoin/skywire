// Package commands cmd/dmsg-commands/self-ping/commands/self-ping.go
package commands

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/dmsg/direct"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// selfPingPort is the dmsg port used for the loopback stream.
const selfPingPort = uint16(1)

var serverAddr string

func init() {
	RootCmd.Flags().StringVar(&serverAddr, "server", "", "dmsg server to connect through: `pk@ip:port`")
	RootCmd.MarkFlagRequired("server") //nolint:errcheck,gosec
}

// RootCmd is the self-ping command.
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG self-ping: dial own PK through a specific server",
	Long: `Creates a temporary dmsg client, connects to the specified dmsg server,
then dials its own public key through that server.

If the noise handshake completes successfully the round-trip latency is printed
and the command exits 0. If anything fails, the error is printed and the
command exits 1.

The --server flag is required and must be in the format pk@ip:port, e.g.:

  skywire dmsg self-ping --server 02a2d4c3...@139.162.173.101:30082`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	RunE: func(_ *cobra.Command, _ []string) error {
		log := logging.MustGetLogger("dmsg-self-ping")

		// Parse the --server flag (pk@ip:port).
		srvEntry, err := parseServerEntry(serverAddr)
		if err != nil {
			return fmt.Errorf("invalid --server value: %w", err)
		}

		// Generate a temporary keypair for this diagnostic run.
		pk, sk := cipher.GenerateKeyPair()
		log.WithField("tmp_pk", pk.String()).Debug("Generated temporary keypair")

		// Build a direct disc client with entries for both the server and our
		// temporary client PK so that DialStream can resolve the destination.
		entries := direct.GetAllEntries(cipher.PubKeys{pk}, []*disc.Entry{srvEntry})
		dClient := direct.NewClient(entries, log)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cfg := &dmsg.Config{MinSessions: 1}
		dmsgC, stop, err := direct.StartDmsg(ctx, log, pk, sk, dClient, cfg)
		if err != nil {
			return fmt.Errorf("failed to connect to dmsg server %s: %w", srvEntry.Server.Address, err)
		}
		defer stop()

		// Open a listener on the test port so the inbound half of the
		// self-dial has somewhere to land.
		lis, err := dmsgC.Listen(selfPingPort)
		if err != nil {
			return fmt.Errorf("failed to listen on dmsg port %d: %w", selfPingPort, err)
		}
		defer lis.Close() //nolint:errcheck

		// Accept the inbound stream in a goroutine.
		acceptErrCh := make(chan error, 1)
		go func() {
			stream, aerr := lis.AcceptStream()
			if aerr != nil {
				acceptErrCh <- aerr
				return
			}
			stream.Close() //nolint:errcheck,gosec
			acceptErrCh <- nil
		}()

		// Dial own PK through the server and measure time-to-handshake.
		start := time.Now()
		dialStream, err := dmsgC.DialStream(ctx, dmsg.Addr{PK: pk, Port: selfPingPort})
		if err != nil {
			return fmt.Errorf("self-dial failed via server %s: %w", srvEntry.Server.Address, err)
		}
		latency := time.Since(start)
		dialStream.Close() //nolint:errcheck,gosec

		// Wait for the accept side to finish.
		if aerr := <-acceptErrCh; aerr != nil {
			return fmt.Errorf("accept side of self-dial failed: %w", aerr)
		}

		fmt.Printf("DMSG self-ping OK\n")
		fmt.Printf("  server:  %s @ %s\n", srvEntry.Static.String(), srvEntry.Server.Address)
		fmt.Printf("  own pk:  %s\n", pk.String())
		fmt.Printf("  latency: %v\n", latency.Round(time.Millisecond))
		return nil
	},
}

// parseServerEntry parses a "pk@ip:port" string into a disc.Entry.
func parseServerEntry(s string) (*disc.Entry, error) {
	atIdx := strings.IndexByte(s, '@')
	if atIdx < 1 || atIdx >= len(s)-1 {
		return nil, fmt.Errorf("expected format pk@ip:port, got %q", s)
	}
	pkStr := s[:atIdx]
	addr := s[atIdx+1:]

	var pk cipher.PubKey
	if err := pk.Set(pkStr); err != nil {
		return nil, fmt.Errorf("invalid server public key %q: %w", pkStr, err)
	}
	if addr == "" {
		return nil, fmt.Errorf("server address is empty")
	}

	return &disc.Entry{
		Version: "0.0.1",
		Static:  pk,
		Server:  &disc.Server{Address: addr, AvailableSessions: 2048},
	}, nil
}
