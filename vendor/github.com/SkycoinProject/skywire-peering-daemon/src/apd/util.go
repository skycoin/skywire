package apd

import (
	"bytes"
	"encoding/gob"
	"fmt"
	"net"
	"os"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

var logger = func(moduleName string) *logging.Logger {
	masterLogger := logging.NewMasterLogger()
	return masterLogger.PackageLogger(moduleName)
}

const moduleName = "apd.broadcast"

// BroadCastPubKey broadcasts a UDP packet containing the public key of the local visor.
// Broadcasts is sent on the local network broadcasts address.
func BroadCastPubKey(pubkey, broadCastIP string, port int) error {
	address := fmt.Sprintf("%s:%d", broadCastIP, port)
	bAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		logger(moduleName).Errorf("Couldn't resolve broadcast address: %v", err)
		return err
	}

	conn, err := net.DialUDP("udp", nil, bAddr)
	if err != nil {
		return err
	}

	defer conn.Close()

	packet := []byte(pubkey)
	_, err = conn.Write(packet)
	if err != nil {
		return err
	}

	return nil
}

func getLocalIP() string {
	var localIP string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		logger(moduleName).Errorf("Couldn't get device unicast addresses: %v", err)
		return ""
	}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil {
				localIP = ipnet.IP.String()
			}
		}
	}
	return localIP
}

func Deserialize(data []byte) (Packet, error) {
	var packet Packet
	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&packet)
	if err != nil {
		return Packet{}, err
	}

	return packet, nil
}

func serialize(packet Packet) ([]byte, error) {
	var buff bytes.Buffer
	decoder := gob.NewEncoder(&buff)
	err := decoder.Encode(packet)
	if err != nil {
		return nil, err
	}

	return buff.Bytes(), nil
}

func write(data []byte, filePath string) error {
	logger(moduleName).Info("Sending packet over pipe")
	stdOut, err := os.OpenFile(filePath, os.O_RDWR, 0600)
	if err != nil {
		return err
	}

	stdOut.Write(data)
	stdOut.Close()

	return nil
}
