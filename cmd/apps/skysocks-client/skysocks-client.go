/*
proxy client app for skywire visor
*/
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/internal/skysocks"
	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	netType   = appnet.TypeSkynet
	socksPort = routing.Port(3)
)

var (
	log      = logrus.New()
	r        = netutil.NewRetrier(log, time.Second, netutil.DefaultMaxBackoff, 0, 1)
	serverPK string
	addr     string
)

func init() {
	rootCmd.Flags().SortFlags = false

	rootCmd.Flags().StringVarP(&addr, "addr", "a", skyenv.SkysocksClientAddr, "Client address to listen on")
	rootCmd.Flags().StringVarP(&serverPK, "srv", "q", "", "PubKey of the server to connect to")
}

var rootCmd = &cobra.Command{
	Use:   "skysocks-client",
	Short: "Skywire SOCKS5 Proxy Client",
	Long: `
	┌─┐┬┌─┬ ┬┌─┐┌─┐┌─┐┬┌─┌─┐   ┌─┐┬  ┬┌─┐┌┐┌┌┬┐
	└─┐├┴┐└┬┘└─┐│ ││  ├┴┐└─┐───│  │  │├┤ │││ │
	└─┘┴ ┴ ┴ └─┘└─┘└─┘┴ ┴└─┘   └─┘┴─┘┴└─┘┘└┘ ┴ `,
	Run: func(_ *cobra.Command, _ []string) {
		appC := app.NewClient(nil)
		defer appC.Close()

		skysocks.Log = log

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		if _, err := buildinfo.Get().WriteTo(os.Stdout); err != nil {
			log.Printf("Failed to output build info: %v", err)
		}

		if serverPK == "" {
			log.Warn("Empty server PubKey. Exiting")
			return
		}

		pk := cipher.PubKey{}
		if err := pk.UnmarshalText([]byte(serverPK)); err != nil {
			log.Fatal("Invalid server PubKey: ", err)
		}

		for {
			conn, err := dialServer(ctx, appC, pk)
			if err != nil {
				log.Fatalf("Failed to dial to a server: %v", err)
			}

			log.Printf("Connected to %v\n", pk)

			client, err := skysocks.NewClient(conn)
			if err != nil {
				log.Fatal("Failed to create a new client: ", err)
			}

			log.Printf("Serving proxy client %v\n", addr)

			if err := client.ListenAndServe(addr); err != nil {
				log.Errorf("Error serving proxy client: %v\n", err)
			}

			// need to filter this out, cause usually client failure means app conn is already closed
			if err := conn.Close(); err != nil && err != io.ErrClosedPipe {
				log.Errorf("Error closing app conn: %v\n", err)
			}

			log.Println("Reconnecting to skysocks server")
		}
	},
}

func dialServer(ctx context.Context, appCl *app.Client, pk cipher.PubKey) (net.Conn, error) {
	var conn net.Conn
	err := r.Do(ctx, func() error {
		var err error
		conn, err = appCl.Dial(appnet.Addr{
			Net:    netType,
			PubKey: pk,
			Port:   socksPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:         rootCmd,
		Headings:        cc.HiBlue + cc.Bold,
		Commands:        cc.HiBlue + cc.Bold,
		CmdShortDescr:   cc.HiBlue,
		Example:         cc.HiBlue + cc.Italic,
		ExecName:        cc.HiBlue + cc.Bold,
		Flags:           cc.HiBlue + cc.Bold,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
	}
}

func main() {
	Execute()
}
