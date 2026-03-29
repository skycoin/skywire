// Package visor pkg/visor/init_lan_dmsg.go
package visor

import (
	"fmt"
	"net"
	"strings"

	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// LANDmsgServer holds the state of an embedded LAN DMSG server.
type LANDmsgServer struct {
	Server  *dmsg.Server
	PK      cipher.PubKey
	SK      cipher.SecKey
	Address string // "ip:port" the server is listening on (LAN-routable)
	Port    int    // actual port (useful when port 0 was configured)
}

// startLANDmsgServer creates and starts an embedded DMSG server for LAN visors.
// It uses direct.NewClient as the discovery backend (in-memory, no public registration).
// The server keypair is stored in the config and auto-generated on first use.
// Port 0 is supported — the OS picks a random available port.
func startLANDmsgServer(conf *visorconfig.LANDmsgServerConf, visorConf *visorconfig.V1, masterLogger *logging.MasterLogger) (*LANDmsgServer, error) {
	log := masterLogger.PackageLogger("lan_dmsg_server")

	maxSessions := conf.MaxSessions
	if maxSessions == 0 {
		maxSessions = 100
	}

	// Use keypair from config, or generate and save to config
	pk := conf.PK
	sk := conf.SK
	if pk.Null() || sk.Null() {
		pk, sk = cipher.GenerateKeyPair()
		conf.PK = pk
		conf.SK = sk
		// Persist the generated keypair to the config file
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Warnf("Could not persist LAN DMSG server keypair to config: %v", r)
				}
			}()
			if err := visorConf.Flush(); err != nil {
				log.WithError(err).Warn("Failed to save generated LAN DMSG server keypair to config")
			} else {
				log.Info("Generated and saved LAN DMSG server keypair to config")
			}
		}()
	}

	log.Infof("LAN DMSG server PK: %s", pk.String())

	// Listen first so we know the actual port (important when port is 0)
	listenAddr := fmt.Sprintf(":%d", conf.Port)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", listenAddr, err)
	}

	// Extract actual port
	_, portStr, _ := net.SplitHostPort(lis.Addr().String())
	actualPort := 0
	fmt.Sscanf(portStr, "%d", &actualPort) //nolint:errcheck

	// Determine LAN-routable address
	lanIP := getLANIP(log)
	advertisedAddr := fmt.Sprintf("%s:%d", lanIP, actualPort)

	log.Infof("LAN DMSG server listening on :%d, advertised as %s", actualPort, advertisedAddr)

	// Create a direct (in-memory) discovery client for the server
	serverEntry := &dmsgdisc.Entry{
		Static: pk,
		Server: &dmsgdisc.Server{
			Address:           advertisedAddr,
			AvailableSessions: maxSessions,
		},
	}
	dc := direct.NewClient([]*dmsgdisc.Entry{serverEntry}, log)

	// Create and start the server
	serverConf := &dmsg.ServerConfig{
		MaxSessions: maxSessions,
	}
	server := dmsg.NewServer(pk, sk, dc, serverConf, nil)
	server.SetLogger(log)

	go func() {
		if err := server.Serve(lis, advertisedAddr); err != nil {
			log.WithError(err).Error("LAN DMSG server stopped")
		}
	}()

	return &LANDmsgServer{
		Server:  server,
		PK:      pk,
		SK:      sk,
		Address: advertisedAddr,
		Port:    actualPort,
	}, nil
}

// getLANIP returns the first non-loopback private IPv4 address.
func getLANIP(log *logging.Logger) string {
	addrs, err := netutil.LocalAddresses()
	if err != nil {
		log.WithError(err).Warn("Failed to get local addresses, using 127.0.0.1")
		return "127.0.0.1"
	}
	for _, addr := range addrs {
		if addr == "127.0.0.1" || addr == "::1" || strings.Contains(addr, ":") {
			continue
		}
		ip := net.ParseIP(addr)
		if ip != nil && ip.IsPrivate() {
			return addr
		}
	}
	for _, addr := range addrs {
		if addr != "127.0.0.1" && addr != "::1" && !strings.Contains(addr, ":") {
			return addr
		}
	}
	return "127.0.0.1"
}
