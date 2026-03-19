// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/login.go
package clirewardsserver

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// session represents an authenticated user session
type session struct {
	Address   string    `json:"address"`
	Visors    []string  `json:"visors"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

var (
	sessions   = make(map[string]*session)
	sessionsMu sync.RWMutex
)

func generateSessionID() string {
	b := make([]byte, 32)
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

// registerLoginRoutes adds login-related routes to the gin router.
func registerLoginRoutes(r *gin.Engine, wd string) {
	backupsDir := filepath.Join(wd, "log_backups")

	// Login page
	r.GET("/login", func(c *gin.Context) {
		c.Writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		c.Writer.WriteHeader(http.StatusOK)

		l := "<html><head><title>Login - Skywire Rewards</title>"
		l += "<style type='text/css'>a { color: #3399FF; } a:visited { color: #FF00FF; }"
		l += "body { background-color: black; color: white; font-family: monospace; max-width: 600px; margin: 40px auto; padding: 0 20px; }"
		l += "input[type=text] { width: 100%; padding: 8px; font-family: monospace; font-size: 14px; background: #222; color: white; border: 1px solid #444; margin: 10px 0; }"
		l += "button { padding: 8px 20px; font-family: monospace; font-size: 14px; background: #36A2EB; color: white; border: none; cursor: pointer; }"
		l += "button:hover { background: #2888CC; }"
		l += ".error { color: #FF6384; }"
		l += ".info { color: #4BC0C0; }"
		l += "</style></head><body>"
		l += navlinks
		l += "<h1>Login</h1>"
		l += "<p>Enter your reward address or xpub key to view your visor data.</p>"
		l += "<form method='POST' action='/login'>"
		l += "<input type='text' name='address' placeholder='Skycoin address or xpub key' required>"
		l += "<br><button type='submit'>Look up</button>"
		l += "</form>"

		// Check for message from redirect
		if msg := c.Query("msg"); msg != "" {
			l += "<p class='error'>" + strings.ReplaceAll(msg, "<", "&lt;") + "</p>"
		}
		if msg := c.Query("info"); msg != "" {
			l += "<p class='info'>" + strings.ReplaceAll(msg, "<", "&lt;") + "</p>"
		}

		l += "</body></html>"
		c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
	})

	// Login POST — look up address in surveys
	r.POST("/login", func(c *gin.Context) {
		address := strings.TrimSpace(c.PostForm("address"))
		if address == "" {
			c.Redirect(http.StatusFound, "/login?msg=Please+enter+an+address+or+xpub+key")
			return
		}

		visors := findVisorsByRewardAddress(backupsDir, address)
		if len(visors) == 0 {
			c.Redirect(http.StatusFound, "/login?msg=No+visors+found+for+this+address")
			return
		}

		// Create session
		sessionID := generateSessionID()
		sessionsMu.Lock()
		sessions[sessionID] = &session{
			Address:   address,
			Visors:    visors,
			CreatedAt: time.Now(),
			ExpiresAt: time.Now().Add(24 * time.Hour),
		}
		sessionsMu.Unlock()

		// Set session cookie
		c.SetCookie("session", sessionID, 86400, "/", "", false, true)
		c.Redirect(http.StatusFound, "/account")
	})

	// Account page — shows visor data for logged-in user
	r.GET("/account", func(c *gin.Context) {
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

		l := "<html><head><title>Account - Skywire Rewards</title>"
		l += "<style type='text/css'>a { color: #3399FF; } a:visited { color: #FF00FF; }"
		l += "body { background-color: black; color: white; font-family: monospace; max-width: 900px; margin: 40px auto; padding: 0 20px; }"
		l += "pre { white-space: pre-wrap; word-wrap: break-word; }"
		l += ".visor-section { border: 1px solid #333; padding: 15px; margin: 15px 0; }"
		l += "</style></head><body>"
		l += navlinks

		shortAddr := sess.Address
		if len(shortAddr) > 20 {
			shortAddr = shortAddr[:20] + "..."
		}
		l += "<h1>Account</h1>"
		l += "<p>Reward address: <code>" + sess.Address + "</code></p>"
		l += fmt.Sprintf("<p>%d visor(s) found</p>", len(sess.Visors))
		l += "<p><a href='/logout'>Logout</a></p>"
		l += "<hr>"

		// Show each visor's survey data
		for _, pk := range sess.Visors {
			l += "<div class='visor-section'>"
			l += "<h3>Visor: <a href='/log-collection/tree/" + pk + "'>" + pk[:16] + "...</a></h3>"

			surveyPath := filepath.Join(backupsDir, pk, "node-info.json")
			data, err := os.ReadFile(surveyPath) //nolint:gosec
			if err != nil {
				l += "<p>Survey not available</p>"
			} else {
				// Parse and display key fields
				var survey map[string]interface{}
				if json.Unmarshal(data, &survey) == nil {
					if v, ok := survey["skywire_version"]; ok {
						l += fmt.Sprintf("<p>Version: %v</p>", v)
					}
					if v, ok := survey["go_arch"]; ok {
						l += fmt.Sprintf("<p>Architecture: %v</p>", v)
					}
					if v, ok := survey["ip_address"]; ok {
						l += fmt.Sprintf("<p>IP: %v</p>", v)
					}
				}
			}

			// Show health info if available
			healthPath := filepath.Join(backupsDir, pk, "health.json")
			healthData, err := os.ReadFile(healthPath) //nolint:gosec
			if err == nil {
				var health map[string]interface{}
				if json.Unmarshal(healthData, &health) == nil {
					if bi, ok := health["build_info"].(map[string]interface{}); ok {
						if v, ok := bi["version"]; ok {
							l += fmt.Sprintf("<p>Build version: %v</p>", v)
						}
					}
				}
			}

			l += "</div>"
		}

		l += "</body></html>"
		c.Writer.Write([]byte(l)) //nolint:errcheck,gosec
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
