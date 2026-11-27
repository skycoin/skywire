// Package rewards pkg/deployment/rewards/minversion.go c3-app-rewards
package rewards

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/go-version"
)

// MinVersionDelay is how long after its release a version becomes the
// minimum. See rewards/mainnet_rules.md, Version.
const MinVersionDelay = 14 * 24 * time.Hour

// Release is one published skywire release.
type Release struct {
	Tag        string    `json:"tag_name"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
}

// ReleasesFetch reads the skywire releases from GitHub.
func ReleasesFetch(ctx context.Context) ([]Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/skycoin/skywire/releases?per_page=100", nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github releases: status %d", resp.StatusCode)
	}
	var releases []Release
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}

// MinVersion is the minimum version at time at: the highest release
// published at least MinVersionDelay before it, or "" when there is none.
func MinVersion(releases []Release, at time.Time) string {
	cutoff := at.Add(-MinVersionDelay)
	var best *version.Version
	var bestTag string
	for _, r := range releases {
		if r.Draft || r.Prerelease || r.Published.IsZero() || r.Published.After(cutoff) {
			continue
		}
		v, err := version.NewVersion(r.Tag)
		if err != nil {
			continue
		}
		if best == nil || v.GreaterThan(best) {
			best, bestTag = v, r.Tag
		}
	}
	return bestTag
}

// VersionBelow is whether skywire version v, without any build suffix, is
// below minimum. A v that does not parse counts as below.
func VersionBelow(v, minimum string) bool {
	if i := strings.IndexAny(v, "-+ "); i > 0 {
		v = v[:i]
	}
	parsed, err := version.NewVersion(v)
	if err != nil {
		return true
	}
	floor, err := version.NewVersion(minimum)
	if err != nil {
		return false
	}
	return parsed.LessThan(floor)
}
