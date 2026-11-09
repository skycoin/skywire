// Package visor pkg/visor/hypervisor_known_visors.go c3-vis-core
package visor

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// knownVisorsFile sits beside the hypervisor's users.db.
const knownVisorsFile = "known-visors.json"

// knownVisor is the part of a visor's last summary the hypervisor keeps
// across restarts, so a visor that is offline at startup still shows.
type knownVisor struct {
	PK       cipher.PubKey `json:"pk"`
	Hostname string        `json:"hostname,omitempty"`
	PublicIP string        `json:"public_ip,omitempty"`
	LocalIP  string        `json:"local_ip,omitempty"`
	Country  string        `json:"country_code,omitempty"`
	Version  string        `json:"version,omitempty"`
	LastSeen time.Time     `json:"last_seen"`
}

func (hv *Hypervisor) knownVisorsPath() string {
	if hv.c.DBPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(hv.c.DBPath), knownVisorsFile)
}

// snapshotKnownVisors lists every remote visor in the summary cache.
// LastSeen is cut to the minute so the file changes at most once a minute.
func (hv *Hypervisor) snapshotKnownVisors() []knownVisor {
	hv.summaryCacheMx.RLock()
	out := make([]knownVisor, 0, len(hv.summaryCache))
	for pk, c := range hv.summaryCache {
		kv := knownVisor{PK: pk, LastSeen: c.seenAt.UTC().Truncate(time.Minute)}
		if c.sum != nil && c.sum.Overview != nil {
			ov := c.sum.Overview
			kv.Hostname, kv.PublicIP, kv.LocalIP, kv.Country = ov.Hostname, ov.PublicIP, ov.LocalIP, ov.CountryCode
			if ov.BuildInfo != nil {
				kv.Version = ov.BuildInfo.Version
			}
		}
		out = append(out, kv)
	}
	hv.summaryCacheMx.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].PK.Hex() < out[j].PK.Hex() })
	return out
}

// saveKnownVisors writes the known visor list when it changed since the
// last write. Errors are logged; a read-only or browser filesystem just
// loses retention across restarts.
func (hv *Hypervisor) saveKnownVisors() {
	path := hv.knownVisorsPath()
	if path == "" {
		return
	}
	b, err := json.MarshalIndent(hv.snapshotKnownVisors(), "", "  ")
	if err != nil {
		return
	}
	hv.knownMu.Lock()
	defer hv.knownMu.Unlock()
	if bytes.Equal(b, hv.knownWritten) {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		hv.logger.WithError(err).Debug("Failed to write known visors")
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		hv.logger.WithError(err).Debug("Failed to write known visors")
		return
	}
	hv.knownWritten = b
}

// loadKnownVisors seeds the summary cache from the known visor file, so
// the node list and hv ls show those visors as offline until they
// reconnect.
func (hv *Hypervisor) loadKnownVisors() {
	path := hv.knownVisorsPath()
	if path == "" {
		return
	}
	b, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return
	}
	var list []knownVisor
	if err := json.Unmarshal(b, &list); err != nil {
		hv.logger.WithError(err).Warn("Ignoring unreadable known visors file")
		return
	}
	hv.summaryCacheMx.Lock()
	for _, kv := range list {
		if kv.PK.Null() {
			continue
		}
		if _, ok := hv.summaryCache[kv.PK]; ok {
			continue
		}
		hv.summaryCache[kv.PK] = cachedSummary{sum: knownVisorSummary(kv), seenAt: kv.LastSeen}
	}
	hv.summaryCacheMx.Unlock()
	hv.knownMu.Lock()
	hv.knownWritten = b
	hv.knownMu.Unlock()
}

func knownVisorSummary(kv knownVisor) *visorapi.Summary {
	return &visorapi.Summary{
		Overview: &visorapi.Overview{
			PubKey:      kv.PK,
			Hostname:    kv.Hostname,
			PublicIP:    kv.PublicIP,
			LocalIP:     kv.LocalIP,
			CountryCode: kv.Country,
			BuildInfo:   &buildinfo.Info{Version: kv.Version},
		},
	}
}

// ForgetVisor drops a remembered visor. A visor that is still connected
// cannot be forgotten, it would reappear on the next poll.
func (hv *Hypervisor) ForgetVisor(pk cipher.PubKey) error {
	hv.mu.RLock()
	_, connected := hv.remoteVisors[pk]
	hv.mu.RUnlock()
	if connected {
		return fmt.Errorf("visor %s is connected, only offline visors can be forgotten", pk)
	}
	hv.summaryCacheMx.Lock()
	_, ok := hv.summaryCache[pk]
	delete(hv.summaryCache, pk)
	hv.summaryCacheMx.Unlock()
	if !ok {
		return fmt.Errorf("visor %s is not known to this hypervisor", pk)
	}
	hv.saveKnownVisors()
	return nil
}

// offlineKnownEntries returns an offline listing row for every known visor
// that is not in skip.
func (hv *Hypervisor) offlineKnownEntries(skip map[cipher.PubKey]bool) []visorapi.HVVisorEntry {
	hv.summaryCacheMx.RLock()
	defer hv.summaryCacheMx.RUnlock()
	var out []visorapi.HVVisorEntry
	for pk, c := range hv.summaryCache {
		if skip[pk] || c.sum == nil || c.sum.Overview == nil || c.sum.Overview.BuildInfo == nil {
			continue
		}
		e := visorapi.HVVisorEntry{PK: pk}
		populateEntryFromSummary(&e, c.sum)
		seen := c.seenAt
		e.LastSeenAt = &seen
		e.Online = time.Since(seen) < cacheFreshWindow
		if !e.Online {
			e.Uptime = 0
			e.Error = "offline " + time.Since(seen).Truncate(time.Minute).String()
		}
		out = append(out, e)
	}
	return out
}

// HVForgetVisor drops an offline visor from this hypervisor's remembered list.
func (v *Visor) HVForgetVisor(pk cipher.PubKey) error {
	if v.hvInstance == nil {
		return fmt.Errorf("hypervisor not running")
	}
	return v.hvInstance.ForgetVisor(pk)
}

// deleteKnownVisor is DELETE /api/visors/{pk}/known: forget an offline visor.
func (hv *Hypervisor) deleteKnownVisor() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		pk, err := pkFromParam(r, "pk")
		if err != nil {
			httputil.WriteJSON(w, r, http.StatusBadRequest, err)
			return
		}
		if hv.useCsrf() {
			if err := verifyCSRFToken(r.Header.Get(CSRFHeaderName)); err != nil {
				httputil.WriteJSON(w, r, http.StatusForbidden, err)
				return
			}
		}
		if err := hv.ForgetVisor(pk); err != nil {
			httputil.WriteJSON(w, r, http.StatusConflict, err)
			return
		}
		httputil.WriteJSON(w, r, http.StatusOK, true)
	}
}
