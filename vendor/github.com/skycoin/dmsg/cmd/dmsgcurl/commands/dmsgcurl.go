// Package commands cmd/dmsgcurl/commands
package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/calvin"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/spf13/cobra"
	"golang.org/x/net/proxy"

	"github.com/skycoin/dmsg/internal/cli"
	"github.com/skycoin/dmsg/internal/flags"
	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

var (
	dmsgcurlData   string
	sk             cipher.SecKey
	pk             cipher.PubKey
	destPK         cipher.PubKey
	dlog           = logging.MustGetLogger("dmsgcurl")
	dmsgcurlAgent  string
	logLvl         string
	dmsgcurlTries  int
	dmsgcurlWait   int
	dmsgcurlOutput string
	replace        bool
	proxyAddr      string
	dialer         = proxy.Direct //nolint unused
	err            error
)

func init() {
	RootCmd.Flags().SortFlags = false
	flags.InitFlags(RootCmd)
	RootCmd.Flags().StringVarP(&proxyAddr, "proxy", "p", proxyAddr, "connect to DMSG via proxy (i.e. '127.0.0.1:1080')")
	RootCmd.Flags().StringVarP(&logLvl, "loglvl", "l", "fatal", "[ debug | warn | error | fatal | panic | trace | info ]\033[0m\n\r")
	RootCmd.Flags().StringVarP(&dmsgcurlData, "data", "d", "", "dmsghttp POST data")
	RootCmd.Flags().StringVarP(&dmsgcurlOutput, "out", "o", "", "output filepath")
	RootCmd.Flags().BoolVarP(&replace, "replace", "r", false, "replace existing file with new downloaded")
	RootCmd.Flags().IntVarP(&dmsgcurlTries, "try", "t", 1, "download attempts (0 unlimits)\033[0m\n\r")
	RootCmd.Flags().IntVarP(&dmsgcurlWait, "wait", "w", 0, "time to wait between requests")
	RootCmd.Flags().StringVarP(&dmsgcurlAgent, "agent", "a", "dmsgcurl/"+buildinfo.Version(), "identify as `AGENT`\033[0m\n\r")
	if os.Getenv("DMSGCURL_SK") != "" {
		sk.Set(os.Getenv("DMSGCURL_SK")) //nolint
	}
	RootCmd.Flags().VarP(&sk, "sk", "s", "a random key is generated if unspecified\033[0m\n\r")
}

// RootCmd contains the root cli command
var RootCmd = &cobra.Command{
	Use:   "curl",
	Short: "DMSG curl utility",
	Long: calvin.AsciiFont("dmsgcurl") + `
	DMSG curl utility`,
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	RunE: func(_ *cobra.Command, args []string) error {
		if logLvl != "" {
			if lvl, err := logging.LevelFromString(logLvl); err == nil {
				logging.SetLevel(lvl)
			}
		}

		if flags.DmsgHTTPPath != "" {
			dmsg.DmsghttpJSON, err = os.ReadFile(flags.DmsgHTTPPath) //nolint
			if err != nil {
				dlog.WithError(err).Fatal("Failed to read specified dmsghttp-config")
			}
			err = dmsg.InitConfig()
			if err != nil {
				dlog.WithError(err).Fatal("Failed to unmarshal dmsghttp-config")
			}
		}

		pk, err = sk.PubKey()
		if err != nil {
			pk, sk = cipher.GenerateKeyPair()
		}
		if len(args) == 0 {
			dlog.WithError(fmt.Errorf("no URL(s) provided")).Error(errorDesc["FAILED_INIT"] + "\n")
			os.Exit(errorCode["FAILED_INIT"])
		}
		if len(args) > 1 {
			dlog.WithError(fmt.Errorf("multiple URLs are not yet supported")).Error(errorDesc["FAILED_INIT"] + "\n")
			os.Exit(errorCode["FAILED_INIT"])
		}
		parsedURL, err := url.Parse(args[0])
		if err != nil {
			dlog.WithError(fmt.Errorf("failed to parse provided URL")).Error(errorDesc["URL_MALFORMAT"] + "\n")
			os.Exit(errorCode["URL_MALFORMAT"])
		}
		destSlc := strings.Split(parsedURL.Host, ":")
		if len(destSlc) == 1 {
			destSlc = append(destSlc, "80")
		}
		err = destPK.Set(destSlc[0])
		if err != nil {
			dlog.WithError(err).Fatal("bad PK for host\n")
		}

		var cErr curlError
		ctx, cancel := cmdutil.SignalContext(context.Background(), dlog)
		defer cancel()

		httpClient := &http.Client{}
		if proxyAddr != "" {
			// Use SOCKS5 proxy dialer if specified
			dialer, err := proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
			if err != nil {
				dlog.WithError(fmt.Errorf("Error creating SOCKS5 dialer: %v", err)).Error(errorDesc["COULDNT_RESOLVE_PROXY"])
				os.Exit(errorCode["COULDNT_RESOLVE_PROXY"])
			}
			transport := &http.Transport{
				Dial: dialer.Dial,
			}
			httpClient = &http.Client{
				Transport: transport,
			}
			ctx = context.WithValue(context.Background(), "socks5_proxy", proxyAddr) //nolint
		}

		cErr = handleRequest(ctx, pk, sk, httpClient, parsedURL, dmsgcurlData)
		if cErr.Code == 0 {
			return nil
		}

		if cErr.Code != 0 {
			dlog.WithError(cErr.Error).Error("An error occurred\n")
			return cErr.Error
		}
		return err
	},
}

