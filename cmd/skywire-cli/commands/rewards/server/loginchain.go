// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/loginchain.go
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// loginFiberTOMLTemplate is the fiber.toml for the block publisher node.
const loginFiberTOMLTemplate = `# Login chain fiber configuration — auto-generated, do not edit
[node]
genesis_signature_str = ""
genesis_address_str = ""
blockchain_pubkey_str = ""
blockchain_seckey_str = ""
genesis_timestamp = %d
genesis_coin_volume = 1000000000
default_connections = ["127.0.0.1:6002"]
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

// loginPeerTOMLTemplate is the fiber.toml for the peer node that serves the wallet API.
const loginPeerTOMLTemplate = `# Login chain peer node — auto-generated, do not edit
[node]
genesis_signature_str = ""
genesis_address_str = ""
blockchain_pubkey_str = ""
blockchain_seckey_str = ""
genesis_timestamp = %d
genesis_coin_volume = 1000000000
default_connections = ["127.0.0.1:6001"]
peer_list_url = ""
port = 6002
web_interface_port = 6422
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

// LoginChainCmd starts the login chain (block publisher + peer node).
// Run this as a separate process; the reward server connects via --login-node.
var LoginChainCmd = &cobra.Command{
	Use:   "loginchain",
	Short: "start the login chain nodes (block publisher + peer)",
	Long: `Starts a two-node login chain for wallet ownership verification.

Block publisher on :6001 (P2P) / :6421 (API)
Peer node on :6002 (P2P) / :6422 (API) — serves wallet API

The reward system UI server connects to the peer node:
  skywire cli rewards ui --login-node http://127.0.0.1:6422

Blockchain data is wiped on every startup (fresh chain).
The genesis wallet (login_genesis.json) is preserved across restarts.`,
	Run: func(_ *cobra.Command, _ []string) {
		runLoginChain()
	},
}

