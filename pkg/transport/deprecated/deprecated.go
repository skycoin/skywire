package deprecated

import (
	"github.com/google/uuid"
	"github.com/skycoin/skywire/pkg/transport"
)

// Status represents the current state of a Transport from a Transport's single edge.
// Each Transport will have two perspectives; one from each of it's edges.
type Status struct {

	// ID is the Transport ID that identifies the Transport that this status is regarding.
	ID uuid.UUID `json:"t_id"`

	// IsUp represents whether the Transport is up.
	// A Transport that is down will fail to forward Packets.
	IsUp bool `json:"is_up"`
}

// EntryWithStatus stores Entry and Statuses returned by both Edges.
type EntryWithStatus struct {
	Entry      *transport.Entry `json:"entry"`
	IsUp       bool             `json:"is_up"`
	Registered int64            `json:"registered"`
	Updated    int64            `json:"updated"`
	Statuses   [2]bool          `json:"statuses"`
}
