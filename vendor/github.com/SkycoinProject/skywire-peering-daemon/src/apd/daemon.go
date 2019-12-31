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
}

type APD struct {
	PublicKey string
	LocalIP   string
	PacketMap map[string]string
	DoneCh    chan error
	PacketCh  chan Packet
	Logger    *logging.Logger
	NamedPipe string
}

// NewApd returns an Apd type
func NewApd(pubKey, namedPipe string) *APD {
	return &APD{
		PublicKey: pubKey,
		LocalIP:   getLocalIP(),
		PacketMap: make(map[string]string),
		DoneCh:    make(chan error),
		PacketCh:  make(chan Packet, packetLength),
		Logger:    logger("APD"),
		NamedPipe: namedPipe,
	}
}

// BroadCastPubKey broadcasts a UDP packet which contains a public key
// to the local network's broadcast address.
func (apd *APD) BroadCastPubKey(broadCastIP string, timer *time.Ticker, port int) {
	apd.Logger.Infof("broadcasting on address %s:%d", defaultBroadCastIP, port)
	for range timer.C {
		apd.Logger.Infof("broadcasting public key")
		err := BroadCastPubKey(apd.PublicKey, broadCastIP, port)
		if err != nil {
			apd.Logger.Error(err)
			apd.DoneCh <- err
			return
		}
	}
}

// Listen listens for incoming broadcasts on a local network, and reads incoming UDP broadcasts.
func (apd *APD) Listen(port int) {
	address := fmt.Sprintf(":%d", port)
	udpAddr, err := net.ResolveUDPAddr("udp", address)
	if err != nil {
		apd.Logger.Error(err)
		apd.DoneCh <- err
		return
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		apd.Logger.Error(err)
		apd.DoneCh <- err
		return
	}

	defer conn.Close()
	apd.Logger.Infof("listening on address %s", address)

	for {
		buffer := make([]byte, 1024)
		n, addr, err := conn.ReadFromUDP(buffer)
		if err != nil {
			apd.Logger.Error(err)
			apd.DoneCh <- err
			return
		}

		message := Packet{
			PublicKey: string(buffer[:n]),
			IP:        addr.String(),
		}

		apd.PacketCh <- message
	}
}

// Run starts an auto-peering daemon process in two goroutines.
// The daemon broadcasts a public key in a goroutine, and listens
// for incoming broadcasts in another goroutine.
func (apd *APD) Run() {
	t := time.NewTicker(10 * time.Second)

	apd.Logger.Infof("%s: %s", apd.PublicKey, apd.NamedPipe)

	shutDownCh := make(chan os.Signal)
	signal.Notify(shutDownCh, syscall.SIGTERM, syscall.SIGINT)

	apd.Logger.Info("Auto-peering-daemon started")

	// send broadcasts at ten minute intervals
	go apd.BroadCastPubKey(defaultBroadCastIP, t, port)

	// listen for incoming broadcasts
	go apd.Listen(port)

	go func(timer *time.Ticker) {
		for range timer.C {
			data, _ := serialize(Packet{
				PublicKey: "031b80cd5773143a39d940dc0710b93dcccc262a85108018a7a95ab9af734f8055",
				IP:        "127.0.0.1: 3000",
			})
			err := write(data, apd.NamedPipe)
			if err != nil {
				apd.Logger.Fatalf("Error writing to named pipe: %s", err)
			}
			apd.Logger.Info("Packet sent over pipe")
		}
	}(t)

	for {
		select {
		case <-apd.DoneCh:
			apd.Logger.Fatal("Shutting down daemon")
			os.Exit(1)
		case packet := <-apd.PacketCh:
			apd.RegisterPubKey(packet)
		case <-shutDownCh:
			apd.Logger.Print("Shutting down daemon")
			os.Exit(1)
		}
	}
}

// RegisterPubKey checks if a public key received from a broadcast is already registered.
// It adds only new public keys to a map, and sends the registered packet over a named pipe.
func (apd *APD) RegisterPubKey(packet Packet) {
	if apd.PublicKey != packet.PublicKey {
		if _, ok := apd.PacketMap[packet.PublicKey]; !ok {
			apd.PacketMap[packet.PublicKey] = packet.IP
			apd.Logger.Infof("Received packet %s: %s", packet.PublicKey, packet.IP)
			data, err := serialize(packet)
			if err != nil {
				apd.Logger.Fatalf("Couldn't seralize packet: %s", err)
			}

			err = write(data, apd.NamedPipe)
			if err != nil {
				apd.Logger.Fatalf("Error writing to named pipe: %s", err)
			}
		}
	}
}
