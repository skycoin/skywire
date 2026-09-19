// Package commands cmd/dmsg/dmsg-socks5/commands/dmsg-socks5.go c1-net-dmsg
package commands

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/0magnet/calvin"
	socks5 "github.com/armon/go-socks5"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	dmsgcmdutil "github.com/skycoin/skywire/pkg/dmsg/cmdutil"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgclient"
	"github.com/skycoin/skywire/pkg/logging"
)

var (
	sk        cipher.SecKey
	pubk      string
	wl        string
	wlkeys    []cipher.PubKey
	proxyPort int
	dmsgPort  uint16
	dlog      *logging.Logger
	pprofMode string
	pprofAddr string
)

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
func init() {
	RootCmd.AddCommand(
		serveCmd,
		proxyCmd,
	)

	dmsgclient.InitFlags(serveCmd)
	serveCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to serve socks5")
	serveCmd.Flags().StringVarP(&wl, "wl", "w", "", "whitelist keys, comma separated")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	serveCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified")
	serveCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	serveCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")

	dmsgclient.InitFlags(proxyCmd)
	proxyCmd.Flags().IntVarP(&proxyPort, "port", "p", 1081, "TCP port to serve SOCKS5 proxy locally")
	proxyCmd.Flags().Uint16VarP(&dmsgPort, "dport", "q", 1081, "dmsg port to connect to socks5 server")
	proxyCmd.Flags().StringVarP(&pubk, "pk", "k", "", "dmsg socks5 proxy server public key to connect to")
	if os.Getenv("DMSGSK") != "" {
		sk.Set(os.Getenv("DMSGSK")) //nolint
	}
	proxyCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified")
	proxyCmd.Flags().StringVar(&pprofMode, "pprofmode", "", "[ cpu | mem | mutex | block | trace | http ]")
	proxyCmd.Flags().StringVar(&pprofAddr, "pprofaddr", "localhost:6060", "pprof http port")

}

// RootCmd contains the root command
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG socks5 proxy server & client",
	Long: calvin.AsciiFont("dmsg-socks") + `
	DMSG socks5 proxy server & client`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
}

// serveCmd serves socks5 over dmsg
var serveCmd = &cobra.Command{
	Use:                   "server",
	Short:                 "dmsg socks5 proxy server",
	Long:                  "dmsg socks5 proxy server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		dlog = logging.MustGetLogger("dmsg-proxy")
		stopPProf := dmsgcmdutil.InitPProf(dlog, pprofMode, pprofAddr)
		defer stopPProf()

		if err := dmsgclient.InitConfig(); err != nil {
			dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
		}

		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		if wl != "" {
			wlk := strings.Split(wl, ",")
			for _, key := range wlk {
				var pk1 cipher.PubKey
				err := pk1.Set(key)
				if err == nil {
					wlkeys = append(wlkeys, pk1)
				}
			}
		}
		if len(wlkeys) > 0 {
			if len(wlkeys) == 1 {
				dlog.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
			} else {
				dlog.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
			}
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		dmsgC, closeDmsg, err := dmsgclient.InitDmsgWithFlags(ctx, dlog, pk, sk, nil, pk.String())

		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg network")
			return
		}

		defer closeDmsg()

		dlog.Infof("dmsg client pk: " + pk.String())
		time.Sleep(time.Second)
		dmsgL, err := dmsgC.Listen(dmsgPort)
		if err != nil {
			dlog.Fatalf("Error listening on port %d: %v", dmsgPort, err)
		}
		defer func() {
			if err := dmsgL.Close(); err != nil {
				dlog.Printf("Error closing listener: %v", err)
			}
		}()

		go func() {
			<-ctx.Done()
			if err := dmsgL.Close(); err != nil {
				dlog.WithError(err).Debug("Error closing listener on context cancellation")
			}
		}()

		if err := serveSocks5OverDmsg(ctx, dlog, dmsgL, wlkeys); err != nil {
			dlog.WithError(err).Error("SOCKS5 server stopped")
		}
	},
}

// serveSocks5OverDmsg answers a full SOCKS5 session on every accepted dmsg
// stream: the greeting and the CONNECT request ride the stream, and the
// outbound dial to the requested target is made from THIS end. Streams whose
// remote public key is not whitelisted (when a whitelist is configured) are
// closed before any SOCKS5 byte is read.
func serveSocks5OverDmsg(ctx context.Context, log *logging.Logger, dmsgL net.Listener, wlkeys []cipher.PubKey) error {
	server, err := socks5.New(&socks5.Config{})
	if err != nil {
		return fmt.Errorf("creating SOCKS5 server: %w", err)
	}
	for {
		respConn, err := dmsgL.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				log.Info("Shutting down SOCKS5 server...")
				return nil
			default:
				log.Errorf("Error accepting initiator: %v", err)
				continue
			}
		}
		log.Infof("Accepted connection from: %s", respConn.RemoteAddr())

		// Enforce whitelist: extract remote PK from the dmsg address.
		if len(wlkeys) > 0 {
			remotePK, _, splitErr := net.SplitHostPort(respConn.RemoteAddr().String())
			if splitErr != nil {
				log.WithError(splitErr).Warn("Failed to parse remote address, rejecting connection.")
				respConn.Close() //nolint:errcheck,gosec
				continue
			}
			allowed := false
			for _, key := range wlkeys {
				if remotePK == key.String() {
					allowed = true
					break
				}
			}
			if !allowed {
				log.WithField("remote_pk", remotePK).Warn("Connection rejected: not in whitelist.")
				respConn.Close() //nolint:errcheck,gosec
				continue
			}
		}

		go func() {
			defer func() {
				if closeErr := respConn.Close(); closeErr != nil {
					log.Debugf("Error closing client connection: %v", closeErr)
				}
			}()
			if err := server.ServeConn(respConn); err != nil {
				log.Infof("Connection closed: %s", respConn.RemoteAddr())
				log.Errorf("Error serving SOCKS5 proxy: %v", err)
			}
		}()
	}
}

