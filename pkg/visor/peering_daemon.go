package visor

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	skycoin_cipher "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	spd "github.com/SkycoinProject/skywire-peering-daemon/pkg/daemon"
	"github.com/rjeczalik/notify"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

var (
	rpcAddr         = "localhost:3435"
	logger          = logging.MustGetLogger("SPD")
	rpcDialTimeout  = time.Duration(5 * time.Second)
	rpcConnDuration = time.Duration(60 * time.Second)
	spdMu           sync.Mutex
)

func execute(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Pdeathsig: syscall.SIGTERM,
	}
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

func client() (RPCClient, error) {
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		return nil, fmt.Errorf("RPC connection failed: %s", err)
	}
	if err := conn.SetDeadline(time.Now().Add(rpcConnDuration)); err != nil {
		return nil, fmt.Errorf("RPC connection failed: %s", err)
	}
	return NewRPCClient(rpc.NewClient(conn), RPCPrefix), nil
}

// transport establshes an stcp transport to a remote visor
func createTransport(networkType string, packet spd.Packet) (*TransportSummary, error) {
	client, err := client()
	if err != nil {
		return nil, err
	}

	logger.Infof("Establishing transport to remote visor")
	rPK := skycoin_cipher.MustPubKeyFromHex(packet.PublicKey)
	tpSummary, err := client.AddTransport(cipher.PubKey(rPK), networkType, true, 0)
	if err != nil {
		return nil, fmt.Errorf("Unable to establish stcp transport: %s", err)
	}

	return tpSummary, nil
}

func watchNamedPipe(file string, c chan notify.EventInfo) error {
	err := notify.Watch(file, c, notify.Write)
	if err != nil {
		return err
	}

	return nil
}

func readPacket(stdOut *os.File, c chan notify.EventInfo, m map[cipher.PubKey]string) TransportSummary {
	// Read packets from named pipe
	for {
		var (
			packet spd.Packet
			buff   bytes.Buffer
		)

		<-c
		_, err := io.Copy(&buff, stdOut)
		if err != nil {
			logger.Error(err)
		}

		packet, err = spd.Deserialize(buff.Bytes())
		if err != nil {
			logger.Error(err)
		}

		spdMu.Lock()
		rpk := skycoin_cipher.MustPubKeyFromHex(packet.PublicKey)
		m[cipher.PubKey(rpk)] = packet.IP
		spdMu.Unlock()

		logger.Infof("Packets received from skywire-peering-daemon:\n\t{%s: %s}", packet.PublicKey, packet.IP)
		tp, err := createTransport(snet.STcpType, packet)
		if err != nil {
			logger.Errorf("Couldn't establish transport to remote visor: %s", err)
		}

		logger.Infof("Transport established to remote visor: \n%s", tp.Local)
	}
}
