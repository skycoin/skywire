// Package clirewardsserver cmd/skywire-cli/commands/rewards/server/visorbw.go c5-reward-server
//
// Writes each settled day of TPD's per-visor bandwidth feed to
// hist/<date>_bandwidth.json, the file the reward calculation pays pool 2
// from. TPD reduces the day once it has settled, with transports between
// visors on one network left out (pkg/deployment/tpd/store/visorbw.go), so the
// file is final.
//
// A day already rewarded is not rewritten: its numbers must not change after
// the fact. The one exception is yesterday, which the reward run recalculates
// hourly until it is distributed.
package clirewardsserver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/cxosub"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	tpdstore "github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// visorBWWriteInterval is how often the feed is checked for a newly settled
// day. TPD publishes one within ~15 minutes of midnight UTC.
const visorBWWriteInterval = 10 * time.Minute

// startVisorBWWriter keeps hist/ supplied with settled days from TPD's
// per-visor bandwidth feed, for as long as the process runs.
func startVisorBWWriter(histDir string) {
	mgr := statsCXOMgr()
	if mgr == nil {
		return
	}
	mgr.Pin(cxosub.FeedTPDVisorBW)
	log := logging.MustGetLogger("reward-visorbw")
	go func() {
		for {
			writeSettledVisorBW(mgr, histDir, time.Now().UTC(), log)
			time.Sleep(visorBWWriteInterval)
		}
	}()
}

// visorBWSource is the part of the subscription manager the writer reads.
type visorBWSource interface {
	LastSync(feed cxosub.Feed) time.Time
	Walk(feed cxosub.Feed, prefix string, fn func(path string, body []byte) bool) bool
}

// writeSettledVisorBW writes every settled day on the feed that hist/ should
// have and does not.
func writeSettledVisorBW(src visorBWSource, histDir string, now time.Time, log *logging.Logger) {
	// A feed still on its first sync lists leaves whose bodies have not
	// arrived; nothing is read from it until that sync is done.
	if src.LastSync(cxosub.FeedTPDVisorBW).IsZero() {
		return
	}
	yesterday := now.AddDate(0, 0, -1).Format("2006-01-02")
	src.Walk(cxosub.FeedTPDVisorBW, tpdstore.VisorBWDayPrefix, func(path string, body []byte) bool {
		day, err := decodeVisorBWDay(path, body)
		if err != nil {
			log.WithError(err).WithField("leaf", path).Warn("Unusable per-visor bandwidth day; not writing it")
			return true
		}
		file := filepath.Join(histDir, day.Date+"_bandwidth.json")
		if haveVisorBW(file) {
			return true // already written from the feed
		}
		if day.Date != yesterday && fileExists(filepath.Join(histDir, day.Date+"_stats.txt")) {
			return true // already rewarded; its numbers stay as they were
		}
		out, err := json.MarshalIndent(day, "", "  ")
		if err == nil {
			err = writeFileAtomic(file, out)
		}
		if err != nil {
			log.WithError(err).WithField("file", file).Warn("Could not write per-visor bandwidth")
			return true
		}
		log.WithField("date", day.Date).WithField("visors", len(day.Visors)).
			WithField("same_network_excluded", day.SameNetworkExcluded).Info("Wrote settled per-visor bandwidth")
		return true
	})
}

// decodeVisorBWDay decodes a leaf and refuses one that could not be a real
// day: an empty day means an upstream with nothing to serve, not a day on
// which no visor moved a byte.
func decodeVisorBWDay(path string, body []byte) (*tpdstore.VisorBWDay, error) {
	var day tpdstore.VisorBWDay
	if err := json.Unmarshal(cxoutils.Gunzip(body), &day); err != nil {
		return nil, err
	}
	switch {
	case day.Version != tpdstore.VisorBWVersion:
		return nil, fmt.Errorf("version %d, want %d", day.Version, tpdstore.VisorBWVersion)
	case path != tpdstore.VisorBWDayPath(day.Date):
		return nil, fmt.Errorf("leaf holds day %q", day.Date)
	case len(day.Visors) == 0:
		return nil, fmt.Errorf("no visors")
	}
	return &day, nil
}

// haveVisorBW reports whether file already holds a day from the feed.
func haveVisorBW(file string) bool {
	b, err := os.ReadFile(file) //nolint:gosec
	if err != nil {
		return false
	}
	var v struct {
		Version int `json:"version"`
	}
	return json.Unmarshal(b, &v) == nil && v.Version == tpdstore.VisorBWVersion
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// writeFileAtomic writes via a temporary file and a rename, so the reward run
// never reads half a file.
func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
