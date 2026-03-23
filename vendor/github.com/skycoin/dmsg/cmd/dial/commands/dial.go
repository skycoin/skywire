// Package commands cmd/dial/commands/dial.go
package commands

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/chen3feng/safecast"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgclient"
)

var (
	sk       cipher.SecKey
	dpk      cipher.PubKey
	waitTime int
	dport    uint
	logLvl   string
)

func init() {
	dmsgclient.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "info", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	RootCmd.Flags().IntVarP(&waitTime, "wait", "w", 0, "wait time in seconds before disconnecting\n\r\033[0m")
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r\033[0m")
}

// RootCmd contains the root dmsgcurl command
var RootCmd = &cobra.Command{
	Use:   dmsgclient.ExecName(),
	Short: "DMSG Dial network test utility",
	Long: calvin.AsciiFont("dmsgdial") + `
DMSG Dial network test utility
Test connection to dmsg servers
Test connecting to dmsg client address [<pk>:<port>]

Default mode of operation is dmsghttp:
* Start dmsg-direct client ; connect directly to a dmsg server
* HTTP client is configured with a dmsg HTTP transport provided by the dmsg-direct client
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery dmsg address
* If the dmsg-discovery is unreachable via the configured http client:
	- Shuffle dmsg servers
	- Re-make dmsg direct clent
	- Reconfigure HTTP client with dmsg HTTP transport provided by the dmsg-direct client
	- Fetch '/health' from dmsg discovery dmsg address [<pk>:<port>]
	- Repeat the previous 4 steps on error / until no error
* Start dmsghttp client
* Connect to dmsg client address (if specified)

'-Z' flag: use plain http to connect to dmsg-discovery
* HTTP client is used to make HTTP GET request to '/health' of dmsg discovery URL
* Start dmsg client
* Connect to dmsg client address (if specified)

'-B' flag: use dmsg direct client
* Start dmsg-direct client
* Connect to dmsg client address (if specified)
`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, args []string) {
		dlog := logging.MustGetLogger("dmsgdial")
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		//		var rpk cipher.PubKey
		pk, err := sk.PubKey()
		if err != nil {
			_, sk = cipher.GenerateKeyPair()
			pk, err = sk.PubKey()
			if err != nil {
				dlog.WithError(err).Fatal("Failed to derive public key from secret key")
			}
		}

		if len(args) > 0 && strings.Contains(args[0], ":") {
			parts := strings.Split(args[0], ":")
			if len(parts) < 1 || parts[0] == "" {
				dlog.Fatal("Invalid dmsg address format. Expected <public_key>[:<port>]")
			}

			// Parse the public key
			if err := dpk.Set(parts[0]); err != nil {
				dlog.WithError(err).Fatal("Failed to parse public key from dmsg address")
			}
			dlog.Debug("Parsed dmsg client public key to dial: ", dpk.String())
			// Parse the port or use the default (80)
			dport = uint(80) // Default port
			if len(parts) > 1 && parts[1] != "" {
				parsedPort, err := strconv.ParseUint(parts[1], 10, 16) // Ports are 16-bit unsigned integers
				if err != nil {
					dlog.WithError(err).Fatal("Failed to parse dmsg port")
				}
				dport = uint(parsedPort)
			}
			dlog.Debug("Parsed dmsg client port to dial: ", dport)
		}

		httpClient := &http.Client{}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		var dmsgClients []*dmsg.Client
		if dmsgclient.UseDC {
			dlog.Debug("Starting DMSG direct clients.")
			for _, server := range dmsg.Prod.DmsgServers {
				if len(dmsgClients) >= dmsgclient.DmsgSessions {
					break
				}
				dest := dpk.String()

				dmsgDC, closeFn, err := dmsgclient.StartDmsgDirectWithServers(ctx, dlog, pk, sk, "", []*disc.Entry{&server}, dmsgclient.DmsgSessions, dest)
				if err != nil {
					dlog.WithError(err).Error("Failed to start DMSG direct client. Skipping server...")
					continue
				}

				defer closeFn()
				dmsgClients = append(dmsgClients, dmsgDC)
			}
		} else {
			dmsgC, closeDmsg, err := dmsgclient.InitDmsgWithFlags(ctx, dlog, pk, sk, httpClient, pk.String())
			if err != nil {
				dlog.WithError(err).Error("Error connecting to dmsg network")
				return
			}
			defer closeDmsg()
			dmsgClients = append(dmsgClients, dmsgC)
		}

		if len(args) > 0 {
			dp, ok := safecast.To[uint16](dport)
			if !ok {
				dlog.Fatal("uint16 overflow when converting dmsg port")
			}
			dlog.Debug(fmt.Sprintf("Dialing dmsg address %v:%v", dpk.String(), dp))
			for _, dmsgC := range dmsgClients {
				dmsgConn, err := dmsgC.DialStream(context.Background(), dmsg.Addr{PK: dpk, Port: dp}) //nolint
				if err != nil {
					dlog.WithError(err).Warn("Failed to dial remote host: ", args[0], " via dmsg server: ", dmsgC.ConnectedServersPK())
					err = dmsgConn.Close() //nolint
					if err != nil {
						dlog.WithError(err).Error("Error closing dmsg client connection")
					}
					continue
				}
				dlog.Info("Successfully dialed remote host: ", args[0], " with dmsg server: ", dmsgC.ConnectedServersPK())

				err = dmsgConn.Close() //nolint
				if err != nil {
					dlog.WithError(err).Error("Error closing dmsg client connection")
				}
			}
		}

		time.Sleep(time.Duration(waitTime) * time.Second)
		dlog.Debug("Disconnecting from dmsg network")

	},
}

// Execute executes root CLI command.
func Execute() {
	dmsgclient.Execute(RootCmd)
}
