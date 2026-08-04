// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/survey_ingest.go c4-vis-cli
//
// Visor survey PUSH ingest. A visor POSTs its node-info survey to the reward
// system over dmsg instead of the reward system pulling it hourly. The survey is
// stored under the visor's DMSG-AUTHENTICATED source PK (RemoteAddr), so a visor
// can only ever write its own survey. The response tells the visor whether it is
// reward-eligible so the visor can surface that in the hypervisor UI (a rejected
// push becomes a red mark in the node-list reward column, distinct from the
// hyphen shown when no reward address is set).
//
// Storage target is log_backups/<pk>/node-info.json — the calc's authoritative
// source — so a pushed survey is available to the next calc run immediately,
// independent of the pull cycle's rsync (which matters because the pull is being
// reduced to a low-frequency fallback). The existing getlogs.sh age-out + the
// authoritative version prune over log_backups still apply.
package clirewardsserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	version "github.com/hashicorp/go-version"

	"github.com/skycoin/skywire/pkg/rewards"
)

// maxSurveyBytes caps a pushed survey. A real node-info survey is a few KB; this
// bounds a misbehaving/hostile sender.
const maxSurveyBytes = 512 * 1024

// surveyPushMinVersion, when non-empty, is the minimum skywire version a pushed
// survey may report to be stored (reward version-eligibility gate). Empty = accept
// any parseable version and let the authoritative getlogs.sh prune over log_backups
// enforce the live floor. Set via --survey-min-version so the operator can track the
// current reward floor and give the visor an accurate eligible/ineligible signal.
var surveyPushMinVersion string

// registerSurveyIngestRoutes wires the visor survey-PUSH endpoints onto r1.
func registerSurveyIngestRoutes(r1 *gin.Engine, wd string) {
	surveyDir := filepath.Join(wd, "log_backups")

	// POST /node-info — store the sender's own survey. Response contract:
	//   200 {"stored":true,"eligible":true}
	//   403 {"stored":false,"eligible":false,"reason":"..."}  (ineligible version)
	//   403 {"error":"..."}   (sender pushed a survey whose PK isn't its own)
	//   401/400/413 for auth / malformed / oversized.
	r1.POST("/node-info", func(c *gin.Context) {
		remotePK := rewards.RemotePK(c)
		if remotePK.Null() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated: no dmsg source pk"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxSurveyBytes+1))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "read body"})
			return
		}
		if len(body) > maxSurveyBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "survey too large"})
			return
		}
		var meta struct {
			PubKey         string `json:"pk"`
			SkywireVersion string `json:"skywire_version"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "invalid survey json"})
			return
		}
		// The survey must be the sender's own: its self-reported PK must equal the
		// dmsg-authenticated source PK. This is misbehavior, not an eligibility state.
		if !strings.EqualFold(meta.PubKey, remotePK.Hex()) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "survey pk does not match sender"})
			return
		}
		// Version gate: survey collection is the reward version-eligibility gate, so
		// an ineligible version is refused (and reported so the visor can show it).
		if surveyPushMinVersion != "" {
			if ok, reason := versionEligible(meta.SkywireVersion, surveyPushMinVersion); !ok {
				c.JSON(http.StatusForbidden, gin.H{"stored": false, "eligible": false, "reason": reason})
				return
			}
		}
		// Store under the AUTHENTICATED pk, atomically (tmp + rename) so a concurrent
		// reader/calc never sees a half-written survey.
		pkDir := filepath.Join(surveyDir, remotePK.Hex())
		if err := os.MkdirAll(pkDir, 0750); err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "store"})
			return
		}
		dst := filepath.Join(pkDir, "node-info.json")
		tmp := dst + ".tmp"
		if err := os.WriteFile(tmp, body, 0644); err != nil { //nolint:gosec // survey is world-readable like the pulled ones
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "write"})
			return
		}
		if err := os.Rename(tmp, dst); err != nil {
			_ = os.Remove(tmp) //nolint:errcheck
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "commit"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"stored": true, "eligible": true})
	})

	// GET /node-info/stored-checksum — the sha256 the reward system currently holds
	// for the REQUESTER's own survey (keyed by the authenticated pk), so a visor can
	// skip an unchanged push (conditional PUT). Empty sha256 = nothing stored yet.
	r1.GET("/node-info/stored-checksum", func(c *gin.Context) {
		remotePK := rewards.RemotePK(c)
		if remotePK.Null() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
			return
		}
		data, err := os.ReadFile(filepath.Join(surveyDir, remotePK.Hex(), "node-info.json")) //nolint:gosec
		if err != nil {
			c.JSON(http.StatusOK, gin.H{"sha256": ""})
			return
		}
		sum := sha256.Sum256(data)
		c.JSON(http.StatusOK, gin.H{"sha256": hex.EncodeToString(sum[:])})
	})
}

// versionEligible reports whether reported (the survey's skywire_version) is at
// least min. The build suffix (e.g. -abcdef1 or -dirty) is stripped, mirroring
// getlogs.sh. A bad min is treated as no gate (eligible); an unparseable reported
// version is ineligible.
func versionEligible(reported, min string) (bool, string) {
	base := strings.SplitN(reported, "-", 2)[0]
	rv, err := version.NewVersion(base)
	if err != nil {
		return false, "unparseable version " + reported
	}
	mv, err := version.NewVersion(min)
	if err != nil {
		return true, ""
	}
	if rv.LessThan(mv) {
		return false, "version " + base + " below reward floor " + min
	}
	return true, ""
}