func handleRequest(ctx context.Context, pk cipher.PubKey, sk cipher.SecKey, httpClient *http.Client, parsedURL *url.URL, dmsgcurlData string) curlError {
	file, err := prepareOutputFile()
	if err != nil {
		return curlError{
			Error: fmt.Errorf("%s", errorDesc["WRITE_INIT"]),
			Code:  errorCode["WRITE_INIT"],
		}
	}
	defer closeAndCleanFile(file, err)
	var dmsgC *dmsg.Client
	var closeDmsg func()

	if flags.UseDC {
		dmsgC, closeDmsg, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, "", flags.DmsgSessions, pk.String())
	} else {
		if flags.UseHTTP {
			dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, httpClient, flags.DmsgDiscURL, flags.DmsgSessions)
		} else {
			var dmsgDC *dmsg.Client
			var closeDmsgDC func()
			dmsgDC, closeDmsgDC, err = cli.StartDmsgDirect(ctx, dlog, pk, sk, httpClient, "", flags.DmsgSessions, dmsg.ExtractPKFromDmsgAddr(flags.DmsgDiscAddr))
			if err != nil {
				dlog.WithError(err).Error("Error connecting to dmsg network")
				return curlError{
					Error: fmt.Errorf("%s", errorDesc["DMSG_INIT"]),
					Code:  errorCode["DMSG_INIT"],
				}
			}
			defer closeDmsgDC()
			dmsgHTTP := &http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgDC)}
			dmsgC, closeDmsg, err = cli.StartDmsg(ctx, dlog, pk, sk, dmsgHTTP, flags.DmsgDiscAddr, flags.DmsgSessions)
		}
	}

	if err != nil {
		dlog.WithError(err).Debug("Error connecting to dmsg network")
		return curlError{
			Error: fmt.Errorf("%s", errorDesc["DMSG_INIT"]),
			Code:  errorCode["DMSG_INIT"],
		}
	}
	defer closeDmsg()

	if dmsgC == nil {
		dlog.Error("nil dmsg client pointer")
		return curlError{
			Error: fmt.Errorf("%s", errorDesc["DMSG_INIT"]),
			Code:  errorCode["DMSG_INIT"],
		}
	}

	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}
	firstTry := true
	for i := 0; i < dmsgcurlTries; i++ {
		if dmsgcurlOutput != "" {
			if !firstTry {
				dlog.Debugf("Download attempt %d/%d ...", i, dmsgcurlTries)
			}
			firstTry = false
			if _, err := file.Seek(0, 0); err != nil {
				return curlError{
					Error: fmt.Errorf("%s", errorDesc["WRITE_ERROR"]),
					Code:  errorCode["WRITE_ERROR"],
				}
			}
		}
		var req *http.Request
		if dmsgcurlData != "" {
			req, err = http.NewRequest(http.MethodPost, parsedURL.String(), strings.NewReader(dmsgcurlData))
		} else {
			req, err = http.NewRequest(http.MethodGet, parsedURL.String(), nil)
		}
		if err != nil {
			dlog.WithError(err).Error("Failed to formulate HTTP request\n")
			return curlError{
				Error: fmt.Errorf("%s", errorDesc["FAILED_INIT"]),
				Code:  errorCode["FAILED_INIT"],
			}
		}
		if dmsgcurlData != "" {
			req.Header.Set("Content-Type", "text/plain")
		}
		resp, err := httpC.Do(req)
		for attempts := 1; attempts <= 10; attempts++ {
			if err != nil {
				var netErr net.Error

				if errors.As(err, &netErr) && netErr.Timeout() {
					dlog.WithError(err).Error("Failed to perform HTTP request\n")
					return curlError{
						Error: fmt.Errorf("%s", errorDesc["RECV_ERROR"]),
						Code:  errorCode["RECV_ERROR"],
					}
				} else if errors.Is(err, context.DeadlineExceeded) {
					dlog.WithError(err).Error("Failed to perform HTTP request\n")
					return curlError{
						Error: fmt.Errorf("%s", errorDesc["RECV_ERROR"]),
						Code:  errorCode["RECV_ERROR"],
					}
				}

				dlog.WithError(err).Debugf("Attempt %d failed, retrying...\n", attempts)
				time.Sleep(time.Duration(attempts) * time.Second) // Exponential backoff
				resp, err = httpC.Do(req)
				continue
			}

			defer resp.Body.Close() //nolint
			dlog.Debugf("Request succeeded with status code: %d\n", resp.StatusCode)
			break
		}

		if err != nil {
			dlog.WithError(err).Debug("Failed to perform HTTP request after maximum retries\n")
			return curlError{
				Error: fmt.Errorf("%s", errorDesc["RECV_ERROR"]),
				Code:  errorCode["RECV_ERROR"],
			}
		}

		n, err := cancellableCopy(ctx, file, resp.Body, resp.ContentLength)
		if err != nil {
			dlog.WithError(err).Error(fmt.Sprintf("download failed at %d/%dB\n", n, resp.ContentLength))
			return curlError{
				Error: fmt.Errorf("%s", errorDesc["DOWNLOAD_ERROR"]),
				Code:  errorCode["DOWNLOAD_ERROR"],
			}
		}
		defer closeResponseBody(resp)
		if err != nil {
			dlog.WithError(err).Error()
			select {
			case <-ctx.Done():
				return curlError{
					Error: fmt.Errorf("%s", errorDesc["CONTEXT_CANCELED"]),
					Code:  errorCode["CONTEXT_CANCELED"],
				}
			case <-time.After(time.Duration(dmsgcurlWait) * time.Second):
				continue
			}
		}
		return curlError{
			Error: fmt.Errorf("%s", errorDesc["SUCCESS"]),
			Code:  errorCode["SUCCESS"],
		}
	}
	if err != nil {
		return curlError{
			Error: fmt.Errorf("%s", errorDesc["FAILURE"]),
			Code:  errorCode["FAILURE"],
		}
	}
	return curlError{
		Error: fmt.Errorf("%s", errorDesc["SUCCESS"]),
		Code:  errorCode["SUCCESS"],
	}
}