func runLoginChain() {
	skywireBin, err := os.Executable()
	if err != nil {
		skywireBin = "skywire"
	}

	publisherDataDir := filepath.Join(wd, "login_data")
	peerDataDir := filepath.Join(wd, "login_peer_data")
	genesisPath := filepath.Join(wd, "login_genesis.json")
	fiberTOMLPath := filepath.Join(wd, "login_fiber.toml")
	peerTOMLPath := filepath.Join(wd, "login_peer.toml")

	// Wipe blockchain data (fresh chain every startup)
	for _, dir := range []string{publisherDataDir, peerDataDir} {
		if err := os.RemoveAll(dir); err != nil {
			fmt.Printf("Failed to remove %s: %v\n", dir, err)
			return
		}
	}

	// Load or create genesis wallet
	var gw addressGenWallet
	data, err := os.ReadFile(genesisPath) //nolint:gosec
	if err == nil {
		if err := json.Unmarshal(data, &gw); err != nil || len(gw.Entries) == 0 {
			fmt.Printf("Failed to parse login_genesis.json: %v\n", err)
			return
		}
		fmt.Printf("Login chain: using existing genesis wallet %s\n", gw.Entries[0].Address)
	} else {
		fmt.Println("Login chain: generating genesis wallet via addressGen...")
		genCmd := exec.Command(skywireBin, "skycoin", "cli", "addressGen") //nolint:gosec
		genOut, err := genCmd.Output()
		if err != nil {
			fmt.Printf("addressGen failed: %v\n", err)
			return
		}
		if err := json.Unmarshal(genOut, &gw); err != nil || len(gw.Entries) == 0 {
			fmt.Printf("Failed to parse addressGen output: %v\n", err)
			return
		}
		if err := os.WriteFile(genesisPath, genOut, 0600); err != nil {
			fmt.Printf("Failed to write login_genesis.json: %v\n", err)
			return
		}
		fmt.Printf("Login chain: generated new genesis wallet %s\n", gw.Entries[0].Address)
	}

	genesisAddr := gw.Entries[0].Address
	genesisTimestamp := time.Now().Unix()

	// Write publisher fiber.toml only — peer toml created after publisher signs genesis
	publisherTOML := fmt.Sprintf(loginFiberTOMLTemplate, genesisTimestamp, genesisAddr)
	if err := os.WriteFile(fiberTOMLPath, []byte(publisherTOML), 0600); err != nil {
		fmt.Printf("Failed to write login_fiber.toml: %v\n", err)
		return
	}

	sharedEnv := append(os.Environ(), "GENESIS="+genesisPath)

	// Step 1: Start block publisher — it creates and signs the genesis block,
	// then writes the signature back to login_fiber.toml
	publisherCmd := exec.Command(skywireBin, "skycoin", "daemon", //nolint:gosec
		"--block-publisher",
		"--localhost-only",
		"--download-peerlist=false",
		"--data-dir="+publisherDataDir,
		"--web-interface-port=6421",
		"--port=6001",
		"--log-level=warn",
	)
	publisherCmd.Env = append(sharedEnv, "FIBER_TOML="+fiberTOMLPath)
	publisherCmd.Stdout = os.Stdout
	publisherCmd.Stderr = os.Stderr

	fmt.Println("Login chain: starting block publisher on :6001/:6421 ...")
	if err := publisherCmd.Start(); err != nil {
		fmt.Printf("Failed to start publisher: %v\n", err)
		return
	}

	// Wait for publisher to be healthy (genesis block created and signed)
	publisherHealthy := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get("http://127.0.0.1:6421/api/v1/health") //nolint:gosec
		if err == nil {
			resp.Body.Close() //nolint:errcheck,gosec
			if resp.StatusCode == http.StatusOK {
				publisherHealthy = true
				break
			}
		}
	}
	if !publisherHealthy {
		fmt.Println("Login chain: publisher failed to become healthy after 30s")
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		return
	}
	fmt.Println("Login chain: publisher is healthy")

	// Step 2: Publisher wrote genesis signature back to login_fiber.toml.
	// Create peer toml from the updated publisher toml (with signed genesis).
	updatedTOML, err := os.ReadFile(fiberTOMLPath) //nolint:gosec
	if err != nil {
		fmt.Printf("Failed to read updated login_fiber.toml: %v\n", err)
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		return
	}
	peerTOMLContent := strings.ReplaceAll(string(updatedTOML), "port = 6001", "port = 6002")
	peerTOMLContent = strings.ReplaceAll(peerTOMLContent, "web_interface_port = 6421", "web_interface_port = 6422")
	peerTOMLContent = strings.ReplaceAll(peerTOMLContent, `"127.0.0.1:6002"`, `"127.0.0.1:6001"`)
	if err := os.WriteFile(peerTOMLPath, []byte(peerTOMLContent), 0600); err != nil {
		fmt.Printf("Failed to write login_peer.toml: %v\n", err)
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		return
	}

	// Step 3: Start peer node with signed genesis credentials
	peerCmd := exec.Command(skywireBin, "skycoin", "daemon", //nolint:gosec
		"--localhost-only",
		"--download-peerlist=false",
		"--disable-csrf",
		"--host-whitelist=fiber.skywire.dev",
		"--enable-all-api-sets=true",
		"--data-dir="+peerDataDir,
		"--web-interface-port=6422",
		"--port=6002",
		"--log-level=warn",
	)
	peerCmd.Env = append(sharedEnv, "FIBER_TOML="+peerTOMLPath)
	peerCmd.Stdout = os.Stdout
	peerCmd.Stderr = os.Stderr

	fmt.Println("Login chain: starting peer node on :6002/:6422 ...")
	if err := peerCmd.Start(); err != nil {
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		fmt.Printf("Failed to start peer: %v\n", err)
		return
	}

	// Cleanup on signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		fmt.Println("\nLogin chain: shutting down...")
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		peerCmd.Process.Kill()      //nolint:errcheck,gosec
		os.Exit(0)
	}()

	// Wait for peer to be healthy
	peerHealthy := false
	for i := 0; i < 30; i++ {
		time.Sleep(1 * time.Second)
		resp, err := http.Get("http://127.0.0.1:6422/api/v1/health") //nolint:gosec
		if err == nil {
			resp.Body.Close() //nolint:errcheck,gosec
			if resp.StatusCode == http.StatusOK {
				peerHealthy = true
				break
			}
		}
	}
	if !peerHealthy {
		fmt.Println("Login chain: peer failed to become healthy after 30s")
		publisherCmd.Process.Kill() //nolint:errcheck,gosec
		peerCmd.Process.Kill()      //nolint:errcheck,gosec
		return
	}
	fmt.Println("Login chain: peer is healthy")

	// Bootstrap: create block #1
	if err := bootstrapLoginChain(skywireBin, "http://127.0.0.1:6421", fiberTOMLPath, genesisPath, genesisAddr); err != nil {
		fmt.Printf("Login chain: bootstrap failed: %v\n", err)
		fmt.Println("Login chain: nodes still running — you can bootstrap manually")
	}

	fmt.Println("\nLogin chain is running. Connect the reward server with:")
	fmt.Println("  skywire cli rewards ui --login-node http://127.0.0.1:6422")
	fmt.Println("\nPress Ctrl+C to stop.")

	// Wait for either process to exit
	done := make(chan error, 2)
	go func() { done <- publisherCmd.Wait() }()
	go func() { done <- peerCmd.Wait() }()
	<-done

	// Kill the other
	publisherCmd.Process.Kill() //nolint:errcheck,gosec
	peerCmd.Process.Kill()      //nolint:errcheck,gosec
}

// bootstrapLoginChain uses createRawTransaction + injectTransaction to create block #1.
func bootstrapLoginChain(skywireBin, publisherURL, fiberTOMLPath, genesisPath, genesisAddr string) error {
	cliEnv := append(os.Environ(),
		"FIBER_TOML="+fiberTOMLPath,
		"RPC_ADDR="+publisherURL,
	)

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

	fmt.Println("Login chain: injecting bootstrap transaction...")
	injectBody := fmt.Sprintf(`{"rawtx":"%s"}`, rawTxStr)
	injectReq, err := http.NewRequest("POST", publisherURL+"/api/v1/injectTransaction", strings.NewReader(injectBody))
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

	// Wait for block #1 on the peer node
	fmt.Println("Login chain: waiting for bootstrap block...")
	peerURL := "http://127.0.0.1:6422"
	for i := 0; i < 30; i++ {
		time.Sleep(2 * time.Second)
		resp, err := http.Get(fmt.Sprintf("%s/api/v1/blockchain/metadata", peerURL)) //nolint:gosec
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec
		resp.Body.Close()                //nolint:errcheck,gosec
		if strings.Contains(string(body), `"seq": 1`) {
			fmt.Println("Login chain: bootstrap complete — block #1 confirmed on peer")
			return nil
		}
	}
	return fmt.Errorf("bootstrap block not confirmed on peer after 60s")
}

// ensureLoginChain is the old embedded approach — now just returns an error
// directing users to run the loginchain subcommand separately.
// Kept for backward compatibility with --login-node auto.
func ensureLoginChain(_ string) (string, func(), error) {
	return "", nil, fmt.Errorf("--login-node auto is no longer supported; run 'skywire cli rewards loginchain' separately and use --login-node http://127.0.0.1:6422")
}
