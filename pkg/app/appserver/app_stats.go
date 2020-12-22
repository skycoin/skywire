package appserver

import (
	"time"
)

type AppStats struct {
	Connections []ConnectionSummary `json:"connections"`
	StartTime   *time.Time          `json:"start_time,omitempty"`
}
