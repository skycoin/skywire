// Package netview pkg/visor/netview/netview.go c3-vis-core
// all-transports + UT uptimes aggregated per-PK), shared by the native visor
// (pkg/visor) and the wasm-visor (cmd/wasm-visor). It is a near-leaf — only the
// stdlib (encoding/json + strings + time) plus the wasm-safe tptypes leaf (the
// canonical transport-type list) — so the wasm-visor can import it; pkg/visor
// itself does NOT compile for js/wasm. Keeping the aggregation here means the
// native and browser visors can't drift on how the table is built.
package netview

import (
	"encoding/json"
	"strings"
	"time"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Entry is one row of the combined network table. JSON tags match what
// `cli sd --json` emits so callers (UI, scripts) consume both interchangeably.
type Entry struct {
	PK       string `json:"pk"`
	Country  string `json:"country,omitempty"`
	Version  string `json:"version,omitempty"`
	Services string `json:"services,omitempty"`
	STCPR    int    `json:"stcpr"`
	SUDPH    int    `json:"sudph"`
	DMSG     int    `json:"dmsg"`
	STCP     int    `json:"stcp"`
	SQUICR   int    `json:"squicr"`
	WEBRTC   int    `json:"webrtc"`
	SWSR     int    `json:"swsr"`
	SWTR     int    `json:"swtr"`
	Total    int    `json:"total"`
	UTStatus string `json:"ut_status,omitempty"` // "online" | "offline" | "" (not in UT)
}

// Response is the /api/network-view payload.
type Response struct {
	Entries   []Entry   `json:"entries"`
	FetchedAt time.Time `json:"fetched_at"`
}

// Compute aggregates SD (proxy/vpn/visor) + TPD all-transports + UT uptimes into
// the per-PK table, via the supplied deployment-service fetch (service ∈
// {sd,tpd,ut}, path → raw JSON). A partial failure on any source is treated as
// an empty slice — the table renders with whatever was reachable.
func Compute(fetch func(service, path string) ([]byte, error)) *Response {
	type sdEntry struct {
		Address string `json:"address"`
		Geo     struct {
			Country string `json:"country"`
		} `json:"geo"`
		Version string `json:"version"`
	}
	fetchSD := func(serviceType string) []sdEntry {
		body, err := fetch("sd", "/api/services?type="+serviceType)
		if err != nil {
			return nil
		}
		var es []sdEntry
		if err := json.Unmarshal(body, &es); err != nil {
			return nil
		}
		return es
	}

	type serviceInfo struct {
		DisplayPK string
		Country   string
		Version   string
		Services  []string
	}
	serviceMap := make(map[string]*serviceInfo)
	mergeIn := func(entries []sdEntry, kind string, withDisplayPK bool) {
		for _, e := range entries {
			pk := strings.Split(e.Address, ":")[0]
			s := serviceMap[pk]
			if s == nil {
				s = &serviceInfo{Country: e.Geo.Country, Version: e.Version}
				if withDisplayPK {
					s.DisplayPK = e.Address
				}
				serviceMap[pk] = s
			} else {
				if s.Country == "" {
					s.Country = e.Geo.Country
				}
				if s.Version == "" {
					s.Version = e.Version
				}
				if withDisplayPK && s.DisplayPK == "" {
					s.DisplayPK = e.Address
				}
			}
			s.Services = append(s.Services, kind)
		}
	}
	mergeIn(fetchSD("proxy"), "proxy", false)
	mergeIn(fetchSD("vpn"), "vpn", false)
	mergeIn(fetchSD("visor"), "visor", true)

	// UT — uptimes?v=v2 → [{pk, on}, ...]
	utStatus := make(map[string]string)
	if body, err := fetch("ut", "/uptimes?v=v2"); err == nil {
		var es []struct {
			PK string `json:"pk"`
			On bool   `json:"on"`
		}
		if json.Unmarshal(body, &es) == nil {
			for _, ut := range es {
				if ut.On {
					utStatus[ut.PK] = "online"
				} else {
					utStatus[ut.PK] = "offline"
				}
			}
		}
	}

	// TPD — all-transports → count by type per edge. Every canonical transport
	// type (tptypes.Known()) gets its own counter; NormalizeType folds legacy
	// wire names (quic/squic→squicr, ws→swsr, wt→swtr) into the canonical bucket
	// so the breakdown never silently drops a type as new ones are added.
	type tpCount struct{ STCPR, SUDPH, DMSG, STCP, SQUICR, WEBRTC, SWSR, SWTR, Total int }
	tpMap := make(map[string]*tpCount)
	if body, err := fetch("tpd", "/all-transports"); err == nil {
		var tps []struct {
			Edges []string `json:"edges"`
			Type  string   `json:"type"`
		}
		if json.Unmarshal(body, &tps) == nil {
			for _, tp := range tps {
				for _, edge := range tp.Edges {
					if tpMap[edge] == nil {
						tpMap[edge] = &tpCount{}
					}
					switch tptypes.NormalizeType(tptypes.Type(tp.Type)) {
					case tptypes.STCPR:
						tpMap[edge].STCPR++
					case tptypes.SUDPH:
						tpMap[edge].SUDPH++
					case tptypes.DMSG:
						tpMap[edge].DMSG++
					case tptypes.STCP:
						tpMap[edge].STCP++
					case tptypes.QUIC:
						tpMap[edge].SQUICR++
					case tptypes.WEBRTC:
						tpMap[edge].WEBRTC++
					case tptypes.WS:
						tpMap[edge].SWSR++
					case tptypes.WT:
						tpMap[edge].SWTR++
					}
					tpMap[edge].Total++
				}
			}
		}
	}

	out := make([]Entry, 0, len(serviceMap)+16)
	for pk, info := range serviceMap {
		c := tpMap[pk]
		if c == nil {
			c = &tpCount{}
		}
		display := pk
		if info.DisplayPK != "" {
			display = info.DisplayPK
		}
		out = append(out, Entry{
			PK: display, Country: info.Country, Version: info.Version,
			Services: strings.Join(info.Services, ","),
			STCPR:    c.STCPR, SUDPH: c.SUDPH, DMSG: c.DMSG, STCP: c.STCP,
			SQUICR: c.SQUICR, WEBRTC: c.WEBRTC, SWSR: c.SWSR, SWTR: c.SWTR, Total: c.Total,
			UTStatus: utStatus[pk],
		})
	}
	for pk, c := range tpMap {
		if serviceMap[pk] != nil {
			continue
		}
		out = append(out, Entry{
			PK: pk, STCPR: c.STCPR, SUDPH: c.SUDPH, DMSG: c.DMSG, STCP: c.STCP,
			SQUICR: c.SQUICR, WEBRTC: c.WEBRTC, SWSR: c.SWSR, SWTR: c.SWTR,
			Total: c.Total, UTStatus: utStatus[pk],
		})
	}

	// Sort: most transports first, PK tiebreak (stable across refetches).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && lessEntry(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return &Response{Entries: out, FetchedAt: time.Now()}
}

func lessEntry(a, b Entry) bool {
	if a.Total != b.Total {
		return a.Total > b.Total
	}
	return a.PK < b.PK
}
