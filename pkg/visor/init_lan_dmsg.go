// Package visor pkg/visor/init_lan_dmsg.go
package visor

import (
	"encoding/json"
	"fmt"
	"net"
	"os"

	"github.com/skycoin/dmsg/pkg/direct"
	dmsgdisc "github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// LANDmsgServer holds the state of an embedded LAN DMSG server.
type LANDmsgServer struct {
	Server  *dmsg.Server
	PK      cipher.PubKey
	SK      cipher.SecKey
	Address string // "ip:port" the server is listening on
}

// lanDmsgKeyPair is the persisted server keypair.
type lanDmsgKeyPair struct {
	PK string `json:"pk"`
	SK string `json:"sk"`
}

// startLANDmsgServer creates and starts an embedded DMSG server for LAN visors.
// It uses direct.NewClient as the discovery backend (in-memory, no public registration).
// The server keypair is persisted to a file so it remains stable across restarts.
func startLANDmsgServer(conf *visorconfig.LANDmsgServerConf, masterLogger *logging.MasterLogger) (*LANDmsgServer, error) {
	log := masterLogger.PackageLogger("lan_dmsg_server")

	// Defaults
	port := conf.Port
	if port == 0 {
		port = 8085
	}
	maxSessions := conf.MaxSessions
	if maxSessions == 0 {
		maxSessions = 100
	}
	keyFile := conf.KeyFile
	if keyFile == "" {
		keyFile = "lan_dmsg_server.json"
	}

	// Load or generate server keypair
	pk, sk, err := loadOrGenerateKeyPair(keyFile, log)
	if err != nil {
		return nil, fmt.Errorf("keypair: %w", err)
	}

	log.Infof("LAN DMSG server PK: %s", pk.String())

	// Create a direct (in-memory) discovery client for the server.
	// This absorbs all registration calls without contacting public discovery.
	serverEntry := &dmsgdisc.Entry{
		Static: pk,
		Server: &dmsgdisc.Server{
			Address:           fmt.Sprintf(":%d", port),
			AvailableSessions: maxSessions,
		},
	}
	dc := direct.NewClient([]*dmsgdisc.Entry{serverEntry}, log)

	// Create the server
	serverConf := &dmsg.ServerConfig{
		MaxSessions: maxSessions,
	}
	server := dmsg.NewServer(pk, sk, dc, serverConf, nil)
	server.SetLogger(log)

	// Listen on all interfaces
	listenAddr := fmt.Sprintf(":%d", port)
	lis, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", listenAddr, err)
	}

	// Determine the advertised address (LAN IP + port)
	advertisedAddr := lis.Addr().String()
	log.Infof("LAN DMSG server listening on %s", advertisedAddr)

	// Start serving in background
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
	}, nil
}

// loadOrGenerateKeyPair loads a keypair from a JSON file, or generates and saves a new one.
func loadOrGenerateKeyPair(path string, log *logging.Logger) (cipher.PubKey, cipher.SecKey, error) { //nolint:unparam
	data, err := os.ReadFile(path) //nolint:gosec
	if err == nil {
		var kp lanDmsgKeyPair
		if err := json.Unmarshal(data, &kp); err == nil && kp.PK != "" && kp.SK != "" {
			var pk cipher.PubKey
			var sk cipher.SecKey
			if err := pk.Set(kp.PK); err == nil {
				if err := sk.Set(kp.SK); err == nil {
					log.Info("Loaded existing LAN DMSG server keypair")
					return pk, sk, nil
				}
			}
		}
	}

	// Generate new keypair
	pk, sk := cipher.GenerateKeyPair()
	kp := lanDmsgKeyPair{PK: pk.Hex(), SK: sk.Hex()}
	data, err = json.MarshalIndent(kp, "", "  ")
	if err != nil {
		return pk, sk, nil // Use the keypair even if we can't save it
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		log.WithError(err).Warn("Failed to save LAN DMSG server keypair")
	} else {
		log.Info("Generated and saved new LAN DMSG server keypair")
	}
	return pk, sk, nil
}
