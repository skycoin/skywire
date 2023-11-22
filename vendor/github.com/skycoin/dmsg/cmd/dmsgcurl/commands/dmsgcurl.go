// Package commands cmd/dmsgcurl/commands/dmsgcurl.go
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	cc "github.com/ivanpirog/coloredcobra"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/skyenv"
	"github.com/spf13/cobra"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

var (
	dmsgDisc     string
	dmsgSessions int
	dmsgcurlData string
	//	dmsgcurlHeader string
	sk             cipher.SecKey
	dmsgcurlLog    *logging.Logger
	dmsgcurlAgent  string
	logLvl         string
	dmsgcurlTries  int
	dmsgcurlWait   int
	dmsgcurlOutput string
	stdout         bool
)

func init() {
	RootCmd.Flags().StringVarP(&dmsgDisc, "dmsg-disc", "c", "", "dmsg discovery url default:\n"+skyenv.DmsgDiscAddr)
	RootCmd.Flags().IntVarP(&dmsgSessions, "sess", "e", 1, "number of dmsg servers to connect to")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m")
	RootCmd.Flags().StringVarP(&dmsgcurlData, "data", "d", "", "dmsghttp POST data")
	//	RootCmd.Flags().StringVarP(&dmsgcurlHeader, "header", "H", "", "Pass custom header(s) to server")
	RootCmd.Flags().StringVarP(&dmsgcurlOutput, "out", "o", ".", "output filepath")
	RootCmd.Flags().BoolVarP(&stdout, "stdout", "n", false, "output to STDOUT")
	RootCmd.Flags().IntVarP(&dmsgcurlTries, "try", "t", 1, "download attempts (0 unlimits)")
	RootCmd.Flags().IntVarP(&dmsgcurlWait, "wait", "w", 0, "time to wait between fetches")
	RootCmd.Flags().StringVarP(&dmsgcurlAgent, "agent", "a", "dmsgcurl/"+buildinfo.Version(), "identify as `AGENT`")
	if os.Getenv("DMSGCURL_SK") != "" {
		sk.Set(os.Getenv("DMSGCURL_SK")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\n\r")
	var helpflag bool
	RootCmd.SetUsageTemplate(help)
	RootCmd.PersistentFlags().BoolVarP(&helpflag, "help", "h", false, "help for dmsgcurl")
	RootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
	RootCmd.PersistentFlags().MarkHidden("help") //nolint
}

// RootCmd containsa the root dmsgcurl command
var RootCmd = &cobra.Command{
	Short: "dmsgcurl",
	Use:   "dmsgcurl [OPTIONS] ... [URL]",
	Long: `
	┌┬┐┌┬┐┌─┐┌─┐┌─┐┬ ┬┬─┐┬  
	 │││││└─┐│ ┬│  │ │├┬┘│  
	─┴┘┴ ┴└─┘└─┘└─┘└─┘┴└─┴─┘`,
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
	RunE: func(cmd *cobra.Command, args []string) error {
		if dmsgcurlLog == nil {
			dmsgcurlLog = logging.MustGetLogger("dmsgcurl")
		}
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		ctx, cancel := cmdutil.SignalContext(context.Background(), dmsgcurlLog)
		defer cancel()

		pk, err := sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}

		u, err := parseURL(args)
		if err != nil {
			dmsgcurlLog.WithError(err).Fatal("failed to parse provided URL")
		}
		if dmsgcurlData != "" {
			dmsgC, closeDmsg, err := startDmsg(ctx, pk, sk)
			if err != nil {
				dmsgcurlLog.WithError(err).Fatal("failed to start dmsg")
			}
			defer closeDmsg()

			httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

			req, err := http.NewRequest(http.MethodPost, u.URL.String(), strings.NewReader(dmsgcurlData))
			if err != nil {
				dmsgcurlLog.WithError(err).Fatal("Failed to formulate HTTP request.")
			}
			req.Header.Set("Content-Type", "text/plain")

			resp, err := httpC.Do(req)
			if err != nil {
				dmsgcurlLog.WithError(err).Fatal("Failed to execute HTTP request.")
			}

			defer func() {
				if err := resp.Body.Close(); err != nil {
					dmsgcurlLog.WithError(err).Fatal("Failed to close response body")
				}
			}()
			respBody, err := io.ReadAll(resp.Body)
			if err != nil {
				dmsgcurlLog.WithError(err).Fatal("Failed to read respose body.")
			}
			fmt.Println(string(respBody))
		} else {

			file, err := parseOutputFile(dmsgcurlOutput, u.URL.Path)
			if err != nil {
				return fmt.Errorf("failed to prepare output file: %w", err)
			}
			defer func() {
				if fErr := file.Close(); fErr != nil {
					dmsgcurlLog.WithError(fErr).Warn("Failed to close output file.")
				}
				if err != nil {
					if rErr := os.RemoveAll(file.Name()); rErr != nil {
						dmsgcurlLog.WithError(rErr).Warn("Failed to remove output file.")
					}
				}
			}()

			dmsgC, closeDmsg, err := startDmsg(ctx, pk, sk)
			if err != nil {
				return fmt.Errorf("failed to start dmsg: %w", err)
			}
			defer closeDmsg()

			httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

			for i := 0; i < dmsgcurlTries; i++ {
				if !stdout {
					dmsgcurlLog.Debugf("Download attempt %d/%d ...", i, dmsgcurlTries)
				}

				if _, err := file.Seek(0, 0); err != nil {
					return fmt.Errorf("failed to reset file: %w", err)
				}
				if stdout {
					if fErr := file.Close(); fErr != nil {
						dmsgcurlLog.WithError(fErr).Warn("Failed to close output file.")
					}
					if err != nil {
						if rErr := os.RemoveAll(file.Name()); rErr != nil {
							dmsgcurlLog.WithError(rErr).Warn("Failed to remove output file.")
						}
					}
					file = os.Stdout
				}
				if err := Download(ctx, dmsgcurlLog, &httpC, file, u.URL.String(), 0); err != nil {
					dmsgcurlLog.WithError(err).Error()
					select {
					case <-ctx.Done():
						return ctx.Err()
					case <-time.After(time.Duration(dmsgcurlWait) * time.Second):
						continue
					}
				}

				// download successful.
				return nil
			}

			return errors.New("all download attempts failed")

		}
		return nil
	},
}

// URL represents a dmsg http URL.
type URL struct {
	dmsg.Addr
	url.URL
}

// Fill fills the internal fields from an URL string.
func (du *URL) fill(str string) error {
	u, err := url.Parse(str)
	if err != nil {
		return err
	}

	if u.Scheme == "" {
		return errors.New("URL is missing a scheme")
	}

	if u.Host == "" {
		return errors.New("URL is missing a host")
	}

	du.URL = *u
	return du.Addr.Set(u.Host)
}

func parseURL(args []string) (*URL, error) {
	if len(args) == 0 {
		return nil, errors.New("no URL(s) provided")
	}

	if len(args) > 1 {
		return nil, errors.New("multiple URLs is not yet supported")
	}

	var out URL
	if err := out.fill(args[0]); err != nil {
		return nil, fmt.Errorf("provided URL is invalid: %w", err)
	}

	return &out, nil
}

func parseOutputFile(name string, urlPath string) (*os.File, error) {
	stat, statErr := os.Stat(name)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			f, err := os.Create(name) //nolint
			if err != nil {
				return nil, err
			}
			return f, nil
		}
		return nil, statErr
	}

	if stat.IsDir() {
		f, err := os.Create(filepath.Join(name, urlPath)) //nolint
		if err != nil {
			return nil, err
		}
		return f, nil
	}

	return nil, os.ErrExist
}

