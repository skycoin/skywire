/*Package deprecated contains transport status*/
package deprecated

import (
	"github.com/google/uuid"
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
