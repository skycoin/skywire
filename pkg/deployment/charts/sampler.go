package charts

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
)

// Interval is how often a service samples its counts.
const Interval = 5 * time.Minute

// Collect reads the current value of every series. It returns an error when
// the counts cannot be trusted yet, and that sample is skipped rather than
// drawn as a dip.
type Collect func(ctx context.Context) (map[string]float64, error)

// Run samples on every Interval boundary until ctx ends.
func Run(ctx context.Context, st Store, collect Collect, log logrus.FieldLogger) {
	for {
		now := time.Now()
		next := now.Truncate(Interval).Add(Interval)
		select {
		case <-ctx.Done():
			return
		case <-time.After(next.Sub(now)):
		}
		v, err := collect(ctx)
		if err != nil {
			log.WithError(err).Debug("charts: sample skipped")
			continue
		}
		if err := st.Add(ctx, Sample{At: next.UTC(), V: v}); err != nil {
			log.WithError(err).Warn("charts: could not store sample")
		}
	}
}
