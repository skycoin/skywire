// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/loginchain.go
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	skycoincipher "github.com/skycoin/skycoin/src/cipher"
)

const loginFiberTOMLTemplate = `# Login chain fiber configuration — auto-generated, do not edit
[node]
genesis_signature_str = ""
genesis_address_str = "%s"
blockchain_pubkey_str = "%s"
blockchain_seckey_str = "%s"
genesis_timestamp = %d
genesis_coin_volume = 1000000000
default_connections = []
peer_list_url = ""
port = 6001
web_interface_port = 6421
display_name = "SkywireLogin"
ticker = "SWL"
coin_hours_display_name = "Login Hours"
coin_hours_display_name_singular = "Login Hour"
coin_hours_ticker = "SLH"
bip44_coin = 8001
explorer_url = ""

[params]
max_coin_supply = 1000000000
initial_unlocked_count = 1
unlock_address_rate = 0
unlock_time_interval = 0
distribution_addresses = [
    "%s",
]
`

type genesisWallet struct {
	PubKey  string `json:"pubkey"`
	SecKey  string `json:"seckey"`
	Address string `json:"address"`
}

// ensureLoginChain sets up a local fiber chain for login authentication.
// It wipes blockchain data on every startup (fresh chain) but preserves the
// genesis wallet so the genesis address stays the same across restarts.
// Returns the node address and a cleanup function that kills the subprocess.
func ensureLoginChain(wd string) (nodeAddr string, cleanup func(), err error) {
	loginDataDir := filepath.Join(wd, "login_data")
	genesisPath := filepath.Join(wd, "login_genesis.json")
	fiberTOMLPath := filepath.Join(wd, "login_fiber.toml")

	// Always start fresh — wipe blockchain data
	if err := os.RemoveAll(loginDataDir); err != nil {
		return "", nil, fmt.Errorf("failed to remove login_data: %w", err)
	}

	// Load or create genesis wallet
	var gw genesisWallet
	data, err := os.ReadFile(genesisPath) //nolint:gosec
	if err == nil {
		if err := json.Unmarshal(data, &gw); err != nil {
			return "", nil, fmt.Errorf("failed to parse login_genesis.json: %w", err)
		}
		fmt.Printf("Login chain: using existing genesis wallet %s\n", gw.Address)
	} else {
		// Generate new keypair
		pk, sk := skycoincipher.GenerateKeyPair()
		addr := skycoincipher.AddressFromPubKey(pk)
		gw = genesisWallet{
			PubKey:  pk.Hex(),
			SecKey:  sk.Hex(),
			Address: addr.String(),
		}
		data, err := json.MarshalIndent(gw, "", "  ")
		if err != nil {
			return "", nil, fmt.Errorf("failed to marshal genesis wallet: %w", err)
		}
		if err := os.WriteFile(genesisPath, data, 0600); err != nil {
			return "", nil, fmt.Errorf("failed to write login_genesis.json: %w", err)
		}
		fmt.Printf("Login chain: generated new genesis wallet %s\n", gw.Address)
	}

	// Write fiber.toml for login chain
	tomlContent := fmt.Sprintf(loginFiberTOMLTemplate,
		gw.Address,
		gw.PubKey,
		gw.SecKey,
		time.Now().Unix(),
		gw.Address,
	)
	if err := os.WriteFile(fiberTOMLPath, []byte(tomlContent), 0600); err != nil {
		return "", nil, fmt.Errorf("failed to write login_fiber.toml: %w", err)
	}

	// Start skycoin node subprocess using the skywire binary
	// (skywire skycoin daemon) to ensure same codebase
	skywireBin, err := os.Executable()
	if err != nil {
		skywireBin = "skywire" // fallback to PATH
	}
	cmd := exec.Command(skywireBin, //nolint:gosec
		"skycoin", "daemon",
		"--block-publisher",
		"--data-dir="+loginDataDir,
		"--localhost-only",
		"--disable-networking",
		"--web-interface-port=6421",
		"--port=6001",
		"--log-level=warn",
	)
	cmd.Env = append(os.Environ(),
		"FIBER_TOML="+fiberTOMLPath,
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	fmt.Println("Login chain: starting skycoin node on :6421 ...")
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("failed to start login chain node: %w", err)
	}

	cleanupFn := func() {
		fmt.Println("Login chain: stopping node...")
		if cmd.Process != nil {
			cmd.Process.Kill() //nolint:errcheck,gosec
		}
	}

	// Wait for health check
	healthURL := "http://127.0.0.1:6421/api/v1/health"
	healthy := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get(healthURL) //nolint:gosec
		if err == nil {
			resp.Body.Close() //nolint:errcheck,gosec
			if resp.StatusCode == http.StatusOK {
				healthy = true
				break
			}
		}
	}
	if !healthy {
		cleanupFn()
		return "", nil, fmt.Errorf("login chain node failed to become healthy after 30s")
	}

	fmt.Println("Login chain: node is healthy")
	return "http://127.0.0.1:6421", cleanupFn, nil
}
