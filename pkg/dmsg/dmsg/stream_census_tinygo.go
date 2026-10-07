//go:build tinygo

package dmsg

type censusEntry struct{}

func censusTrack(*Stream, bool) {}

func (e *censusEntry) set(uint16, string) {}

// StreamCensusRow counts the streams still in memory that share a direction,
// a service port and a lifecycle state.
type StreamCensusRow struct {
	Initiator bool    `json:"initiator"`
	Port      uint16  `json:"port"`
	State     string  `json:"state"`
	Count     int     `json:"count"`
	OldestS   float64 `json:"oldest_s"`
}

// StreamCensus is empty under TinyGo, which has no weak pointers.
func StreamCensus() []StreamCensusRow { return nil }
