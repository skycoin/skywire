// Package clirewards cmd/skywire-cli/commands/rewards/cycle.go c4-vis-cli
//
// cycle.go keeps what a reward cycle learns between runs: the uptimes and
// bandwidth of a finished day are fetched once, and the outputs are written
// again only when a hash of every input of the calculation changes.
package clirewards

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/spf13/pflag"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/buildinfo"
	deprewards "github.com/skycoin/skywire/pkg/deployment/rewards"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

const (
	// defaultDayCloseDelay is how long after midnight UTC a day's uptimes
	// and bandwidth are taken as final.
	defaultDayCloseDelay = 15 * time.Minute
	// closeRetry spaces the attempts to collect a finished day's bandwidth.
	closeRetry = 10 * time.Minute
	// maxBandwidthDays is the widest ?days= window TPD answers.
	maxBandwidthDays = 35
)

// fetchUT and closeBW fetch a day's uptimes and its final bandwidth.
var (
	fetchUT = fetchAndSaveUT
	closeBW = closeBandwidth
)

// dayCycle is the state a reward cycle keeps from one run to the next.
type dayCycle struct {
	flags *pflag.FlagSet
	cfg   cycleConfig
	now   func() time.Time

	mu         sync.Mutex
	lastHash   map[string]string    // date to the inputs hash of the outputs on disk
	closeTried map[string]time.Time // date to the last attempt to collect its final bandwidth
}

// cycleResult is what one calculation run did.
type cycleResult struct {
	date    string
	hash    string
	skipped bool
	note    string
}

func newDayCycle(flags *pflag.FlagSet, c cycleConfig) *dayCycle {
	return &dayCycle{
		flags:      flags,
		cfg:        c,
		now:        time.Now,
		lastHash:   map[string]string{},
		closeTried: map[string]time.Time{},
	}
}

func (d *dayCycle) closeDelay() time.Duration {
	if d.cfg.dayCloseDelay > 0 {
		return d.cfg.dayCloseDelay
	}
	return defaultDayCloseDelay
}

// closedAt is when date's uptimes and bandwidth are final.
func (d *dayCycle) closedAt(date string) time.Time {
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return time.Time{}
	}
	return t.AddDate(0, 0, 1).Add(d.closeDelay())
}

// finalFile reports whether path was written after closedAt, so it holds the whole day.
func finalFile(path string, closedAt time.Time) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.ModTime().Before(closedAt)
}

// calcDay calculates o.date unless it was paid or its inputs are those of
// the outputs already written. Caller holds cycleMu.
func (d *dayCycle) calcDay(o rewardOpts) (cycleResult, error) {
	res := cycleResult{date: o.date}
	if _, err := os.Stat(filepath.Join(o.histDir, o.date+".txt")); err == nil {
		log.Infof("Transaction already broadcasted for %s; nothing to do", o.date)
		res.skipped, res.note = true, "paid"
		return res, nil
	}
	now := d.now()
	closed := d.closedAt(o.date)

	o.utfile = d.cfg.utFile
	if o.utfile == "" {
		o.utfile = filepath.Join(o.histDir, o.date+"_ut.txt")
		_, statErr := os.Stat(o.utfile)
		if !finalFile(o.utfile, closed) && (!now.Before(closed) || statErr != nil) {
			if err := fetchUT(d.flags, o.date, o.histDir); err != nil {
				if statErr != nil {
					return res, fmt.Errorf("UT fetch failed: %w", err)
				}
				log.WithError(err).Warnf("UT fetch failed; calculating from the uptimes already in %s", o.utfile)
			}
		}
	}

	bwFile := filepath.Join(o.histDir, o.date+"_bandwidth.json")
	if !d.cfg.skipCollect && !now.Before(closed) && !finalFile(bwFile, closed) {
		d.mu.Lock()
		tried := d.closeTried[o.date]
		due := now.Sub(tried) >= closeRetry
		if due {
			d.closeTried[o.date] = now
		}
		d.mu.Unlock()
		if due {
			if err := closeBW(o, now); err != nil {
				log.WithError(err).Warnf("Final bandwidth collection for %s failed; calculating from %s as it is", o.date, bwFile)
			}
		}
	}

	// Ask the chain afresh: what an address had not sent at the last look
	// it may have sent since.
	forgetUnspent()
	h, _, err := inputsHash(o)
	if err != nil {
		return res, fmt.Errorf("hash inputs: %w", err)
	}
	res.hash = h
	d.mu.Lock()
	same := d.lastHash[o.date] == h
	d.mu.Unlock()
	if same {
		log.Infof("Inputs for %s unchanged (%s); outputs kept", o.date, h[:12])
		res.skipped, res.note = true, "inputs unchanged"
		return res, nil
	}

	xpubIndex = nil
	r := calcRewards(o)
	if err := writeAllOutputs(r, o.histDir); err != nil {
		return res, fmt.Errorf("write outputs: %w", err)
	}
	if err := writePerPoolFiles(o.histDir, r.date, r.eligible, r.bwSatExp, r.minBW); err != nil {
		log.Warnf("Failed to write per-pool detail files: %v", err)
	}
	d.mu.Lock()
	d.lastHash[o.date] = h
	d.mu.Unlock()
	log.Infof("Reward cycle complete for %s (inputs %s)", r.date, h[:12])
	return res, nil
}