func prepareOutputFile() (*os.File, error) {
	if dmsgcurlOutput == "" {
		return os.Stdout, nil
	}
	return parseOutputFile(dmsgcurlOutput, replace)
}

func closeAndCleanFile(file *os.File, err error) {
	if fErr := file.Close(); fErr != nil {
		dlog.WithError(fErr).Warn("Failed to close output file.\n")
	}
	if err != nil && file != os.Stdout {
		if rErr := os.RemoveAll(file.Name()); rErr != nil {
			dlog.WithError(rErr).Warn("Failed to remove output file.\n")
		}
	}
}

func closeResponseBody(resp *http.Response) {
	if err := resp.Body.Close(); err != nil {
		dlog.WithError(err).Debug("Failed to close response body\n")
	}
}

func parseOutputFile(output string, replace bool) (*os.File, error) {
	_, statErr := os.Stat(output)
	if statErr != nil {
		if os.IsNotExist(statErr) {
			if err := os.MkdirAll(filepath.Dir(output), fs.ModePerm); err != nil {
				return nil, err
			}
			f, err := os.Create(output) //nolint
			if err != nil {
				return nil, err
			}
			return f, nil
		}
		return nil, statErr
	}
	if replace {
		return os.OpenFile(filepath.Clean(output), os.O_RDWR|os.O_CREATE|os.O_TRUNC, os.ModePerm) //nolint
	}
	return nil, os.ErrExist
}

type readerFunc func(p []byte) (n int, err error)

func (rf readerFunc) Read(p []byte) (n int, err error) { return rf(p) }

func cancellableCopy(ctx context.Context, w io.Writer, body io.ReadCloser, length int64) (int64, error) {
	n, err := io.Copy(io.MultiWriter(w, &progressWriter{Total: length}), readerFunc(func(p []byte) (int, error) {
		select {
		case <-ctx.Done():
			return 0, errors.New("Download Canceled")
		default:
			return body.Read(p)
		}
	}))
	return n, err
}

type progressWriter struct {
	Current int64
	Total   int64
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n := len(p)
	current := atomic.AddInt64(&pw.Current, int64(n))
	total := atomic.LoadInt64(&pw.Total)
	pc := fmt.Sprintf("%d%%", current*100/total)
	if dmsgcurlOutput != "" {
		fmt.Printf("Downloading: %d/%dB (%s)", current, total, pc)
		if current != total {
			fmt.Print("\r")
		} else {
			fmt.Print("\n")
		}
	}
	return n, nil
}

// Execute executes the RootCmd
func Execute() {
	if err := RootCmd.Execute(); err != nil {
		// WHY WON'T THIS PRINT??
		dlog.WithError(err).Debug("An error occurred\n")
		log.Fatal("Failed to execute command: ", err)
	}
}
