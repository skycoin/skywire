// Package dmsgget pkg/dmsgget/dmsgget.go
package dmsgget

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsghttp"
)

var json = jsoniter.ConfigFastest

// DmsgGet contains the logic for dmsgget (wget over dmsg).
type DmsgGet struct {
	startF startupFlags
	dmsgF  dmsgFlags
	dlF    downloadFlags
	httpF  httpFlags
	fs     *flag.FlagSet
}

// New creates a new DmsgGet instance.
func New(fs *flag.FlagSet) *DmsgGet {
	dg := &DmsgGet{fs: fs}

	for _, fg := range dg.flagGroups() {
		fg.Init(fs)
	}

	w := fs.Output()
	flag.Usage = func() {
		_, _ = fmt.Fprintf(w, "Skycoin %s %s, wget over dmsg.\n", ExecName, Version)
		_, _ = fmt.Fprintf(w, "Usage: %s [OPTION]... [URL]\n\n", ExecName)
		flag.PrintDefaults()
		_, _ = fmt.Fprintln(w, "")
	}

	return dg
}

// String implements io.Stringer
func (dg *DmsgGet) String() string {
	m := make(map[string]interface{})
	for _, fg := range dg.flagGroups() {
		m[fg.Name()] = fg
	}
	j, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(j)
}

func (dg *DmsgGet) flagGroups() []FlagGroup {
	return []FlagGroup{&dg.startF, &dg.dmsgF, &dg.dlF, &dg.httpF}
}

// Run runs the download logic.
func (dg *DmsgGet) Run(ctx context.Context, log *logging.Logger, skStr string, args []string) (err error) {
	if log == nil {
		log = logging.MustGetLogger("dmsgget")
	}

	if dg.startF.Help {
		dg.fs.Usage()
		return nil
	}

	pk, sk, err := parseKeyPair(skStr)
	if err != nil {
		return fmt.Errorf("failed to parse provided key pair: %w", err)
	}

	u, err := parseURL(args)
	if err != nil {
		return fmt.Errorf("failed to parse provided URL: %w", err)
	}

	file, err := parseOutputFile(dg.dlF.Output, u.URL.Path)
	if err != nil {
		return fmt.Errorf("failed to prepare output file: %w", err)
	}
	defer func() {
		if fErr := file.Close(); fErr != nil {
			log.WithError(fErr).Warn("Failed to close output file.")
		}
		if err != nil {
			if rErr := os.RemoveAll(file.Name()); rErr != nil {
				log.WithError(rErr).Warn("Failed to remove output file.")
			}
		}
	}()

	dmsgC, closeDmsg, err := dg.StartDmsg(ctx, log, pk, sk)
	if err != nil {
		return fmt.Errorf("failed to start dmsg: %w", err)
	}
	defer closeDmsg()

	httpC := http.Client{Transport: dmsghttp.MakeHTTPTransport(ctx, dmsgC)}

	for i := 0; i < dg.dlF.Tries; i++ {
		log.Infof("Download attempt %d/%d ...", i, dg.dlF.Tries)

		if _, err := file.Seek(0, 0); err != nil {
			return fmt.Errorf("failed to reset file: %w", err)
		}

		if err := Download(ctx, log, &httpC, file, u.URL.String()); err != nil {
			log.WithError(err).Error()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(dg.dlF.Wait) * time.Second):
				continue
			}
		}

		// download successful.
		return nil
	}

	return errors.New("all download attempts failed")
}

func parseKeyPair(skStr string) (pk cipher.PubKey, sk cipher.SecKey, err error) {
	if skStr == "" {
		pk, sk = cipher.GenerateKeyPair()
		return
	}

	if err = sk.Set(skStr); err != nil {
		return
	}

	pk, err = sk.PubKey()
	return
}

func parseURL(args []string) (*URL, error) {
	if len(args) == 0 {
		return nil, ErrNoURLs
	}

	if len(args) > 1 {
		return nil, ErrMultipleURLsNotSupported
	}

	var out URL
	if err := out.Fill(args[0]); err != nil {
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

// StartDmsg create dsmg client instance
func (dg *DmsgGet) StartDmsg(ctx context.Context, log *logging.Logger, pk cipher.PubKey, sk cipher.SecKey) (dmsgC *dmsg.Client, stop func(), err error) {
	dmsgC = dmsg.NewClient(pk, sk, disc.NewHTTP(dg.dmsgF.Disc, &http.Client{}, log), &dmsg.Config{MinSessions: dg.dmsgF.Sessions})
	go dmsgC.Serve(context.Background())

	stop = func() {
		err := dmsgC.Close()
		log.WithError(err).Info("Disconnected from dmsg network.")
	}

	log.WithField("public_key", pk.String()).WithField("dmsg_disc", dg.dmsgF.Disc).
		Info("Connecting to dmsg network...")

	select {
	case <-ctx.Done():
		stop()
		return nil, nil, ctx.Err()

	case <-dmsgC.Ready():
		log.Info("Dmsg network ready.")
		return dmsgC, stop, nil
	}
}

// Download downloads a file from the given URL into 'w'.
func Download(ctx context.Context, log logrus.FieldLogger, httpC *http.Client, w io.Writer, urlStr string) error {
	req, err := http.NewRequest(http.MethodGet, urlStr, nil)
	if err != nil {
		log.WithError(err).Fatal("Failed to formulate HTTP request.")
	}

	resp, err := httpC.Do(req)
	if err != nil {
		return fmt.Errorf("failed to connect to HTTP server: %w", err)
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
