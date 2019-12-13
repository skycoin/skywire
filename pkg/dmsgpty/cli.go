package dmsgpty

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh/terminal"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/pty"
)

// CLI represents the command line interface for communicating with the dmsgpty-host.
type CLI struct {
	Log logrus.FieldLogger `json:"-"`

	Net  string `json:"cli_network"`
	Addr string `json:"cli_address"`

	DstPK   cipher.PubKey `json:"destination_public_key"`
	DstPort uint16        `json:"destination_port"`

	Cmd string   `json:"command_name"`
	Arg []string `json:"command_arguments"`
}

// SetDefaults sets the fields with nil-values to default values.
func (c *CLI) SetDefaults() {
	if c.Log == nil {
		c.Log = logging.MustGetLogger("dmsgpty-cli")
	}
	if c.Net == "" {
		c.Net = skyenv.DefaultDmsgPtyCLINet
	}
	if c.Addr == "" {
		c.Addr = skyenv.DefaultDmsgPtyCLIAddr
	}
	if c.DstPort == 0 {
		c.DstPort = skyenv.DefaultDmsgPtyPort
	}
	if c.Cmd == "" {
		c.Cmd = "/bin/bash"
	}
}

// dials to the dmsgpty-host with a given request.
func (c *CLI) dial(req Request) (net.Conn, error) {
	conn, err := net.Dial(c.Net, c.Addr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to dmsgpty-server: %v", err)
	}

	c.Log.
		WithField("request", req).
		Info("requesting")

	if err := WriteRequest(conn, req); err != nil {
		return nil, fmt.Errorf("request failed: %v", err)
	}

	return conn, nil
}

// RequestCfg dials a request of type 'Cfg'.
func (c *CLI) RequestCfg() (net.Conn, error) {
	return c.dial(new(CfgReq))
}

// RequestPty dials a request of type 'Pty'.
func (c *CLI) RequestPty() error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := c.dial(&PtyReq{
		Version: Version,
		DstPK:   c.DstPK,
		DstPort: c.DstPort,
	})
	if err != nil {
		return err
	}

	ptyC := pty.NewPtyClient(ctx, c.Log, conn)

	c.Log.
		WithField("cmd", fmt.Sprint(append([]string{c.Cmd}, c.Arg...))).
		Info("executing")

	// Set stdin to raw mode.
	oldState, err := terminal.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		c.Log.WithError(err).Warn("failed to set stdin to raw mode")
	} else {
		defer func() {
			// Attempt to restore state.
			if err := terminal.Restore(int(os.Stdin.Fd()), oldState); err != nil {
				c.Log.
					WithError(err).
					Error("failed to restore stdin state")
			}
		}()
	}

	if err := ptyC.Start(c.Cmd, c.Arg...); err != nil {
		return fmt.Errorf("failed to start command on remote pty: %v", err)
	}

	// Window resize loop.
	go func() {
		defer cancel()
		if err := c.ptyResizeLoop(ctx, ptyC); err != nil {
			c.Log.
				WithError(err).
				Debug("window resize loop closed with error")
		}
	}()

	// Write loop.
	go func() {
		defer cancel()
		_, _ = io.Copy(ptyC, os.Stdin) //nolint:errcheck
	}()

	// Read loop.
	if _, err := io.Copy(os.Stdout, ptyC); err != nil {
		c.Log.
			WithError(err).
			Error("read loop closed with error")
	}

	return nil
}

// Loop that informs the remote of changes to the local CLI terminal window size.
func (c *CLI) ptyResizeLoop(ctx context.Context, ptyC *pty.Client) error {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ch:
			winSize, err := pty.GetPtySize(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to obtain window size: %v", err)
			}
			if err := ptyC.SetPtySize(winSize); err != nil {
				return fmt.Errorf("failed to set remote window size: %v", err)
			}
		}
	}
}
