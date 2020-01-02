package visor

import (
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"

	"github.com/SkycoinProject/dmsg/cipher"
	cp "github.com/SkycoinProject/skycoin/src/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	apd "github.com/SkycoinProject/skywire-peering-daemon/src/daemon"
)

var logger = logging.MustGetLogger("skywire-peering-daemon")

func execute(binPath, publicKey, namedPipe string) error {
	cmd := exec.Command(binPath, publicKey, namedPipe)
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}

	return nil
}

// transport establshes a transport to a remote visor
func createTransport(network *snet.Network, networkType string, packet apd.Packet) (*snet.Conn, error) {
	logger.Infof("Establishing transport to remote visor: {%s: %s}", packet.PublicKey, packet.IP)
	rPK := cp.MustPubKeyFromHex(packet.PublicKey)
	token := strings.Split(packet.IP, ":")
	port, err := strconv.ParseUint(token[1], 0, 64)
	if err != nil {
		return nil, err
	}

	conn, err := network.Dial(networkType, cipher.PubKey(rPK), uint16(port))
	if err != nil {
		return nil, err
	}

	return conn, nil
}
