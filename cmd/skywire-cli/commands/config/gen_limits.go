// Package cliconfig cmd/skywire-cli/commands/config/gen_limits.go
package cliconfig

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var memoryLimitRe = regexp.MustCompile(`(?i)^[0-9]+(\.[0-9]+)?\s*(b|kib|mib|gib|mb|gb)?$`)

// checkMemoryLimit accepts what the visor reads: none, auto or a size.
func checkMemoryLimit(s string) error {
	if s == "none" || s == "auto" || memoryLimitRe.MatchString(s) {
		return nil
	}
	return fmt.Errorf("--memory-limit %q: want none, auto or a size like 256MiB, 1GiB or 512MB", s)
}

// parseMaxPerType parses "sudph=40,webrtc=10" into a transport type to cap map.
func parseMaxPerType(s string) (map[string]int, error) {
	out := map[string]int{}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		t, n, ok := strings.Cut(pair, "=")
		t = strings.TrimSpace(t)
		c, err := strconv.Atoi(strings.TrimSpace(n))
		if !ok || t == "" || err != nil || c < 0 {
			return nil, fmt.Errorf("--autoconn-max-per-type %q: want type=count pairs with a count of 0 or more, like sudph=40,webrtc=10", pair)
		}
		out[t] = c
	}
	return out, nil
}

// applyAutoconnectVars sets the autoconnect limits that skywire.conf names.
// A limit it leaves unset (-1, or empty) keeps the value the config has.
func applyAutoconnectVars() error {
	if conf.Transport == nil {
		return nil
	}
	if autoconnMaxTotal < -1 || autoconnSiteLinks < -1 {
		return errors.New("--autoconn-max-total and --autoconn-site-links must be 0 or more")
	}
	a := &conf.Transport.Autoconnect
	if autoconnMaxTotal >= 0 {
		a.MaxTotal = autoconnMaxTotal
	}
	if autoconnSiteLinks >= 0 {
		n := autoconnSiteLinks
		a.SiteLinks = &n
	}
	if strings.TrimSpace(autoconnMaxPerType) != "" {
		m, err := parseMaxPerType(autoconnMaxPerType)
		if err != nil {
			return err
		}
		a.MaxPerType = m
	}
	return nil
}
