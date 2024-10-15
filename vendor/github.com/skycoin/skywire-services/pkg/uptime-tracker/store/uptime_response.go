package store

import (
	"sort"
	"strconv"
	"time"
)

// OnlineThreshold is a value used for uptime calculation.
const OnlineThreshold = time.Minute * 6

// UptimeResponse is the tracker API response format for `/uptimes`.
type UptimeResponse []UptimeDef

// UptimeDef is the item of `UptimeResponse`.
type UptimeDef struct {
	Key     string `json:"key"`
	Online  bool   `json:"online"`
	Version string `json:"-"`
}

// UptimeResponseV2 is the tracker API response format v2 for `/uptimes`.
type UptimeResponseV2 []UptimeDefV2

// UptimeDefV2 is the item of `UptimeResponseV2`.
type UptimeDefV2 struct {
	Key                string            `json:"pk"`
	Online             bool              `json:"on"`
	Version            string            `json:"version,omitempty"`
	DailyOnlineHistory map[string]string `json:"daily,omitempty"`
}

func makeUptimeResponse(keys []string, lastTS map[string]string, versions map[string]string, callingErr error) (UptimeResponse, error) {
	if callingErr != nil {
		return UptimeResponse{}, callingErr
	}

	if len(keys) == 0 {
		return UptimeResponse{}, nil
	}

	response := make(UptimeResponse, 0)
	for _, pk := range keys {

		online := false
		ts, err := strconv.ParseInt(lastTS[pk], 10, 64)
		if err == nil {
			online = time.Unix(ts, 0).Add(OnlineThreshold).After(time.Now())
		}

		entry := UptimeDef{
			Key:     pk,
			Online:  online,
			Version: versions[pk],
		}

		response = append(response, entry)
	}

	sort.Slice(response, func(i, j int) bool {
		for k := 0; k < 33; k++ {
			if response[i].Key[k] != response[j].Key[k] {
				return response[i].Key[k] < response[j].Key[k]
			}
		}
		return true
	})

	return response, nil
}