// proxyCmd serves the local socks5 proxy
var proxyCmd = &cobra.Command{
	Use:                   "client",
	Short:                 "socks5 proxy client for dmsg socks5 proxy server",
	Long:                  "socks5 proxy client for dmsg socks5 proxy server",
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Run: func(_ *cobra.Command, _ []string) {
		dlog = logging.MustGetLogger("dmsg-proxy-client")
		stopPProf := dmsgcmdutil.InitPProf(dlog, pprofMode, pprofAddr)
		defer stopPProf()

		if err := dmsgclient.InitConfig(); err != nil {
			dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
		}

		var pubKey cipher.PubKey
		err := pubKey.Set(pubk)
		if err != nil {
			dlog.Fatal("Public key to connect to cannot be empty")
		}
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		// The proxy half only DIALS the socks5 server; nothing dials it back, so
		// it publishes no discovery entry. `dmsg-socks5 serve` is the half that
		// listens, and it registers. Set here rather than in init() because both
		// subcommands share the package global.
		dmsgclient.NoRegister = true
		dmsgC, closeDmsg, err := dmsgclient.InitDmsgWithFlags(ctx, dlog, pk, sk, nil, pubKey.String())

		if err != nil {
			dlog.WithError(err).Fatal("Error connecting to dmsg network")
			return
		}

		defer closeDmsg()
		dlog.Infof("dmsg client pk: " + pk.String())

		proxyListenAddr := fmt.Sprintf("127.0.0.1:%d", proxyPort)
		tcpL, err := net.Listen("tcp", proxyListenAddr)
		if err != nil {
			dlog.Fatalf("Error listening on %s: %v", proxyListenAddr, err)
		}
		defer func() {
			if err := tcpL.Close(); err != nil {
				dlog.Printf("Error closing local listener: %v", err)
			}
		}()
		go func() {
			<-ctx.Done()
			if err := tcpL.Close(); err != nil {
				dlog.WithError(err).Debug("Error closing local listener on context cancellation")
			}
		}()

		remote := dmsg.Addr{PK: pubKey, Port: dmsgPort}
		dlog.Infof("Serving SOCKS5 proxy on %s over dmsg://%s:%d", proxyListenAddr, pubKey.String(), dmsgPort)
		if err := runSocks5Client(ctx, dlog, tcpL, dmsgC, remote); err != nil {
			dlog.WithError(err).Error("SOCKS5 proxy client stopped")
		}
	},
}

// streamDialer is the slice of *dmsg.Client that runSocks5Client needs.
type streamDialer interface {
	DialStream(ctx context.Context, addr dmsg.Addr) (*dmsg.Stream, error)
}

// runSocks5Client accepts local TCP connections on tcpL and gives each one its
// OWN dmsg stream to the remote SOCKS5 server, then pipes bytes in both
// directions. The client speaks no SOCKS5 itself — the handshake and the
// CONNECT request are passed through untouched, so the target is dialed by the
// server end and every byte of the session crosses dmsg.
func runSocks5Client(ctx context.Context, log *logging.Logger, tcpL net.Listener, dialer streamDialer, remote dmsg.Addr) error {
	for {
		conn, err := tcpL.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				log.Info("Shutting down SOCKS5 proxy client...")
				return nil
			default:
				return fmt.Errorf("accepting local connection: %w", err)
			}
		}
		go func() {
			stream, err := dialer.DialStream(ctx, remote)
			if err != nil {
				log.WithError(err).Errorf("Error dialing responder %s", remote.String())
				conn.Close() //nolint:errcheck,gosec
				return
			}
			pipeConns(conn, stream)
		}()
	}
}

// pipeConns copies bytes between two connections until either side is done,
// then closes both.
func pipeConns(a, b net.Conn) {
	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			a.Close() //nolint:errcheck,gosec
			b.Close() //nolint:errcheck,gosec
		})
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(a, b) //nolint:errcheck,gosec
		closeBoth()
	}()
	go func() {
		defer wg.Done()
		io.Copy(b, a) //nolint:errcheck,gosec
		closeBoth()
	}()
	wg.Wait()
}
