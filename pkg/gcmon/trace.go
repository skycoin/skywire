package gcmon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// TraceLine is one row of the trace, with deltas against the previous sample.
type TraceLine struct {
	Time         time.Time `json:"time"`
	CyclesDelta  uint64    `json:"cycles_delta"`
	PauseSDelta  float64   `json:"pause_seconds_delta"`
	LiveBytes    uint64    `json:"live_bytes"`
	GoalBytes    uint64    `json:"goal_bytes"`
	ObjectsBytes uint64    `json:"heap_objects_bytes"`
	RSSBytes     uint64    `json:"rss_bytes"`
	Goroutines   uint64    `json:"goroutines"`
}

// Trace returns the ring as rows, oldest first. A metric that was absent is 0.
func (s *Sampler) Trace() []TraceLine {
	samples := s.Samples()
	out := make([]TraceLine, len(samples))
	for i := range samples {
		cur := &samples[i]
		l := TraceLine{
			Time: cur.At, LiveBytes: cur.Live, GoalBytes: cur.Goal,
			ObjectsBytes: cur.Objects, RSSBytes: cur.RSS, Goroutines: cur.Goroutines,
		}
		if i > 0 {
			prev := &samples[i-1]
			if cur.Cycles >= prev.Cycles {
				l.CyclesDelta = cur.Cycles - prev.Cycles
			}
			if cur.CPUPause >= prev.CPUPause {
				l.PauseSDelta = cur.CPUPause - prev.CPUPause
			}
		}
		out[i] = l
	}
	return out
}

// Handler serves the trace as plain text, or as JSON with ?json=1.
func Handler(s func() *Sampler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lines := s().Trace()
		if r.URL.Query().Get("json") == "1" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(lines) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "# time cycles_delta pause_seconds_delta live_bytes goal_bytes heap_objects_bytes rss_bytes goroutines") //nolint:errcheck
		for _, l := range lines {
			fmt.Fprintf(w, "%s %d %.6f %d %d %d %d %d\n", l.Time.UTC().Format(time.RFC3339), //nolint:errcheck
				l.CyclesDelta, l.PauseSDelta, l.LiveBytes, l.GoalBytes, l.ObjectsBytes, l.RSSBytes, l.Goroutines)
		}
	})
}

// DefaultHandler serves the process wide sampler.
func DefaultHandler() http.Handler { return Handler(Default) }