// closeBandwidth collects o.date's bandwidth after the day ended, so the
// file holds the whole day and not what was known at the last collection.
func closeBandwidth(o rewardOpts, now time.Time) error {
	t, err := time.Parse("2006-01-02", o.date)
	if err != nil {
		return err
	}
	days := int(now.UTC().Truncate(24*time.Hour).Sub(t)/(24*time.Hour)) + 1
	if days > maxBandwidthDays {
		return fmt.Errorf("%s is older than the %d days TPD answers", o.date, maxBandwidthDays)
	}
	log.Infof("Collecting the final bandwidth of %s (bw-collect --date %s --days %d)", o.date, o.date, days)
	h0, s0, d0, n0, c0 := histPath, bwSurveyPath, bwDate, bwDays, noCache
	histPath, bwSurveyPath, bwDate, bwDays, noCache = o.histDir, o.surveyPath, o.date, days, true
	defer func() { histPath, bwSurveyPath, bwDate, bwDays, noCache = h0, s0, d0, n0, c0 }()
	return collectBandwidth(bwCollectCmd.Flags(), logging.MustGetLogger("bw-collect"))
}

// surveyInputs are the survey fields calcRewards reads, as it reads them.
func surveyInputs(s *surveyData) ([]byte, error) {
	return json.Marshal(s)
}

// inputsHash hashes everything calcRewards reads for o.date: the binary, the
// options, the uptime, bandwidth and transport files, the surveys of the
// visors that met the uptime, and whether each of their single addresses has
// sent coins. singles are those addresses that had not.
func inputsHash(o rewardOpts) (sum string, singles []string, err error) {
	h := sha256.New()
	fmt.Fprintf(h, "%s %s\n", buildinfo.Version(), buildinfo.Commit()) //nolint:errcheck,gosec // a hash never fails a write
	h.Write(deployment.ServicesJSON)                                   //nolint:errcheck,gosec // a hash never fails a write
	fmt.Fprintf(h, "\n%+v\n", o)                                       //nolint:errcheck,gosec // a hash never fails a write
	for _, p := range []string{
		o.utfile,
		filepath.Join(o.histDir, o.date+"_bandwidth.json"),
		filepath.Join(o.histDir, o.date+"_transports.txt"),
	} {
		if err := hashFile(h, p); err != nil {
			return "", nil, err
		}
	}
	met, _, err := uptimeKeys(o.utfile, o.date)
	if err != nil {
		return "", nil, fmt.Errorf("read %s: %w", o.utfile, err)
	}
	day, _ := time.Parse("2006-01-02", o.date) //nolint:errcheck // checked by the caller
	for _, pk := range met {
		s, err := parseSurvey(filepath.Join(o.surveyPath, pk, "node-info.json"))
		if err != nil {
			fmt.Fprintf(h, "%s none\n", pk) //nolint:errcheck,gosec // a hash never fails a write
			continue
		}
		b, err := surveyInputs(s)
		if err != nil {
			return "", nil, err
		}
		fmt.Fprintf(h, "%s %s\n", pk, b) //nolint:errcheck,gosec // a hash never fails a write
		canonical, xpub, err := rewardconfig.ValidateRewardAddress(s.SkyAddr)
		if err != nil || xpub || day.Before(deprewards.SingleAddressCutoff) {
			continue
		}
		spent, err := addressSpent(canonical)
		if err != nil {
			return "", nil, fmt.Errorf("check whether reward address %s has sent coins (skycoin node %s): %w", canonical, skycoinNodeURL, err)
		}
		fmt.Fprintf(h, "%s spent=%t\n", canonical, spent) //nolint:errcheck,gosec // a hash never fails a write
		if !spent {
			singles = append(singles, canonical)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), singles, nil
}

func hashFile(h hash.Hash, path string) error {
	f, err := os.Open(path) //nolint:gosec // a path of the reward work dir
	if errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(h, "%s missing\n", filepath.Base(path)) //nolint:errcheck,gosec // a hash never fails a write
		return nil
	}
	if err != nil {
		return err
	}
	defer f.Close()                             //nolint:errcheck
	fmt.Fprintf(h, "%s\n", filepath.Base(path)) //nolint:errcheck,gosec // a hash never fails a write
	_, err = io.Copy(h, f)
	return err
}

// forgetUnspent drops the cached answers that an address had not sent
// coins. An address that has sent stays so.
func forgetUnspent() {
	spentCache.Lock()
	defer spentCache.Unlock()
	for a, spent := range spentCache.m {
		if !spent {
			delete(spentCache.m, a)
		}
	}
}
