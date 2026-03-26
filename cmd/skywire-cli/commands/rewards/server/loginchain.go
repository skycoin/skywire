// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/loginchain.go
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const loginFiberTOMLTemplate = `# Login chain fiber configuration — auto-generated, do not edit
[node]
genesis_signature_str = ""
genesis_address_str = ""
blockchain_pubkey_str = ""
blockchain_seckey_str = ""
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

// addressGenWallet is the JSON format produced by `skycoin cli addressGen`
// and expected by the GENESIS env var.
type addressGenWallet struct {
	Entries []struct {
		Address   string `json:"address"`
		PublicKey string `json:"public_key"`
		SecretKey string `json:"secret_key"`
	} `json:"entries"`
}

// ensureLoginChain sets up a local fiber chain for login authentication.
// It wipes blockchain data on every startup (fresh chain) but preserves the
// genesis wallet so the genesis address stays the same across restarts.
// Returns the node address and a cleanup function that kills the subprocess.
func ensureLoginChain(wd string) (nodeAddr string, cleanup func(), err error) {
	loginDataDir := filepath.Join(wd, "login_data")
	genesisPath := filepath.Join(wd, "login_genesis.json")
	fiberTOMLPath := filepath.Join(wd, "login_fiber.toml")

	skywireBin, err := os.Executable()
	if err != nil {
		skywireBin = "skywire" // fallback to PATH
	}

	// Always start fresh — wipe blockchain data
	if err := os.RemoveAll(loginDataDir); err != nil {
		return "", nil, fmt.Errorf("failed to remove login_data: %w", err)
	}

	// Load or create genesis wallet using addressGen format
	var gw addressGenWallet
	data, err := os.ReadFile(genesisPath) //nolint:gosec
	if err == nil {
		if err := json.Unmarshal(data, &gw); err != nil || len(gw.Entries) == 0 {
			return "", nil, fmt.Errorf("failed to parse login_genesis.json: %w", err)
		}
		fmt.Printf("Login chain: using existing genesis wallet %s\n", gw.Entries[0].Address)
	} else {
		// Generate genesis wallet via skycoin cli addressGen
		fmt.Println("Login chain: generating genesis wallet via addressGen...")
		genCmd := exec.Command(skywireBin, "skycoin", "cli", "addressGen") //nolint:gosec
		genOut, err := genCmd.Output()
		if err != nil {
			return "", nil, fmt.Errorf("addressGen failed: %w", err)
		}
		if err := json.Unmarshal(genOut, &gw); err != nil || len(gw.Entries) == 0 {
			return "", nil, fmt.Errorf("failed to parse addressGen output: %w", err)
		}
		if err := os.WriteFile(genesisPath, genOut, 0600); err != nil {
			return "", nil, fmt.Errorf("failed to write login_genesis.json: %w", err)
		}
		fmt.Printf("Login chain: generated new genesis wallet %s\n", gw.Entries[0].Address)
	}

	genesisAddr := gw.Entries[0].Address

	// Write fiber.toml for login chain
	// Leave genesis_address_str, blockchain_pubkey_str, blockchain_seckey_str empty —
	// they will be loaded from GENESIS env var at startup.
	tomlContent := fmt.Sprintf(loginFiberTOMLTemplate,
		time.Now().Unix(),
		genesisAddr,
	)
	if err := os.WriteFile(fiberTOMLPath, []byte(tomlContent), 0600); err != nil {
		return "", nil, fmt.Errorf("failed to write login_fiber.toml: %w", err)
	}

	// Build daemon arguments: fixed args + configurable flags + fixed port args
	daemonArgs := []string{"skycoin", "daemon"}
	if loginChainFlags != "" {
		daemonArgs = append(daemonArgs, strings.Fields(loginChainFlags)...)
	} else {
		daemonArgs = append(daemonArgs,
			"--block-publisher",
			"--localhost-only",
			"--download-peerlist=false",
			"--disable-default-peers",
			"--disable-csrf",
			"--host-whitelist=fiber.skywire.dev",
			"--log-level=warn",
		)
	}
	daemonArgs = append(daemonArgs,
		"--data-dir="+loginDataDir,
		"--web-interface-port=6421",
		"--port=6001",
	)
	cmd := exec.Command(skywireBin, daemonArgs...) //nolint:gosec
	cmd.Env = append(os.Environ(),
		"FIBER_TOML="+fiberTOMLPath,
		"GENESIS="+genesisPath,
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

	// Bootstrap: create block #1 by sending genesis coins to the same address.
	// The genesis UxOut has a null SrcTransaction which the transaction API
	// cannot handle. createRawTransaction + broadcastTransaction creates a
	// proper block with valid SrcTransactions on the UxOuts.
	nodeURL := "http://127.0.0.1:6421"
	if err := bootstrapLoginChain(skywireBin, nodeURL, fiberTOMLPath, genesisPath, genesisAddr); err != nil {
		fmt.Printf("Login chain: bootstrap warning: %v\n", err)
	}

	return nodeURL, cleanupFn, nil
}

// bootstrapLoginChain uses the genesis wallet (addressGen format) with
// createRawTransaction to send genesis coins to the same address,
// producing block #1 with proper SrcTransactions on the UxOuts.
func bootstrapLoginChain(skywireBin, nodeURL, fiberTOMLPath, genesisPath, genesisAddr string) error {
	cliEnv := append(os.Environ(),
		"FIBER_TOML="+fiberTOMLPath,
		"RPC_ADDR="+nodeURL,
	)

	// The addressGen output (login_genesis.json) IS a wallet file.
	// createRawTransaction can use it directly.
	fmt.Println("Login chain: creating bootstrap transaction...")
	createCmd := exec.Command(skywireBin, "skycoin", "cli", "createRawTransaction", //nolint:gosec
		genesisPath, genesisAddr, "1000")
	createCmd.Env = cliEnv
	rawTx, err := createCmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("createRawTransaction failed: %s: %w", string(rawTx), err)
	}

	rawTxStr := strings.TrimSpace(string(rawTx))
	if rawTxStr == "" {
		return fmt.Errorf("createRawTransaction returned empty output")
	}

	// Inject transaction via API with no_broadcast (no peers to broadcast to)
	fmt.Println("Login chain: injecting bootstrap transaction (no broadcast)...")
	injectBody := fmt.Sprintf(`{"rawtx":"%s","no_broadcast":true}`, rawTxStr)
	injectReq, err := http.NewRequest("POST", nodeURL+"/api/v1/injectTransaction", strings.NewReader(injectBody))
	if err != nil {
		return fmt.Errorf("inject request: %w", err)
	}
	injectReq.Header.Set("Content-Type", "application/json")
	injectResp, err := http.DefaultClient.Do(injectReq)
	if err != nil {
		return fmt.Errorf("inject failed: %w", err)
	}
	defer injectResp.Body.Close()               //nolint:errcheck,gosec
	injectOut, _ := io.ReadAll(injectResp.Body) //nolint:errcheck,gosec
	if injectResp.StatusCode != http.StatusOK {
		return fmt.Errorf("inject failed (%d): %s", injectResp.StatusCode, string(injectOut))
	}
	fmt.Printf("Login chain: bootstrap tx injected: %s\n", strings.TrimSpace(string(injectOut)))

	// Wait for block #1
	fmt.Println("Login chain: waiting for bootstrap block...")
	for i := 0; i < 15; i++ {
		time.Sleep(2 * time.Second)
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/blockchain/metadata", nodeURL)) //nolint:gosec
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
		resp.Body.Close()                //nolint:errcheck,gosec
		if strings.Contains(string(body), `"seq": 1`) {
			fmt.Println("Login chain: bootstrap complete")
			return nil
		}
	}
	return fmt.Errorf("bootstrap block not confirmed after 30s")
}
