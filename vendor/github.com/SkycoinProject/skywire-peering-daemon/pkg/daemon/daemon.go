package apd

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const (
	defaultBroadCastIP = "255.255.255.255"
	port               = 4000
	packetLength       = 10
)

// Packet defines a packet type
type Packet struct {
	PublicKey string
	IP        string
	T         int64
}

// Config defines configuration parameters for daemon
type Config struct {
	PubKey    string
	LocalAddr string
	NamedPipe string
}

// Daemon provides configuration parameters for a
// skywire-peering-skywire-peering-daemon.
type Daemon struct {
	conf      *Config
	PacketMap map[string]string
	DoneCh    chan error
	PacketCh  chan []byte
	logger    *logging.Logger
}

// NewDaemon returns a Daemon type
func NewDaemon(conf *Config) *Daemon {
	return &Daemon{
		conf:      conf,
		PacketMap: make(map[string]string),
		DoneCh:    make(chan error),
		PacketCh:  make(chan []byte, packetLength),
		logger:    logger("SPD"),
	}
}

// BroadCastPacket broadcasts a UDP packet which contains a public key
// to the local network's broadcast address.
func (d *Daemon) BroadCastPacket(broadCastIP string, timer *time.Ticker, port int, data []byte) {
	d.logger.Infof("Broadcasting packet on address %s:%d", defaultBroadCastIP, port)
	for range timer.C {
		err := BroadCast(broadCastIP, port, data)
		if err != nil {
			d.logger.Error(err)
			d.DoneCh <- err
			return
		}
	}
}

// Listen listens for incoming broadcasts on a local network, and reads incoming UDP broadcasts.
func (d *Daemon) Listen(port int) {
	address := fmt.Sprintf(":%d", port)
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		d.logger.Error(err)
		d.DoneCh <- err
		return
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		d.logger.Error(err)
		d.DoneCh <- err
		return
	}

	defer func() {
		err := conn.Close()
		if err != nil {
			d.logger.WithError(err)
		}
	}()

	d.logger.Infof("Listening on address %s", address)

	for {
		buffer := make([]byte, 1024)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			d.logger.Error(err)
			d.DoneCh <- err
			return
		}

		data := buffer[:n]
		if !verifyPacket(d.conf.PubKey, data) {
			d.PacketCh <- data
		}
	}
}

// Run starts an auto-peering skywire-peering-daemon process in two goroutines.
// The skywire-peering-daemon broadcasts a public key in a goroutine, and listens
// for incoming broadcasts in another goroutine.
func (d *Daemon) Run() {
	d.logger.Info("Skywire-peering-daemon started...")
	t := time.NewTicker(10 * time.Second)

	shutDownCh := make(chan os.Signal, 1)
	signal.Notify(shutDownCh, syscall.SIGTERM, syscall.SIGINT)

	packet := Packet{
		PublicKey: d.conf.PubKey,
		IP:        d.conf.LocalAddr,
	}
	data, err := serialize(packet)
	if err != nil {
		d.logger.Fatal(err)
	}

	// send broadcasts at ten minute intervals
	go d.BroadCastPacket(defaultBroadCastIP, t, port, data)

	// listen for incoming broadcasts
	go d.Listen(port)

	for {
		select {
		case <-d.DoneCh:
			d.logger.Fatal("Shutting down skywire-peering-daemon")
			os.Exit(1)
		case packet := <-d.PacketCh:
			d.RegisterPacket(packet)
		case <-shutDownCh:
			d.logger.Print("Shutting down skywire-peering-daemon")
			os.Exit(1)
		}
	}
}

// RegisterPacket checks if a public key received from a broadcast is already registered.
// It adds only new public keys to a map, and sends the registered packet over a named pipe.
func (d *Daemon) RegisterPacket(data []byte) {
	packet, err := Deserialize(data)
	if err != nil {
		d.logger.Fatal(err)
	}
	packet.T = time.Now().Unix()

	if d.conf.PubKey != packet.PublicKey {
		if _, ok := d.PacketMap[packet.PublicKey]; !ok {
			d.PacketMap[packet.PublicKey] = packet.IP

			d.logger.Infof("Received packet %s: %s", packet.PublicKey, packet.IP)
			data, err := serialize(packet)

			if err != nil {
				d.logger.Fatalf("Couldn't serialize packet: %s", err)
			}

			err = write(data, d.conf.NamedPipe)
			if err != nil {
				d.logger.Fatalf("Error writing to named pipe: %s", err)
			}
		}
	}
}
