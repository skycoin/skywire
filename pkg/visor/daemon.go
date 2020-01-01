package visor

import (
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/SkycoinProject/dmsg/cipher"
	cp "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-peering-daemon/src/apd"
)

var (
	//pipeCh = make(chan apd.Packet, 10)
	logger = logging.MustGetLogger("skywire-peering-daemon")
)

func client(rpcAddr string, dialTimeout, connDuration time.Duration) (RPCClient, error) {
	conn, err := net.DialTimeout("tcp", rpcAddr, dialTimeout)
	if err != nil {
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(connDuration)); err != nil {
		return nil, err
	}

	return NewRPCClient(rpc.NewClient(conn), RPCPrefix), nil
}

func execute(binPath, publicKey, namedPipe string) error {
	cmd := exec.Command(binPath, publicKey, namedPipe)
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

func addTransport(network *snet.Network, networkType string, packet apd.Packet) (*snet.Conn, error) {
	logger.Infof("Establishing transport to remote visor: {%s: %s}", packet.PublicKey, packet.IP)
	publicKey := cp.MustPubKeyFromHex(packet.PublicKey)
	token := strings.Split(packet.IP, ":")
	port, err := strconv.ParseUint(token[1], 0, 64)
	if err != nil {
		return nil, err
	}

	conn, err := network.Dial(networkType, cipher.PubKey(publicKey), uint16(port))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
