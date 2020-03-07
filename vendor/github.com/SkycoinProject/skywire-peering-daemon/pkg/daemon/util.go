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

const moduleName = "SPD"

// BroadCast broadcasts a UDP packet containing the public key of the local visor.
// Broadcasts is sent on the local network broadcasts address.
func BroadCast(broadCastIP string, port int, data []byte) error {
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

	defer func() {
		err := conn.Close()
		if err != nil {
			logger(moduleName).WithError(err)
		}
	}()

	_, err = conn.Write(data)
	if err != nil {
		return err
	}

	return nil
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

	_, err = stdOut.Write(data)
	if err != nil {
		logger(moduleName).Fatal(err)
	}

	err = stdOut.Close()
	if err != nil {
		logger(moduleName).Fatal(err)
	}

	return nil
}

// Deserialize decodes a byte to a packet type
func Deserialize(data []byte) (Packet, error) {
	var packet Packet
	decoder := gob.NewDecoder(bytes.NewReader(data))
	err := decoder.Decode(&packet)
	if err != nil {
		return Packet{}, err
	}

	return packet, nil
}

// verifyPacket checks if packet received is sent from local daemon
func verifyPacket(pubKey string, data []byte) bool {
	packet, err := Deserialize(data)
	if err != nil {
		logger(moduleName).Fatalf("Couldn't serialize packet: %s", err)
	}

	return packet.PublicKey == pubKey
}
