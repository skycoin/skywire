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
	port               = 3000
	packetLength       = 10
)

type Packet struct {
	PublicKey string
	IP        string
	T         int64
}

type Daemon struct {
	PublicKey string
	localAddr string
	PacketMap map[string]string
	DoneCh    chan error
	PacketCh  chan []byte
	Logger    *logging.Logger
	NamedPipe string
}

// NewDaemon returns a Daemon type
func NewDaemon(pubKey, rAddr, namedPipe string) *Daemon {
	return &Daemon{
		PublicKey: pubKey,
		localAddr: rAddr,
		PacketMap: make(map[string]string),
		DoneCh:    make(chan error),
		PacketCh:  make(chan []byte),
		Logger:    logger("SPD"),
		NamedPipe: namedPipe,
	}
}

// BroadCastPacket broadcasts a UDP packet which contains a public key
// to the local network's broadcast address.
func (d *Daemon) BroadCastPacket(broadCastIP string, timer *time.Ticker, port int, data []byte) {
	d.Logger.Infof("broadcasting packet on address %s:%d", defaultBroadCastIP, port)
	for range timer.C {
		err := BroadCast(broadCastIP, port, data)
		if err != nil {
			d.Logger.Error(err)
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
		d.Logger.Error(err)
		d.DoneCh <- err
		return
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		d.Logger.Error(err)
		d.DoneCh <- err
		return
	}

	defer conn.Close()
	d.Logger.Infof("listening on address %s", address)

	for {
		buffer := make([]byte, 1024)
		n, _, err := conn.ReadFromUDP(buffer)
		if err != nil {
			d.Logger.Error(err)
			d.DoneCh <- err
			return
		}

		data := buffer[:n]
		if !verifyPacket(d.PublicKey, data) {
			d.Logger.Infof("Packets received: %s", string(buffer[:n]))
			d.PacketCh <- data
		}
	}
}

// Run starts an auto-peering daemon process in two goroutines.
// The daemon broadcasts a public key in a goroutine, and listens
// for incoming broadcasts in another goroutine.
func (d *Daemon) Run() {
	d.Logger.Info("Skywire-peering-daemon started")
	t := time.NewTicker(10 * time.Second)

	shutDownCh := make(chan os.Signal, 1)
	signal.Notify(shutDownCh, syscall.SIGTERM, syscall.SIGINT)

	packet := Packet{
		PublicKey: d.PublicKey,
		IP:        d.localAddr,
	}
	data, err := serialize(packet)
	if err != nil {
		d.Logger.Fatal(err)
	}

	// send broadcasts at ten minute intervals
	go d.BroadCastPacket(defaultBroadCastIP, t, port, data)

	// listen for incoming broadcasts
	go d.Listen(port)

	go func() {
		d.Logger.Info("ASLEEP...")
		time.Sleep(15*time.Second)
		d.Logger.Info("AWAKE...")
		for range t.C {
			d.Logger.Info("Sending...")
			packet = Packet{
				PublicKey: "031b80cd5773143a39d940dc0710b93dcccc262a85108018a7a95ab9af734f8055",
				IP: "127.0.0.1:9498",
			}

			b, _ := serialize(packet)
			write(b, d.NamedPipe)
		}
	}()

	for {
		select {
		case <-d.DoneCh:
			d.Logger.Fatal("Shutting down daemon")
			os.Exit(1)
		case packet := <-d.PacketCh:
			d.RegisterPacket(packet)
		case <-shutDownCh:
			d.Logger.Print("Shutting down daemon")
			os.Exit(1)
		}
	}
}

// RegisterPacket checks if a public key received from a broadcast is already registered.
// It adds only new public keys to a map, and sends the registered packet over a named pipe.
func (d *Daemon) RegisterPacket(data []byte) {
	packet, err := Deserialize(data)
	if err != nil {
		d.Logger.Fatal(err)
	}
	packet.T = time.Now().Unix()

	if d.PublicKey != packet.PublicKey {
		if _, ok := d.PacketMap[packet.PublicKey]; !ok {
			d.PacketMap[packet.PublicKey] = packet.IP

			d.Logger.Infof("Received packet %s: %s", packet.PublicKey, packet.IP)
			data, err := serialize(packet)

			if err != nil {
				d.Logger.Fatalf("Couldn't serialize packet: %s", err)
			}

			err = write(data, d.NamedPipe)
			if err != nil {
				d.Logger.Fatalf("Error writing to named pipe: %s", err)
			}
		}
	}
}
