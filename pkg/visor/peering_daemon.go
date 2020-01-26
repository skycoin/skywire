package visor

import (
	"bytes"
	"fmt"
	"github.com/rjeczalik/notify"
	"io"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	skycoin_cipher "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	spd "github.com/SkycoinProject/skywire-peering-daemon/pkg/daemon"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
)

var (
	rpcAddr         = "localhost:3435"
	logger          = logging.MustGetLogger("SPD")
	rpcDialTimeout  = time.Duration(5 * time.Second)
	rpcConnDuration = time.Duration(60 * time.Second)
)

func execute(binPath, publicKey, namedPipe, lAddr string) error {
	cmd := exec.Command(binPath, publicKey, lAddr, namedPipe)
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

func RpcClient() (RPCClient, error) {
	conn, err := net.DialTimeout("tcp", rpcAddr, rpcDialTimeout)
	if err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	if err := conn.SetDeadline(time.Now().Add(rpcConnDuration)); err != nil {
		logger.Fatal("RPC connection failed:", err)
	}
	return NewRPCClient(rpc.NewClient(conn), RPCPrefix), nil
}

// transport establshes an stcp transport to a remote visor
func createTransport(network *snet.Network, networkType string, packet spd.Packet) (*TransportSummary, error) {
	client, err := RpcClient()
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

func watchNamedPipe(file string, c chan notify.EventInfo, errCh chan error) {
	go func() {
		err := notify.Watch(file, c, notify.Write)
		if err != nil {
			errCh <- err
		}
	}()
}

func readPacket(stdOut *os.File, c chan notify.EventInfo, conf *Config, n *snet.Network) TransportSummary {
	// Read packets from named pipe
	for {
		var (
			packet spd.Packet
			buff   bytes.Buffer
		)

		<- c
		_, err := io.Copy(&buff, stdOut)
		if err != nil {
			logger.Error(err)
		}

		packet, err = spd.Deserialize(buff.Bytes())
		if err != nil {
			logger.Error(err)
		}

		rpk := skycoin_cipher.MustPubKeyFromHex(packet.PublicKey)
		conf.STCP.PubKeyTable[cipher.PubKey(rpk)] = packet.IP
		logger.Infof("Packets received from skywire-peering-daemon:\n\t{%s: %s}", packet.PublicKey, packet.IP)
		tp, err := createTransport(n, snet.STcpType, packet)
		if err != nil {
			logger.Errorf("Couldn't establish transport to remote visor: %s", err)
		}

		logger.Infof("Transport established to remote visor: \n%s", tp.Local)
	}
}
