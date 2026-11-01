// Package commands cmd/apps/skymail/commands/skymail.go c4-app-mail
//
// skymail is the visor's mailbox: SMTP on port 25 over dmsg and skynet,
// kept in a Maildir. It reaches the network through the app client, and
// the visor's mail API reaches it through its control surface.
package commands

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/0magnet/calvin"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/app/launcher"
	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cmdutil"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skymail"
	"github.com/skycoin/skywire/pkg/skymailbridge"
)

// Name is the app's name in the launcher.
const Name = "skymail"

type options struct {
	dir, addr                    string
	maxMessage, maxTotal, maxAge string
}

var opts options

func (o *options) bind(fs *pflag.FlagSet) {
	fs.StringVar(&o.dir, "dir", "mail", "Maildir root, which also holds settings.json")
	fs.StringVar(&o.addr, "addr", "", "also serve the control surface on this TCP address, for a visor that runs this app as a separate process")
	fs.StringVar(&o.maxMessage, "max-message-size", "", "largest message taken, e.g. 10MiB, or none")
	fs.StringVar(&o.maxTotal, "max-total-size", "", "most mail kept in every folder together, e.g. 64MiB, or none")
	fs.StringVar(&o.maxAge, "max-age", "", "age at which mail is deleted, e.g. 7d, or none")
}

func init() {
	opts.bind(RootCmd.Flags())
	launcher.RegisterApp(Name, RunSkymail)
	launcher.RegisterAppFlags(Name, RootCmd.Flags())
}

// RootCmd is the root command for skymail.
var RootCmd = &cobra.Command{
	Use:                   "skymail",
	Short:                 "skywire mailbox application",
	Long:                  calvin.AsciiFont("skymail"),
	SilenceErrors:         true,
	SilenceUsage:          true,
	DisableSuggestions:    true,
	DisableFlagsInUseLine: true,
	Version:               buildinfo.Version(),
	Run: func(_ *cobra.Command, _ []string) {
		if err := run(context.Background(), opts); err != nil {
			cmdutil.Fatal(err)
		}
	},
}

// Execute executes root CLI command.
func Execute() {
	cmdutil.RunRoot(RootCmd)
}

// RunSkymail runs the mailbox. The internal launcher passes the app's
// arguments.
func RunSkymail(ctx context.Context, args []string) error {
	o := opts
	if len(args) > 0 {
		o = options{}
		fs := pflag.NewFlagSet(Name, pflag.ContinueOnError)
		o.bind(fs)
		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("failed to parse flags: %w", err)
		}
	}
	return run(ctx, o)
}

func (o options) limits() (skymail.Limits, error) {
	var l skymail.Limits
	var err error
	if o.maxMessage != "" {
		if l.MaxMessageSize, err = skymail.ParseSize(o.maxMessage); err != nil {
			return l, err
		}
	}
	if o.maxTotal != "" {
		if l.MaxTotalSize, err = skymail.ParseSize(o.maxTotal); err != nil {
			return l, err
		}
	}
	if o.maxAge != "" {
		if l.MaxAge, err = skymail.ParseAge(o.maxAge); err != nil {
			return l, err
		}
	}
	return l, nil
}

func run(ctx context.Context, o options) error {
	appCl := app.NewClient(nil)
	defer appCl.Close()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStarting)
	log := appCl.Log()

	limits, err := o.limits()
	if err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	info, err := appCl.MailHostInfo()
	if err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	if info.PortServed {
		// The host's own mail server owning port 25 is a setup, not a fault.
		appCl.SetStatusOrLog(appserver.AppDetailedStatus(ErrPortServed.Error()))
		return nil
	}

	host := skymail.Host{
		PK: appCl.Config().VisorPK,
		Listen: func(network string) (net.Listener, error) {
			return appCl.Listen(appnet.Type(network), skymail.Port)
		},
		Dialers: map[string]skymailbridge.Dialer{
			".skynet": appDialer{cl: appCl, net: appnet.TypeSkynet},
			".dmsg":   appDialer{cl: appCl, net: appnet.TypeDmsg},
		},
		PeerPK: func(c net.Conn) (cipher.PubKey, bool) {
			a, ok := c.RemoteAddr().(appnet.Addr)
			return a.PubKey, ok
		},
		Peers: func() ([]cipher.PubKey, []cipher.PubKey) {
			info, err := appCl.MailHostInfo()
			if err != nil {
				log.WithError(err).Warn("skymail: no hypervisor relation from the visor")
			}
			return info.Managed, info.Hypervisors
		},
		OnDeliver: func(d skymail.Delivered) { notify(appCl, d) },
		Log:       log,
	}
	if offline {
		host.Sealer = appSealer{appCl}
	}
	svc, err := skymail.StartService(ctx, skymail.ServiceConfig{
		Host: host, Dir: o.dir, Base: limits, Outbox: offline,
	})
	if err != nil {
		appCl.SetErrorOrLog(err)
		return err
	}
	defer svc.Close()

	h := svc.Handler("")
	launcher.RegisterHTTPHandler(Name, h)
	defer launcher.ClearHTTPHandler(Name, h)
	if o.addr != "" {
		srv, err := serveTCP(o, svc)
		if err != nil {
			appCl.SetErrorOrLog(err)
			return err
		}
		defer srv.Close() //nolint:errcheck
	}

	appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)
	<-ctx.Done()
	appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
	return nil
}

// ErrPortServed is why the mailbox stays down when `serve add 25` gives
// port 25 to the host's own mail server.
var ErrPortServed = errors.New("port 25 is served by the host's mail server (`serve add 25`)")

// serveTCP serves the control surface on o.addr, behind a token it writes
// beside the mail for the visor to read.
func serveTCP(o options, svc *skymail.Service) (*http.Server, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(raw)
	if err := os.MkdirAll(o.dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(o.dir, skymail.TokenFile), []byte(token), 0o600); err != nil {
		return nil, err
	}
	l, err := net.Listen("tcp", o.addr)
	if err != nil {
		return nil, fmt.Errorf("control surface on %s: %w", o.addr, err)
	}
	srv := &http.Server{Handler: svc.Handler(token), ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(l) //nolint:errcheck // ends with Close
	return srv, nil
}

// appDialer reaches a peer's port 25 over one network and no other: the
// address suffix is the sender's choice, so a failed route is reported,
// not quietly replaced with dmsg.
type appDialer struct {
	cl  *app.Client
	net appnet.Type
}

func (d appDialer) Dial(ctx context.Context, peer cipher.PubKey, port uint16) (net.Conn, error) {
	type result struct {
		c   net.Conn
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := d.cl.Dial(appnet.Addr{Net: d.net, PubKey: peer, Port: routing.Port(port)})
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		return r.c, r.err
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.c != nil {
				_ = r.c.Close() //nolint:errcheck
			}
		}()
		return nil, ctx.Err()
	}
}

// appSealer seals and opens with the visor's key, which stays in the visor.
type appSealer struct{ cl *app.Client }

func (s appSealer) Seal(msg []byte, rcpt cipher.PubKey) ([]byte, error) {
	return s.cl.MailSeal(msg, rcpt)
}

func (s appSealer) Open(body []byte) ([]byte, cipher.PubKey, error) {
	return s.cl.MailOpen(body)
}
