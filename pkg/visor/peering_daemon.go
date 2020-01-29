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
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	skycoin_cipher "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/rjeczalik/notify"

	spd "github.com/SkycoinProject/skywire-peering-daemon/pkg/daemon"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

var (
	logger          = logging.MustGetLogger("SPD")
	rpcDialTimeout  = time.Duration(5 * time.Second)
	rpcConnDuration = time.Duration(60 * time.Second)
	spdMu           sync.Mutex
)

func execute(cmd *exec.Cmd) error {
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

func client(rpcAddr string) (RPCClient, error) {
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
func createTransport(pubKey string, rpcAddr string) (*TransportSummary, error) {
	client, err := client(rpcAddr)
	if err != nil {
		return nil, err
	}

	logger.Infof("Establishing transport to remote visor")
	rPK := skycoin_cipher.MustPubKeyFromHex(pubKey)
	tpSummary, err := client.AddTransport(cipher.PubKey(rPK), snet.STcpType, true, 0)
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

func readSPDPacket(stdOut *os.File, c chan notify.EventInfo, m map[cipher.PubKey]string, rpcAddr string) {
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
		tp, err := createTransport(packet.PublicKey, rpcAddr)
		if err != nil {
			logger.Errorf("Couldn't establish transport to remote visor: %s", err)
		}

		logger.Infof("Transport established to remote visor: \n%s", tp.Remote)
	}
}
