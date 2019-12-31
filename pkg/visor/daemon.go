package visor

import (
	"net"
	"net/rpc"
	"os"
	"os/exec"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	cp "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-peering-daemon/src/apd"
)

var pipeCh = make(chan apd.Packet, 10)

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

func addTransport(logger *logging.Logger) {
	for {
		select {
		case packet := <-pipeCh:
			rpcClient, err := client(packet.IP, time.Second*5, time.Second*60)
			if err != nil {
				logger.Fatal(err)
			}

			publicKey := cp.MustPubKeyFromHex(packet.PublicKey)
			logger.Infof("Establishing transport to remote visor: {%s: %s}", packet.PublicKey, packet.IP)

			_, err = rpcClient.AddTransport(cipher.PubKey(publicKey), dmsg.Type, false, time.Second*30)
			if err != nil {
				logger.Fatal(err)
			}
		}
	}
}
