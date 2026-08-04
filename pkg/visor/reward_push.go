// Package visor pkg/visor/reward_push.go c3-vis-core
//
// Visor-side survey PUSH: periodically send this visor's node-info survey to the
// reward system over dmsg, so reward eligibility no longer depends on the reward
// system pulling the survey on its own (online-gated, silent-drop-prone) schedule.
//
// Design: a conditional PUT. Each cycle the visor GETs the sha256 the reward
// system currently holds for its PK (/node-info/stored-checksum, scoped to the
// authenticated sender) and only POSTs the full survey when it differs — so a
// steady-state survey costs one tiny checksum round-trip, and a changed survey (or
// one the reward system has lost/aged-out) is re-sent. The survey regenerates ~daily
// (a fresh timestamp), which naturally yields a daily re-assert. The reward system's
// accept/reject verdict is recorded in v.survey so the hypervisor UI can show reward
// eligibility (a rejected push → red mark, distinct from the hyphen for no reward
// address). No-op until a reward address is set and reward_system_dmsg is configured.
package visor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/dmsg/dmsghttp"
	"github.com/skycoin/skywire/pkg/logging"
)

const (
	// rewardPushInterval is how often the visor checks whether its survey needs
	// (re-)pushing. The per-cycle cost is a checksum GET; a full POST happens only on
	// a real change, so this can be frequent without being wasteful.
	rewardPushInterval = time.Hour
	// rewardPushWarmup delays the first attempt so dmsg + the first survey generation
	// have a chance to complete after boot.
	rewardPushWarmup = 2 * time.Minute
	// rewardPushHTTPTimeout bounds one checksum GET or survey POST over dmsg.
	rewardPushHTTPTimeout = 30 * time.Second
	// rewardPushMaxRespBytes caps the reward system's response body we read.
	rewardPushMaxRespBytes = 4096
)

// rewardSurveyPush runs the survey-push loop for the life of ctx (canceled on
// visor Suspend/close, so Resume starts a fresh loop rather than duplicating one).
func rewardSurveyPush(ctx context.Context, v *Visor, log *logging.Logger) {
	base := strings.TrimRight(v.conf.RewardSystemDmsg, "/")
	if !strings.HasPrefix(base, "dmsg://") {
		log.Debug("Reward survey push disabled: reward_system_dmsg not configured")
		return
	}
	checksumURL := base + "/node-info/stored-checksum"
	postURL := base + "/node-info"

	timer := time.NewTimer(rewardPushWarmup)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		v.pushSurveyOnce(ctx, log, checksumURL, postURL)
		timer.Reset(rewardPushInterval)
	}
}

// pushSurveyOnce runs one conditional-PUT cycle. Network errors are transient and
// logged at debug (they don't overwrite a previously-recorded eligibility verdict);
// only an actual reward-system response updates the verdict.
func (v *Visor) pushSurveyOnce(ctx context.Context, log *logging.Logger, checksumURL, postURL string) {
	if v.dmsgC == nil {
		return
	}
	// Snapshot the current survey. Empty PubKey/SkycoinAddress = no reward address
	// yet (survey not generated) — nothing to push.
	v.survey.mu.RLock()
	survey := v.survey.data
	v.survey.mu.RUnlock()
	if survey.PubKey.Null() || strings.TrimSpace(survey.SkycoinAddress) == "" {
		return
	}

	body, err := json.Marshal(survey)
	if err != nil {
		log.WithError(err).Warn("Reward survey push: failed to marshal survey")
		return
	}
	localHex := hex.EncodeToString(sha256Sum(body))

	client := &http.Client{
		Transport: dmsghttp.MakeHTTPTransport(ctx, v.dmsgC),
		Timeout:   rewardPushHTTPTimeout,
	}
	defer client.CloseIdleConnections()

	// Conditional PUT: skip the POST if the reward system already holds this exact
	// survey. A failure here (unreachable / no stored copy) just falls through to POST.
	if remoteHex, err := fetchStoredChecksum(ctx, client, checksumURL); err == nil && remoteHex != "" && remoteHex == localHex {
		log.Debug("Reward survey push: reward system already has current survey; skipping")
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, postURL, bytes.NewReader(body))
	if err != nil {
		log.WithError(err).Warn("Reward survey push: failed to build request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		log.WithError(err).Debug("Reward survey push: POST failed (transient)")
		return
	}
	defer resp.Body.Close()                                                      //nolint:errcheck
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, rewardPushMaxRespBytes)) //nolint:errcheck

	var r struct {
		Stored   bool   `json:"stored"`
		Eligible bool   `json:"eligible"`
		Reason   string `json:"reason"`
		Error    string `json:"error"`
	}
	_ = json.Unmarshal(respBody, &r) //nolint:errcheck

	v.survey.mu.Lock()
	v.survey.pushAttempted = true
	v.survey.pushAt = time.Now()
	switch {
	case resp.StatusCode == http.StatusOK && r.Eligible:
		v.survey.pushEligible = true
		v.survey.pushReason = ""
		log.Debug("Reward survey push: accepted (eligible)")
	case r.Reason != "":
		// The reward system explicitly judged this survey reward-ineligible
		// (e.g. version below floor). Surface it in the UI.
		v.survey.pushEligible = false
		v.survey.pushReason = r.Reason
		log.WithField("reason", r.Reason).Info("Reward survey push: rejected as ineligible")
	default:
		// A non-eligibility failure (pk mismatch, 5xx, malformed) — record the status
		// so the operator can see something is wrong, without a specific reason.
		v.survey.pushEligible = false
		if r.Error != "" {
			v.survey.pushReason = r.Error
		} else {
			v.survey.pushReason = fmt.Sprintf("reward system returned HTTP %d", resp.StatusCode)
		}
		log.WithField("status", resp.StatusCode).Warn("Reward survey push: not accepted")
	}
	v.survey.mu.Unlock()
}

// fetchStoredChecksum GETs the sha256 the reward system currently holds for the
// requesting visor's survey. Empty string (no error) means nothing is stored yet.
func fetchStoredChecksum(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("stored-checksum returned HTTP %d", resp.StatusCode)
	}
	var r struct {
		SHA256 string `json:"sha256"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, rewardPushMaxRespBytes)).Decode(&r); err != nil {
		return "", err
	}
	return r.SHA256, nil
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
