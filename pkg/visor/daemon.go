package visor

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/SkycoinProject/dmsg/cipher"
	skycoin_cipher "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"
	spd "github.com/SkycoinProject/skywire-peering-daemon/src/daemon"
)

var logger = logging.MustGetLogger("skywire-peering-daemon")

func execute(binPath, publicKey, namedPipe, lAddr string) error {
	cmd := exec.Command(binPath, publicKey, lAddr, namedPipe)
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

// transport establshes a transport to a remote visor
func createTransport(network *snet.Network, networkType string, packet spd.Packet) (*snet.Conn, error) {
	logger.Infof("Establishing transport to remote visor")
	rPK := skycoin_cipher.MustPubKeyFromHex(packet.PublicKey)
	token := strings.Split(packet.IP, ":")
	port, err := strconv.ParseUint(token[1], 0, 64)
	if err != nil {
		return nil, err
	}

	conn, err := network.Dial(context.Background(), networkType, cipher.PubKey(rPK), uint16(port))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
