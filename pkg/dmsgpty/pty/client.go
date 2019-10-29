package pty

import (
	"context"
	"io"
	"net/rpc"
	"os"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/creack/pty"
	"github.com/sirupsen/logrus"
)

var empty = &struct{}{}

type Client struct {
	ctx  context.Context
	log  logrus.FieldLogger
	rpcC *rpc.Client
}

func NewPtyClient(ctx context.Context, log logrus.FieldLogger, conn io.ReadWriteCloser) *Client {
	if log == nil {
		log = logging.MustGetLogger("dmsgpty-client")
	}
	return &Client{
		ctx:  ctx,
		log:  log,
		rpcC: rpc.NewClient(conn),
	}
}

func NewPtyClientWithTp(log logrus.FieldLogger, _ cipher.SecKey, tp *dmsg.Transport) (*Client, error) {
	if log == nil {
		log = logging.MustGetLogger("dmsgpty-client")
	}

	// TODO(evanlinjin): Wrap connection with noise.
	//ns, err := noise.New(noise.HandshakeXK, noise.Config{
	//	LocalPK:   tp.LocalPK(),
	//	LocalSK:   sk,
	//	RemotePK:  tp.RemotePK(),
	//	Initiator: true,
	//})
	//if err != nil {
	//	log.WithError(err).Fatal("NewPtyClientWithTp: failed to init noise")
	//	return nil, err
	//}
	//conn, err := noise.WrapConn(tp, ns, noise.AcceptHandshakeTimeout)
	//if err != nil {
	//	return nil, err
	//}

	return &Client{
		log:  log,
		rpcC: rpc.NewClient(tp),
	}, nil
}

func (c *Client) Close() error {
	if err := c.Stop(); err != nil {
		c.log.WithError(err).Warn("failed to stop remote pty")
	}
	return c.rpcC.Close()
}

func (c *Client) Start(name string, arg ...string) error {
	size, err := pty.GetsizeFull(os.Stdin)
	if err != nil {
		c.log.WithError(err).Warn("failed to obtain terminal size")
		size = nil
	}
	return c.call("Start", &CommandReq{Name: name, Arg: arg, Size: size}, empty)
}

func (c *Client) Stop() error {
	return c.call("Stop", empty, empty)
}

func (c *Client) Read(b []byte) (int, error) {
	reqN := len(b)
	var respB []byte
	err := c.call("Read", &reqN, &respB)
	return copy(b, respB), processRPCError(err)
}

func (c *Client) Write(b []byte) (int, error) {
	var n int
	err := c.call("Write", &b, &n)
	return n, processRPCError(err)
}

func (c *Client) SetPtySize(size *pty.Winsize) error {
	return c.call("SetPtySize", size, empty)
}

func (c *Client) call(method string, args, reply interface{}) error {
	call := c.rpcC.Go(ptyMethod(method), args, reply, nil)
	select {
	case <-c.ctx.Done():
		return c.ctx.Err()
	case <-call.Done:
		return call.Error
	}
}

func ptyMethod(m string) string {
	return GatewayName + "." + m
}

func GetPtySize(t *os.File) (*pty.Winsize, error) { return pty.GetsizeFull(t) }

func processRPCError(err error) error {
	if err != nil {
		switch err.Error() {
		case io.EOF.Error():
			return io.EOF
		case io.ErrUnexpectedEOF.Error():
			return io.ErrUnexpectedEOF
		case io.ErrClosedPipe.Error():
			return io.ErrClosedPipe
		case io.ErrNoProgress.Error():
			return io.ErrNoProgress
		case io.ErrShortBuffer.Error():
			return io.ErrShortBuffer
		case io.ErrShortWrite.Error():
			return io.ErrShortWrite
		default:
			return err
		}
	}
	return nil
}
