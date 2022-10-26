// Package routing pkg/routing/failure.go
package routing

import (
	"fmt"
)

// Failure describes a routing failure.
type Failure struct {
	Code FailureCode `json:"code"`
	Msg  string      `json:"msg"`
}

func (f Failure) Error() string {
	return fmt.Sprintf("failure code %d (%s)", f.Code, f.Msg)
}

// FailureCode is a code that indicates a reason why a failure happened.
type FailureCode byte

// Failure codes
const (
	FailureUnknown FailureCode = iota
	FailureAddRules
	FailureCreateRoutes
	FailureRoutesCreated
	FailureReserveRtIDs
)

func (fc FailureCode) String() string {
	switch fc {
	case FailureUnknown:
		return "FailureUnknown"
	case FailureAddRules:
		return "FailureAddRules"
	case FailureCreateRoutes:
		return "FailureCreateRoutes"
	case FailureRoutesCreated:
		return "FailureRoutesCreated"
	case FailureReserveRtIDs:
		return "FailureReserveRtIDs"
	default:
		return fmt.Sprintf("unknown(%d)", fc)
	}
}