func startDmsg(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dmsgDisc, &http.Client{}, dmsgcurlLog), &dmsg.Config{MinSessions: dmsgSessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		dmsgcurlLog.WithError(err).Debug("Disconnected from dmsg network.")
		fmt.Printf("\n")
	}
	dmsgcurlLog.WithField("public_key", pk.String()).WithField("dmsg_disc", dmsgDisc).
		Debug("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		dmsgcurlLog.Debug("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// Download downloads a file from the given URL into 'w'.
func Download(ctx context.Context, log logrus.FieldLogger, httpC *http.Client, w io.Writer, urlStr string, maxSize int64) error {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		log.WithError(err).Fatal("Failed to formulate HTTP request.")
	}
	resp, err := httpC.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
	}
	if maxSize > 0 {
		if resp.ContentLength > maxSize*1024 {
			return fmt.Errorf("requested file size is more than allowed size: %d KB > %d KB", (resp.ContentLength / 1024), maxSize)
		}
	}
	n, err := CancellableCopy(ctx, w, resp.Body, resp.ContentLength)
	if err != nil {
		return fmt.Errorf("download failed at %d/%dB: %w", n, resp.ContentLength, err)
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.WithError(err).Warn("HTTP Response body closed with non-nil error.")
		}
	}()

	return nil
}

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

// CancellableCopy will call the Reader and Writer interface multiple time, in order
// to copy by chunk (avoiding loading the whole file in memory).
func CancellableCopy(ctx context.Context, w io.Writer, body io.ReadCloser, length int64) (int64, error) {

	n, err := io.Copy(io.MultiWriter(w, &ProgressWriter{Total: length}), readerFunc(func(p []byte) (int, error) {

		// golang non-blocking channel: https://gobyexample.com/non-blocking-channel-operations
		select {

		// if context has been canceled
		case <-ctx.Done():
			// stop process and propagate "Download Canceled" error
			return 0, errors.New("Download Canceled")
		default:
			// otherwise just run default io.Reader implementation
			return body.Read(p)
		}
	}))
	return n, err
}

// ProgressWriter prints the progress of a download to stdout.
type ProgressWriter struct {
	// atomic requires 64-bit alignment for struct field access
	Current int64
	Total   int64
}

// Write implements io.Writer
func (pw *ProgressWriter) Write(p []byte) (int, error) {
	n := len(p)

	current := atomic.AddInt64(&pw.Current, int64(n))
	total := atomic.LoadInt64(&pw.Total)
	pc := fmt.Sprintf("%d%%", current*100/total)
	if !stdout {
		fmt.Printf("Downloading: %d/%dB (%s)", current, total, pc)
		if current != total {
			fmt.Print("\r")
		} else {
			fmt.Print("\n")
		}
	}

	return n, nil
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
