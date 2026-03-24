// Package clirpc root.go
package clirpc

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/skycoin/dmsg/pkg/disc"
	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/spf13/pflag"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

const (
	// RPCAddrEnvVar is the environment variable name for the RPC server address
	RPCAddrEnvVar = "SKYWIRE_RPC"
)

var (
	logger = logging.MustGetLogger("skywire-cli")
	// DefaultRPCAddr is the default RPC address (from env var or default value)
	DefaultRPCAddr = getDefaultRPCAddr()
	// Addr is the address (ip:port) of the rpc server
	Addr string
	// Timeout is the timeout for RPC calls in seconds (0 = unlimited)
	Timeout int = 30
	// VisorPK is the public key of the visor to connect to over dmsg
	VisorPK string
	// CliSK is the secret key for authenticating CLI over dmsg
	CliSK string
)

// getDefaultRPCAddr returns the RPC address from environment variable or the default value
func getDefaultRPCAddr() string {
	if addr := os.Getenv(RPCAddrEnvVar); addr != "" {
		return addr
	}
	return skyenv.RPCAddr
}

// Client is used by other skywire-cli commands to query the visor rpc
func Client(cmdFlags *pflag.FlagSet) (visor.API, error) {
	// If VisorPK is provided, use dmsg connection
	if VisorPK != "" {
		return DmsgClient(cmdFlags)
	}

	// Default: TCP connection to local RPC
	const rpcDialTimeout = time.Second * 5
	conn, err := net.DialTimeout("tcp", Addr, rpcDialTimeout)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("RPC connection failed; is skywire running?: %v", err))
		return nil, err
	}
	// Timeout of 0 means unlimited
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with RPC address as tag for better identification
	rpcLogger := logging.MustGetLogger(fmt.Sprintf("rpc://%s", Addr))
	return visor.NewRPCClient(rpcLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}

// DmsgClient creates an RPC client over dmsg
func DmsgClient(cmdFlags *pflag.FlagSet) (visor.API, error) {
	// Parse visor public key
	visorPubKey := cipher.PubKey{}
	if err := visorPubKey.UnmarshalText([]byte(VisorPK)); err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("invalid visor public key: %v", err))
		return nil, err
	}

	// Parse CLI secret key (if provided)
	var cliPK cipher.PubKey
	var cliSK cipher.SecKey
	if CliSK != "" {
		if err := cliSK.UnmarshalText([]byte(CliSK)); err != nil {
			internal.PrintError(cmdFlags, fmt.Errorf("invalid CLI secret key: %v", err))
			return nil, err
		}
		var err error
		cliPK, err = cliSK.PubKey()
		if err != nil {
			internal.PrintError(cmdFlags, fmt.Errorf("failed to derive public key: %v", err))
			return nil, err
		}
		logger.Infof("Using CLI key: %s", cliPK)
	} else {
		// Generate ephemeral keypair if no secret key provided
		cliPK, cliSK = cipher.GenerateKeyPair()
		logger.Warnf("No --cli key provided, using ephemeral key (will fail if visor has whitelist): %s", cliPK)
	}

	// Create dmsg client
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*30)
	defer cancel()

	dmsgConf := &dmsg.Config{
		MinSessions:    1,
		UpdateInterval: time.Minute,
	}

	// Use production dmsg discovery
	dmsgDisc := disc.NewHTTP(deployment.Prod.DmsgDiscovery, &http.Client{}, logger)

	dmsgC := dmsg.NewClient(cliPK, cliSK, dmsgDisc, dmsgConf)
	go dmsgC.Serve(context.Background())

	// Wait for dmsg client to be ready
	select {
	case <-dmsgC.Ready():
		logger.Info("Dmsg client ready")
	case <-ctx.Done():
		internal.PrintError(cmdFlags, fmt.Errorf("dmsg client initialization timed out"))
		return nil, ctx.Err()
	}

	// Dial visor over dmsg
	addr := dmsg.Addr{PK: visorPubKey, Port: skyenv.DmsgHypervisorPort}
	logger.Infof("Dialing visor %s over dmsg on port %d...", visorPubKey, skyenv.DmsgHypervisorPort)

	conn, err := dmsgC.Dial(ctx, addr)
	if err != nil {
		internal.PrintError(cmdFlags, fmt.Errorf("failed to dial visor over dmsg: %v", err))
		return nil, err
	}

	logger.Info("Connected to visor over dmsg")
	// Use the configured timeout for RPC calls
	rpcCallTimeout := time.Duration(Timeout) * time.Second
	// Use logger with dmsg address as tag for better identification
	dmsgLogger := logging.MustGetLogger(fmt.Sprintf("dmsg://%s", VisorPK[:8]))
	return visor.NewRPCClient(dmsgLogger, conn, visor.RPCPrefix, rpcCallTimeout), nil
}
