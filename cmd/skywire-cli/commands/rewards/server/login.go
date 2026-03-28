// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/login.go
package clirewardsserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bitfield/script"
	"github.com/gin-gonic/gin"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

// session represents an authenticated user session
type session struct {
	Address   string    `json:"address"`
	Visors    []string  `json:"visors"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// pendingLogin tracks a login challenge waiting for transaction confirmation
type pendingLogin struct {
	Address      string    `json:"address"`       // original reward address or xpub
	LoginAddress string    `json:"login_address"` // derived address for verification (change chain for xpub)
	Visors       []string  `json:"visors"`
	Challenge    string    `json:"challenge"`   // random challenge string
	FundedTxID   string    `json:"funded_txid"` // txid of coins sent to user
	IsXpub       bool      `json:"is_xpub"`
	CreatedAt    time.Time `json:"created_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

var (
	sessions        = make(map[string]*session)
	sessionsMu      sync.RWMutex
	pendingLogins   = make(map[string]*pendingLogin) // challenge -> pending login
	pendingLoginsMu sync.RWMutex
)

func generateSessionID() string {
	b := make([]byte, 32)
	rand.Read(b) //nolint:errcheck,gosec
	return hex.EncodeToString(b)
}

func generateChallenge() string {
	b := make([]byte, 16)
	rand.Read(b) //nolint:errcheck,gosec
	return hex.EncodeToString(b)
}

// findVisorsByRewardAddress searches log_backups for surveys matching the given
// reward address or xpub key. Returns matching visor public keys.
func findVisorsByRewardAddress(backupsDir, address string) []string {
	var visors []string
	entries, err := os.ReadDir(backupsDir)
	if err != nil {
		return nil
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		surveyPath := filepath.Join(backupsDir, entry.Name(), "node-info.json")
		data, err := os.ReadFile(surveyPath) //nolint:gosec
		if err != nil {
			continue
		}
		var survey struct {
			SkycoinAddress string `json:"skycoin_address"`
		}
		if err := json.Unmarshal(data, &survey); err != nil {
			continue
		}
		if survey.SkycoinAddress == address {
			visors = append(visors, entry.Name())
		}
	}
	return visors
}

// checkBalanceOnLoginChain checks the balance of an address on the login chain.
func checkBalanceOnLoginChain(nodeURL, address string) (coins uint64, hours uint64, err error) {
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/balance?addrs=%s", nodeURL, address)) //nolint:gosec
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close() //nolint:errcheck,gosec

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, 0, err
	}

	var result struct {
		Confirmed struct {
			Coins uint64 `json:"coins"`
			Hours uint64 `json:"hours"`
		} `json:"confirmed"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return 0, 0, err
	}

	return result.Confirmed.Coins, result.Confirmed.Hours, nil
}

// sendCoinsOnLoginChain sends coins from the genesis wallet to the target address
// using the skycoin CLI (createRawTransaction + injectTransaction).
// The CLI loads the genesis wallet file directly for signing.
func sendCoinsOnLoginChain(nodeURL, destAddress string, coins string) (string, error) {
	genesisPath := filepath.Join(wd, "login_genesis.json")
	fiberTOMLPath := filepath.Join(wd, "login_fiber.toml")

	skywireBin, err := os.Executable()
	if err != nil {
		skywireBin = "skywire"
	}

	// Create raw transaction using the genesis wallet
	createRawTxCmd := fmt.Sprintf(`bash -c 'FIBER_TOML="%s" RPC_ADDR="%s" "%s" skycoin cli createRawTransaction "%s" "%s" "%s" 2>&1'`,
		fiberTOMLPath, nodeURL, skywireBin, genesisPath, destAddress, coins)
	fmt.Printf("Login chain: running: %s\n", createRawTxCmd)
	rawTxStr, err := script.Exec(createRawTxCmd).String()
	if err != nil {
		return "", fmt.Errorf("createRawTransaction failed: %s: %w", rawTxStr, err)
	}
	rawTxStr = strings.TrimSpace(rawTxStr)
	if rawTxStr == "" {
		return "", fmt.Errorf("createRawTransaction returned empty output")
	}

	// Inject transaction with no_broadcast
	fmt.Printf("Login chain: injecting tx to %s (%d bytes)\n", nodeURL, len(rawTxStr))
	injectBody := fmt.Sprintf(`{"rawtx":"%s","no_broadcast":true}`, rawTxStr)
	injectReq, err := http.NewRequest("POST", nodeURL+"/api/v1/injectTransaction", strings.NewReader(injectBody))
	if err != nil {
		return "", fmt.Errorf("inject request: %w", err)
	}
	injectReq.Header.Set("Content-Type", "application/json")
	injectResp, err := http.DefaultClient.Do(injectReq)
	if err != nil {
		return "", fmt.Errorf("inject failed: %w", err)
	}
	defer injectResp.Body.Close()               //nolint:errcheck,gosec
	injectOut, _ := io.ReadAll(injectResp.Body) //nolint:errcheck,gosec
	if injectResp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("inject failed (%d): %s", injectResp.StatusCode, string(injectOut))
	}

	txID := strings.Trim(strings.TrimSpace(string(injectOut)), `"`)
	return txID, nil
}

// checkTransactionToAddress checks if there's a confirmed transaction
// FROM the given address TO the genesis address on the login chain.
func checkTransactionToAddress(nodeURL, fromAddress, toAddress string) bool {
	resp, err := http.Get(fmt.Sprintf("%s/api/v1/transactions?addrs=%s&confirmed=true&verbose=1", nodeURL, fromAddress)) //nolint:gosec
	if err != nil {
		return false
	}
	defer resp.Body.Close() //nolint:errcheck,gosec

	body, _ := io.ReadAll(resp.Body) //nolint:errcheck,gosec

	// Use verbose=1 to get full input details including owner addresses
	var txs []struct {
		Txn struct {
			Outputs []struct {
				Dst string `json:"dst"`
			} `json:"outputs"`
			Inputs []struct {
				Owner string `json:"owner"`
			} `json:"inputs"`
		} `json:"txn"`
	}
	if err := json.Unmarshal(body, &txs); err != nil {
		return false
	}

	for _, tx := range txs {
		// Check if any output goes to the target address
		hasOutputToTarget := false
		for _, out := range tx.Txn.Outputs {
			if out.Dst == toAddress {
				hasOutputToTarget = true
				break
			}
		}
		if !hasOutputToTarget {
			continue
		}
		// With verbose=1, inputs have owner field; without it, inputs are just UxIDs.
		// If owner fields are populated, verify the sender. Otherwise, since we queried
		// by fromAddress, any tx with an output to toAddress is likely the return tx.
		if len(tx.Txn.Inputs) > 0 && tx.Txn.Inputs[0].Owner != "" {
			for _, inp := range tx.Txn.Inputs {
				if inp.Owner == fromAddress {
					return true
				}
			}
		} else {
			// Non-verbose: trust that the tx involves fromAddress (queried by it)
			// and has an output to toAddress
			return true
		}
	}
	return false
}

// registerLoginRoutes adds login-related routes to the gin router.
// When loginEnabled is false, the login page shows a "not available" message.
func registerLoginRoutes(r *gin.Engine, wd string, loginEnabled bool) {
	backupsDir := filepath.Join(wd, "log_backups")

	// Login page
	r.GET("/login", func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Writer.WriteHeader(http.StatusOK)

		l := loginPageHeader("Login")
		l += navlinks

		if !loginEnabled {
			l += "<h1>Login</h1>"
			l += "<p class='info'>Login is not currently available on this instance.</p>"
			l += "</body></html>"
			c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
			return
		}

		l += "<h1>Login</h1>"
		l += "<p>Enter your reward address to prove wallet ownership and view your visor data.</p>"
		l += "<form method='POST' action='/login'>"
		l += "<input type='text' name='address' placeholder='Skycoin reward address' required>"
		l += "<br><button type='submit'>Start verification</button>"
		l += "</form>"

		if msg := c.Query("msg"); msg != "" {
			l += "<p class='error'>" + strings.ReplaceAll(msg, "<", "&lt;") + "</p>"
		}
		if msg := c.Query("info"); msg != "" {
			l += "<p class='info'>" + strings.ReplaceAll(msg, "<", "&lt;") + "</p>"
		}

		l += "</body></html>"
		c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
	})

	// Login POST — look up address, create verification challenge
	r.POST("/login", func(c *gin.Context) {
		if !loginEnabled {
			c.Redirect(http.StatusFound, "/login?msg=Login+is+not+currently+available")
			return
		}
		address := strings.TrimSpace(c.PostForm("address"))
		if address == "" {
			c.Redirect(http.StatusFound, "/login?msg=Please+enter+a+reward+address")
			return
		}

		visors := findVisorsByRewardAddress(backupsDir, address)
		if len(visors) == 0 {
			c.Redirect(http.StatusFound, "/login?msg=No+visors+found+for+this+address")
			return
		}

		// Determine the login verification address.
		// For xpub keys: derive change chain address (m/account'/1/0)
		// For single addresses: use the address directly
		_, isXpub, vaErr := rewardconfig.ValidateRewardAddress(address)
		if vaErr != nil {
			c.Redirect(http.StatusFound, "/login?msg=Invalid+address:+"+vaErr.Error())
			return
		}
		loginAddress := address
		if isXpub {
			// Check xpub depth before attempting derivation
			if warning := rewardconfig.CheckXpubDepth(address); warning != "" {
				fmt.Printf("Login chain: xpub depth error for %s...\n", address[:20])
				c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
				c.Writer.WriteHeader(http.StatusBadRequest)
				l := loginPageHeader("Login Error")
				l += navlinks
				l += "<h1>Wrong xpub key level</h1>"
				l += "<p class='error'>The xpub key you provided is at the chain level (depth 4), "
				l += "not the account level (depth 3) required for login verification.</p>"
				l += "<p>The Skycoin web wallet shows the <strong>external chain</strong> xpub "
				l += "(BIP44 path <code>m/44'/8000'/0'/0</code>). The login system needs the "
				l += "<strong>account-level</strong> xpub (<code>m/44'/8000'/0'</code>) to derive "
				l += "change chain addresses for verification.</p>"
				l += "<h3>How to get the correct xpub</h3>"
				l += "<p>Export the account-level xpub from your wallet using the CLI:</p>"
				l += "<pre>skywire skycoin cli walletKeyExport WALLET_FILE -k xpub --path=0</pre>"
				l += "<p>The default <code>--path=0/0</code> gives the external chain xpub (wrong).<br>"
				l += "Use <code>--path=0</code> for the account-level xpub (correct).</p>"
				l += "<p>Then update your visor's reward address:</p>"
				l += "<pre>skywire reward ACCOUNT_XPUB</pre>"
				l += "<p><a href='/login'>Back to login</a></p>"
				l += "</body></html>"
				c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
				return
			}
			derived, err := rewardconfig.DeriveLoginAddressFromXpub(address, 0)
			if err != nil {
				c.Redirect(http.StatusFound, "/login?msg=Failed+to+derive+login+address+from+xpub:+"+err.Error())
				return
			}
			loginAddress = derived
			fmt.Printf("Login chain: derived change chain address %s from xpub\n", loginAddress)
		}

		// Fund the login address on the login chain if needed
		var fundedTxID string
		if loginNodeAddr != "" {
			coins, _, err := checkBalanceOnLoginChain(loginNodeAddr, loginAddress)
			if err != nil {
				fmt.Printf("Warning: failed to check login chain balance: %v\n", err)
			}
			if coins == 0 {
				// Send minimum coins (1 coin) to the login address
				txID, err := sendCoinsOnLoginChain(loginNodeAddr, loginAddress, "1.000000")
				if err != nil {
					fmt.Printf("Warning: failed to fund login address: %v\n", err)
					c.Redirect(http.StatusFound, "/login?msg=Failed+to+prepare+login+verification.+Please+try+again.")
					return
				}
				fundedTxID = txID
				fmt.Printf("Login chain: funded %s with 1 coin (tx: %s)\n", loginAddress, txID)
			}
		}

		// Create pending login challenge
		challenge := generateChallenge()
		pendingLoginsMu.Lock()
		pendingLogins[challenge] = &pendingLogin{
			Address:      address,
			LoginAddress: loginAddress,
			Visors:       visors,
			Challenge:    challenge,
			FundedTxID:   fundedTxID,
			IsXpub:       isXpub,
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(10 * time.Minute),
		}
		pendingLoginsMu.Unlock()

		c.Redirect(http.StatusFound, "/login/verify?challenge="+challenge)
	})

	// Verification page — instructs user to send coins back
	r.GET("/login/verify", func(c *gin.Context) {
		challenge := c.Query("challenge")
		if challenge == "" {
			c.Redirect(http.StatusFound, "/login?msg=Invalid+verification+link")
			return
		}

		pendingLoginsMu.RLock()
		pending, ok := pendingLogins[challenge]
		pendingLoginsMu.RUnlock()

		if !ok || time.Now().After(pending.ExpiresAt) {
			c.Redirect(http.StatusFound, "/login?msg=Verification+expired")
			return
		}

		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Writer.WriteHeader(http.StatusOK)

		l := loginPageHeader("Verify Wallet Ownership")
		l += navlinks
		l += "<h1>Verify Wallet Ownership</h1>"
		if pending.IsXpub {
			l += "<p>To prove you control the wallet for xpub <code>" + pending.Address[:20] + "...</code>, "
			l += "please send coins from the change chain address below to the login address.</p>"
			l += "<p><strong>Your login address (change chain):</strong> <code>" + pending.LoginAddress + "</code></p>"
		} else {
			l += "<p>To prove you control address <code>" + pending.Address + "</code>, "
			l += "please send coins from that address to the login address below.</p>"
		}
		l += "<p><strong>Send to:</strong> <code>" + loginGenesisAddress + "</code></p>"
		// Use the current request's host as the node URL for the wallet
		scheme := "https"
		if c.Request.TLS == nil {
			scheme = "http"
		}
		if fwdProto := c.GetHeader("X-Forwarded-Proto"); fwdProto != "" {
			scheme = fwdProto
		}
		nodeURL := scheme + "://" + c.Request.Host
		l += "<p>Use the Skycoin Web Wallet connected to this login chain node to send the transaction.</p>"
		l += "<p>Start the wallet with: <code>skywire skycoin web -n " + nodeURL + " -p 8006</code></p>"
		l += "<p>Then open <a href='http://127.0.0.1:8006' target='_blank'>http://127.0.0.1:8006</a></p>"
		l += "<p class='info'>This verifies that you hold the private key for your reward wallet. "
		l += "The login chain resets periodically — coins have no real value.</p>"
		l += "<hr>"
		l += "<p>Waiting for transaction confirmation...</p>"
		l += "<script>"
		l += "setInterval(function() {"
		l += "  fetch('/login/check?challenge=" + challenge + "')"
		l += "    .then(r => r.json())"
		l += "    .then(d => { if(d.confirmed) window.location='/account'; });"
		l += "}, 3000);"
		l += "</script>"
		l += "<noscript><p>JavaScript is required for auto-detection. "
		l += "<a href='/login/check?challenge=" + challenge + "&redirect=1'>Click here to check manually</a></p></noscript>"
		l += "</body></html>"
		c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
	})

	// Check if verification transaction is confirmed
	r.GET("/login/check", func(c *gin.Context) {
		challenge := c.Query("challenge")

		pendingLoginsMu.RLock()
		pending, ok := pendingLogins[challenge]
		pendingLoginsMu.RUnlock()

		if !ok || time.Now().After(pending.ExpiresAt) {
			if c.Query("redirect") == "1" {
				c.Redirect(http.StatusFound, "/login?msg=Verification+expired")
				return
			}
			c.JSON(http.StatusOK, gin.H{"confirmed": false, "error": "expired"})
			return
		}

		// Check if user sent coins FROM their login address TO the genesis address
		confirmed := checkTransactionToAddress(loginNodeAddr, pending.LoginAddress, loginGenesisAddress)

		if confirmed {
			// Create session
			sessionID := generateSessionID()
			sessionsMu.Lock()
			sessions[sessionID] = &session{
				Address:   pending.Address,
				Visors:    pending.Visors,
				CreatedAt: time.Now(),
				ExpiresAt: time.Now().Add(24 * time.Hour),
			}
			sessionsMu.Unlock()

			// Clean up pending login
			pendingLoginsMu.Lock()
			delete(pendingLogins, challenge)
			pendingLoginsMu.Unlock()

			// Set session cookie
			c.SetCookie("session", sessionID, 86400, "/", "", false, true)

			if c.Query("redirect") == "1" {
				c.Redirect(http.StatusFound, "/account")
				return
			}
			c.JSON(http.StatusOK, gin.H{"confirmed": true})
			return
		}

		if c.Query("redirect") == "1" {
			c.Redirect(http.StatusFound, "/login/verify?challenge="+challenge+"&msg=Transaction+not+yet+confirmed")
			return
		}
		c.JSON(http.StatusOK, gin.H{"confirmed": false})
	})

	// Account page — shows visor data for logged-in user
	r.GET("/account", func(c *gin.Context) {
		if !loginEnabled {
			c.Redirect(http.StatusFound, "/login?msg=Login+is+not+currently+available")
			return
		}
		sessionID, err := c.Cookie("session")
		if err != nil {
			c.Redirect(http.StatusFound, "/login?msg=Please+log+in")
			return
		}

		sessionsMu.RLock()
		sess, ok := sessions[sessionID]
		sessionsMu.RUnlock()

		if !ok || time.Now().After(sess.ExpiresAt) {
			if ok {
				sessionsMu.Lock()
				delete(sessions, sessionID)
				sessionsMu.Unlock()
			}
			c.Redirect(http.StatusFound, "/login?msg=Session+expired")
			return
		}

		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Writer.WriteHeader(http.StatusOK)

		l := loginPageHeader("Account")
		l += navlinks
		l += "<h1>Account</h1>"
		l += "<p>Logged in as: <code>" + sess.Address + "</code></p>"
		l += fmt.Sprintf("<p>Visors: %d</p>", len(sess.Visors))
		l += "<ul>"
		for _, v := range sess.Visors {
			l += "<li><a href='/account/survey/" + v + "'><code>" + v + "</code></a></li>"
		}
		l += "</ul>"
		l += "<p><a href='/logout'>Logout</a></p>"
		l += "</body></html>"
		c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
	})

	// Authenticated survey endpoint — only serves surveys for visors in the user's session
	r.GET("/account/survey/:pk", func(c *gin.Context) {
		sessionID, err := c.Cookie("session")
		if err != nil {
			c.Redirect(http.StatusFound, "/login?msg=Please+log+in")
			return
		}

		sessionsMu.RLock()
		sess, ok := sessions[sessionID]
		sessionsMu.RUnlock()

		if !ok || time.Now().After(sess.ExpiresAt) {
			c.Redirect(http.StatusFound, "/login?msg=Session+expired")
			return
		}

		pk := c.Param("pk")

		// Verify this PK belongs to the logged-in user's visors
		authorized := false
		for _, v := range sess.Visors {
			if v == pk {
				authorized = true
				break
			}
		}
		if !authorized {
			c.JSON(http.StatusForbidden, gin.H{"error": "not authorized for this visor"})
			return
		}

		surveyPath := filepath.Join(backupsDir, pk, "node-info.json")
		surveyData, err := os.ReadFile(surveyPath) //nolint:gosec
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "survey not available"})
			return
		}

		// Serve as JSON
		c.Data(http.StatusOK, "application/json", surveyData)
	})

	// Logout
	r.GET("/logout", func(c *gin.Context) {
		sessionID, err := c.Cookie("session")
		if err == nil {
			sessionsMu.Lock()
			delete(sessions, sessionID)
			sessionsMu.Unlock()
		}
		c.SetCookie("session", "", -1, "/", "", false, true)
		c.Redirect(http.StatusFound, "/login?info=Logged+out")
	})
}

func loginPageHeader(title string) string {
	return "<html><head><title>" + title + " - Skywire Rewards</title>" +
		"<style type='text/css'>a { color: #3399FF; } a:visited { color: #FF00FF; }" +
		"body { background-color: black; color: white; font-family: monospace; max-width: 600px; margin: 40px auto; padding: 0 20px; }" +
		"input[type=text] { width: 100%; padding: 8px; font-family: monospace; font-size: 14px; background: #222; color: white; border: 1px solid #444; margin: 10px 0; }" +
		"button { padding: 8px 20px; font-family: monospace; font-size: 14px; background: #36A2EB; color: white; border: none; cursor: pointer; }" +
		"button:hover { background: #2888CC; }" +
		"code { background: #222; padding: 2px 6px; }" +
		".error { color: #FF6384; }" +
		".info { color: #4BC0C0; }" +
		"</style></head><body>"
}
