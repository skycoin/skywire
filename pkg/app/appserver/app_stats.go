package appserver

import (
	"time"
)

// AppStats contains app runtime statistics.
type AppStats struct {
	Connections []ConnectionSummary `json:"connections"`
	StartTime   *time.Time          `json:"start_time,omitempty"`
}
