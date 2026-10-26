// Package clitp cmd/skywire-cli/commands/tp/tp-uptime-archive.go c4-vis-cli
//
// `skywire cli tp uptime archive` reads the per-transport uptime timelines
// TPD archives to disk once a day has settled.
package clitp

import (
	"bytes"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	internal "github.com/skycoin/skywire/cmd/skywire-cli/cliutil"
	store "github.com/skycoin/skywire/pkg/deployment/tpd/store"
)

var (
	tpArchiveDir      string
	tpArchiveIDs      []string
	tpArchiveTimeline bool
)

func init() {
	tpUptimeArchiveCmd.Flags().StringVarP(&tpArchiveDir, "dir", "d", "/var/lib/skywire/tpd/bandwidth/tp-uptime", "TPD's timeline archive directory, for a date argument")
	tpUptimeArchiveCmd.Flags().StringSliceVar(&tpArchiveIDs, "ids", nil, "only these transport IDs (comma-separated UUIDs)")
	tpUptimeArchiveCmd.Flags().BoolVar(&tpArchiveTimeline, "timeline", false, "include the day's 288-slot timeline")
	tpUptimeArchiveCmd.Flags().Bool("json", false, "emit JSON")
	tpUptimeCmd.AddCommand(tpUptimeArchiveCmd)
}

// archivedUptime is one transport's archived day.
type archivedUptime struct {
	ID       uuid.UUID `json:"id"`
	Date     string    `json:"date"`
	Uptime   float64   `json:"uptime_pct"`
	Timeline string    `json:"timeline,omitempty"`
}

var tpUptimeArchiveCmd = &cobra.Command{
	Use:   "archive <YYYY-MM-DD | file.tptl>",
	Short: "read an archived day of per-transport uptime timelines",
	Long: `Read a day of per-transport uptime timelines from TPD's on-disk archive.

TPD keeps per-transport timelines in redis for 36 h and writes each settled
day to <store_data_path>/tp-uptime/<date>.tptl. Run this on the TPD host with
a date (looked up under --dir) or a file path.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := args[0]
		if !strings.HasSuffix(path, ".tptl") {
			path = store.TimelineArchivePath(tpArchiveDir, path)
		}
		a, err := store.OpenTimelineArchive(filepath.Clean(path))
		if err != nil {
			return err
		}
		defer a.Close() //nolint:errcheck
		out, err := readArchivedUptimes(a, tpArchiveIDs, tpArchiveTimeline)
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		for _, u := range out {
			fmt.Fprintf(&buf, "%s  %s  %6.2f%%\n", u.ID, u.Date, u.Uptime)
			if u.Timeline != "" {
				fmt.Fprintf(&buf, "  [%s]\n", u.Timeline)
			}
		}
		internal.PrintOutput(cmd.Flags(), out, buf.String())
		return nil
	},
}

func readArchivedUptimes(a *store.TimelineArchive, idStrs []string, timeline bool) ([]archivedUptime, error) {
	row := func(id uuid.UUID, bm []byte) archivedUptime {
		set := 0
		var line strings.Builder
		for slot := 0; slot < 288; slot++ {
			up := bm[slot/8]&(1<<uint(7-slot%8)) != 0
			if up {
				line.WriteByte('.')
			} else {
				line.WriteByte(' ')
			}
		}
		for _, b := range bm {
			set += bits.OnesCount8(b)
		}
		u := archivedUptime{ID: id, Date: a.Date, Uptime: float64(set) / 288 * 100}
		if timeline {
			u.Timeline = line.String()
		}
		return u
	}
	var out []archivedUptime
	if len(idStrs) == 0 {
		err := a.Each(func(r store.TimelineRecord) error {
			out = append(out, row(r.ID, r.Bitmap[:]))
			return nil
		})
		return out, err
	}
	ids := make([]uuid.UUID, 0, len(idStrs))
	for _, s := range idStrs {
		id, err := uuid.Parse(s)
		if err != nil {
			return nil, fmt.Errorf("transport ID %q: %w", s, err)
		}
		ids = append(ids, id)
	}
	got, err := a.Lookup(ids)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if bm, ok := got[id]; ok {
			out = append(out, row(id, bm))
		} else {
			fmt.Fprintf(os.Stderr, "%s: not in the %s archive\n", id, a.Date)
		}
	}
	return out, nil
}
