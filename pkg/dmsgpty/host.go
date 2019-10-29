package dmsgpty

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"sync"
	"sync/atomic"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/disc"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/sirupsen/logrus"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/pty"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/ptycfg"
	"github.com/SkycoinProject/skywire-mainnet/pkg/dmsgpty/ptyutil"
)

type HostConfig struct {
	PubKey cipher.PubKey `json:"public_key"`
	SecKey cipher.SecKey `json:"secret_key"`

	DmsgDiscAddr string `json:"dmsg_discovery_address"`
	DmsgMinSrv   int    `json:"dmsg_minimum_servers"`
	DmsgPort     uint16 `json:"dmsg_port"` // port to listen on

	AuthFile string `json:"authorization_file"`

	CLINet  string `json:"cli_network"`
	CLIAddr string `json:"cli_address"`
}

func (c *HostConfig) SetDefaults() {
	if c.DmsgDiscAddr == "" {
		c.DmsgDiscAddr = skyenv.DefaultDmsgDiscAddr
	}
	if c.DmsgMinSrv == 0 {
		c.DmsgMinSrv = 1
	}
	if c.DmsgPort == 0 {
		c.DmsgPort = skyenv.DefaultDmsgPtyPort
	}
	if c.AuthFile == "" {
		c.AuthFile = ptyutil.DefaultAuthPath()
	}
	if c.CLINet == "" {
		c.CLINet = skyenv.DefaultDmsgPtyCLINet
	}
	if c.CLIAddr == "" {
		c.CLIAddr = skyenv.DefaultDmsgPtyCLIAddr
	}
}

// Host hosts a dmsgpty server.
// TODO(evanlinjin): Change this to use `snet.Network` instead of `dmsg.Client` directly.
type Host struct {
	log  logrus.FieldLogger

	pk      cipher.PubKey
	cliNet  string
	cliAddr string

	dmsgC *dmsg.Client   // Communicates with other 'ptycli.Host's.
	dmsgL *dmsg.Listener //
	ptyS  *pty.Server    // Access to local ptys.

	cliL net.Listener // Listens for CLI connections.
	cliI int32        // CLI index.

	done chan struct{}
	once sync.Once
}

func NewHost(ctx context.Context, log logrus.FieldLogger, conf HostConfig) (*Host, error) {
	conf.SetDefaults()

	dmsgC := dmsg.NewClient(
		conf.PubKey,
		conf.SecKey,
		disc.NewHTTP(conf.DmsgDiscAddr),
		dmsg.SetLogger(logging.MustGetLogger("dmsg-client")))

	if err := dmsgC.InitiateServerConnections(ctx, conf.DmsgMinSrv); err != nil {
		return nil, err
	}

	return NewHostFromDmsgClient(
		log,
		dmsgC,
		conf.PubKey,
		conf.SecKey,
		conf.AuthFile,
		conf.DmsgPort,
		conf.CLINet,
		conf.CLIAddr)
}

func NewHostFromDmsgClient(
	log logrus.FieldLogger,
	dmsgC *dmsg.Client,
	pk cipher.PubKey,
	sk cipher.SecKey,
	authFile string,
	dmsgPort uint16,
	cliNet, cliAddr string,
) (*Host, error) {

	if log == nil {
		log = logging.MustGetLogger("ptycli-host")
	}
	dmsgL, err := dmsgC.Listen(dmsgPort)
	if err != nil {
		return nil, err
	}
	ptyS, err := pty.NewServer(
		logging.MustGetLogger("dmsgpty-server"),
		pk,
		sk,
		authFile)
	if err != nil {
		return nil, err
	}
	cliL, err := net.Listen(cliNet, cliAddr)
	if err != nil {
		return nil, err
	}
	return &Host{
		log:   log,
		pk: pk,
		cliNet: cliNet,
		cliAddr: cliAddr,
		dmsgC: dmsgC,
		dmsgL: dmsgL,
		ptyS:  ptyS,
		cliL:  cliL,
	}, nil
}

// ServeRemoteRequests serves remote requests.
func (h *Host) ServeRemoteRequests(ctx context.Context) {
	go func() {
		<-ctx.Done()
		err := h.dmsgL.Close()
		h.log.WithError(err).Info("dmsg listener closed")
	}()
	h.ptyS.Serve(ctx, h.dmsgL)
}

