// Package commands cmd/dmsghttp/commands/dmsghttp.go
package commands

import (
	"context"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

var (
	sk       cipher.SecKey
	dmsgDisc string
	serveDir string
	dmsgPort uint
	wl       string
	wlkeys   []cipher.PubKey
)

func init() {
	RootCmd.Flags().StringVarP(&serveDir, "dir", "d", ".", "local dir to serve via dmsghttp")
	RootCmd.Flags().UintVarP(&dmsgPort, "port", "p", 80, "dmsg port to serve from")
	RootCmd.Flags().StringVarP(&wl, "wl", "w", "", "whitelist keys, comma separated")
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "D", "", "dmsg discovery url default:\n"+skyenv.DmsgDiscAddr)
	if os.Getenv("DMSGHTTP_SK") != "" {
		sk.Set(os.Getenv("DMSGHTTP_SK")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for dmsghttp")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd contains the root dmsghttp command
var RootCmd = &cobra.Command{
	Use:   "http",
	Short: "dmsghttp file server",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┬ ┬┌┬┐┌┬┐┌─┐
	 │││││└─┐│ ┬├─┤ │  │ ├─┘
	─┴┘┴ ┴└─┘└─┘┴ ┴ ┴  ┴ ┴  `,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	PreRun: func(cmd *cobra.Command, args []string) {
		if dmsgDisc == "" {
			dmsgDisc = skyenv.DmsgDiscAddr
		}
	},
	Run: func(cmd *cobra.Command, args []string) {
		log := logging.MustGetLogger("dmsghttp")

		ctx, cancel := cmdutil.SignalContext(context.Background(), log)
		defer cancel()
		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		if wl != "" {
			wlk := strings.Split(wl, ",")
			for _, key := range wlk {
				var pubKey cipher.PubKey
				err := pubKey.Set(key)
				if err == nil {
					wlkeys = append(wlkeys, pubKey)
				}
			}
		}
		if len(wlkeys) > 0 {
			if len(wlkeys) == 1 {
				log.Info(fmt.Sprintf("%d key whitelisted", len(wlkeys)))
			} else {
				log.Info(fmt.Sprintf("%d keys whitelisted", len(wlkeys)))
			}
		}

		c := dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, log), dmsg.DefaultConfig())
		defer func() {
			if err := c.Close(); err != nil {
				log.WithError(err).Error()
			}
		}()

		go c.Serve(context.Background())

		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Warn()
			return

		case <-c.Ready():
		}

		lis, err := c.Listen(uint16(dmsgPort))
		if err != nil {
			log.WithError(err).Fatal()
		}
		go func() {
			<-ctx.Done()
			if err := lis.Close(); err != nil {
				log.WithError(err).Error()
			}
		}()

		log.WithField("dir", serveDir).
			WithField("dmsg_addr", lis.Addr().String()).
			Info("Serving...")

		http.HandleFunc("/", fileServerHandler)
		serve := &http.Server{
			ReadHeaderTimeout: 3 * time.Second,
		}
		log.Fatal(serve.Serve(lis))

	},
}

func fileServerHandler(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	// Get the remote PK.
	remotePK, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	// Check if the remote PK is whitelisted.
	whitelisted := false
	if len(wlkeys) == 0 {
		whitelisted = true
	} else {
		for _, pubKey := range wlkeys {
			if remotePK == pubKey.String() {
				whitelisted = true
				break
			}
		}
	}

	// If the remote PK is whitelisted, serve the file.
	if whitelisted {
		filePath := serveDir + r.URL.Path
		file, err := os.Open(filePath) //nolint
		if err != nil {
			http.Error(w, http.StatusText(http.StatusNotFound), http.StatusNotFound)
			return
		}
		defer file.Close() //nolint

		_, filename := path.Split(filePath)
		w.Header().Set("Content-Type", mime.TypeByExtension(filepath.Ext(filename)))
		http.ServeContent(w, r, filename, time.Time{}, file)

		// Log the response status and time taken.
		elapsed := time.Since(start)
		log.Printf("[DMSGHTTP] %s %s | %d | %v | %s | %s %s\n", start.Format("2006/01/02 - 15:04:05"), r.RemoteAddr, http.StatusOK, elapsed, r.Method, r.Proto, r.URL)
		return
	}

	// Otherwise, return a 403 Forbidden error.
	http.Error(w, http.StatusText(http.StatusForbidden), http.StatusForbidden)

	// Log the response status and time taken.
	elapsed := time.Since(start)
	log.Printf("[DMSGHTTP] %s %s | %d | %v | %s | %s %s\n", start.Format("2006/01/02 - 15:04:05"), r.RemoteAddr, http.StatusForbidden, elapsed, r.Method, r.Proto, r.URL)
}

// Execute executes root CLI command.
func Execute() {
	cc.Init(&cc.Config{
		RootCmd:       RootCmd,
		Headings:      cc.HiBlue + cc.Bold, //+ cc.Underline,
		Commands:      cc.HiBlue + cc.Bold,
		CmdShortDescr: cc.HiBlue,
		Example:       cc.HiBlue + cc.Italic,
		ExecName:      cc.HiBlue + cc.Bold,
		Flags:         cc.HiBlue + cc.Bold,
		//FlagsDataType: cc.HiBlue,
		FlagsDescr:      cc.HiBlue,
		NoExtraNewlines: true,
		NoBottomNewline: true,
	})
	if err := RootCmd.Execute(); err != nil {
		log.Fatal("Failed to execute command: ", err)
	}
}

const help = "Usage:\r\n" +
	"  {{.UseLine}}{{if .HasAvailableSubCommands}}{{end}} {{if gt (len .Aliases) 0}}\r\n\r\n" +
	"{{.NameAndAliases}}{{end}}{{if .HasAvailableSubCommands}}\r\n\r\n" +
	"Available Commands:{{range .Commands}}{{if (or .IsAvailableCommand)}}\r\n  " +
	"{{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}\r\n\r\n" +
	"Flags:\r\n" +
	"{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}\r\n\r\n" +
	"Global Flags:\r\n" +
	"{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}\r\n\r\n"