// ServeCLIRequests serves local requests from CLI.
func (h *Host) ServeCLIRequests(ctx context.Context) {
	wg := new(sync.WaitGroup)
	defer func() {
		wg.Wait()
		h.cleanup()
	}()

	go func() {
		<-ctx.Done()
		err := h.cliL.Close()
		h.log.WithError(err).Info("CLI listener closed")
	}()

	for {
		conn, err := h.cliL.Accept()
		if err != nil {
			log := h.log.WithError(err)
			if err, ok := err.(net.Error); ok && err.Temporary() {
				log.Warn("failed with temporary error, continuing...")
				continue
			}
			if err == io.ErrClosedPipe {
				log.Info("ServeCLIRequests closed cleanly")
				return
			}
			log.Error("failed with permanent error")
			return
		}

		wg.Add(1)
		go func() {
			defer wg.Done()
			h.handleCLIConn(ctx, conn)
		}()
	}
}

func (h *Host) handleCLIConn(ctx context.Context, cliConn net.Conn) {
	log := h.log.WithField("cli_i", atomic.AddInt32(&h.cliI, 1))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	defer func() {
		log.WithError(cliConn.Close()).
			Info("closed CLI conn")
	}()

	req, err := ReadRequest(cliConn)
	if err != nil {
		log.WithError(err).
			Error("failed to initiate CLI conn")
		return
	}

	log = log.WithField("req_type", req.Type())
	log.Info()

	var rpcSrv *rpc.Server

	switch req.Type() {
	case CfgReqType:
		rpcSrv, err = h.handleCfgReq(ctx, log)
	case PtyReqType:
		rpcSrv, err = h.handlePtyReq(ctx, log, req.(*PtyReq))
	}

	if err != nil {
		log.WithError(err).Error("request failed")
		return
	}
	rpcSrv.ServeConn(cliConn)
}

func (h *Host) handleCfgReq(ctx context.Context, log logrus.FieldLogger) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	if err := rpcS.RegisterName(ptycfg.GatewayName, ptycfg.NewGateway(ctx, h.ptyS.Auth())); err != nil {
		return nil, fmt.Errorf("failed to register 'CfgGateway': %v", err)
	}
	return rpcS, nil
}

func (h *Host) handlePtyReq(ctx context.Context, log logrus.FieldLogger, req *PtyReq) (*rpc.Server, error) {

	var dialLocalPty = func() (*rpc.Server, error) {
		rpcS := rpc.NewServer()
		if err := rpcS.RegisterName(pty.GatewayName, pty.NewDirectGateway()); err != nil {
			return nil, fmt.Errorf("failed to register 'DirectGateway': %v", err)
		}
		return rpcS, nil
	}

	var dialRemotePty = func(ctx context.Context, data *PtyReq) (net.Conn, *rpc.Server, error) {
		dmsgConn, err := h.dmsgC.Dial(ctx, data.DstPK, data.DstPort)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to dial dmsg: %v", err)
		}
		gateway := pty.NewProxyGateway(
			pty.NewPtyClient(ctx, logging.MustGetLogger("pty_client"), dmsgConn))

		rpcS := rpc.NewServer()
		if err := rpcS.RegisterName(pty.GatewayName, gateway); err != nil {
			return nil, nil, fmt.Errorf("failed to register 'DirectGateway': %v", err)
		}
		return dmsgConn, rpcS, nil
	}

	log = log.WithField("dst_pk", req.DstPK)
	log.Info("initiated new CLI conn")

	// If pk is null or == local pk, dial local pty.
	// Otherwise, dial remote pty.

	if req.DstPK.Null() || req.DstPK == h.pk {
		log.Info("opening local pty session")
		return dialLocalPty()
	}

	log.Info("opening remote pty session")
	var dmsgConn net.Conn
	dmsgConn, rpcSrv, err := dialRemotePty(ctx, req)
	if err != nil {
		return nil, err
	}
	go func() {
		<-ctx.Done()
		log.WithError(dmsgConn.Close()).
			Info("dmsg conn closed due to context")
	}()
	return rpcSrv, err
}

func (h *Host) cleanup() {
	// close unix file.
	if h.cliNet == "unix" {
		h.log.
			WithError(os.Remove(h.cliAddr)).
			Debug("deleted unix file")
	}
}
