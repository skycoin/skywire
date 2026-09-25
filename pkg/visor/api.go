// Package visor pkg/visor/api.go c3-vis-core
package visor

import (
	"context"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// HealthCheckable resource returns its health status as an integer
// that corresponds to HTTP status code returned from the resource
// 200 codes correspond to a healthy resource
type HealthCheckable interface {
	Health(ctx context.Context) (int, error)
}

// internalHealthInfo contains information of the status of the visor itself.
// It's thread-safe, and could be used in multiple goroutines
type internalHealthInfo int32

// newHealthInfo creates
func newInternalHealthInfo() *internalHealthInfo {
	return new(internalHealthInfo)
}

// init sets the internalHealthInfo status to initial value (2)
func (h *internalHealthInfo) init() {
	atomic.StoreInt32((*int32)(h), 2)
}

// set sets the internalHealthInfo status to true.
func (h *internalHealthInfo) set() {
	atomic.StoreInt32((*int32)(h), 1)
}

// unset sets the internalHealthInfo to false.
func (h *internalHealthInfo) unset() {
	atomic.StoreInt32((*int32)(h), 0)
}

// value gets the internalHealthInfo value
func (h *internalHealthInfo) value() string {
	val := atomic.LoadInt32((*int32)(h))
	switch val {
	case 0:
		return "connecting"
	case 1:
		return "healthy"
	default:
		return "connecting"
	}
}

// TPSHealthCheckArgs is empty input for health check.
type TPSHealthCheckArgs struct{}

// TPSHealthCheckReply is the health check response.
type TPSHealthCheckReply struct {
	Status string
}

// TPSSetupRequest is input for AddTransport RPC via external TPS.
type TPSSetupRequest struct {
	TargetPK cipher.PubKey
	RemotePK cipher.PubKey
	Type     string
}

// TPSSetupResponse is the response for AddTransport via external TPS.
type TPSSetupResponse struct {
	ID     uuid.UUID
	Local  cipher.PubKey
	Remote cipher.PubKey
	Type   string
}

// TPSGetTransportsRequest is input for GetTransports RPC via external TPS.
type TPSGetTransportsRequest struct {
	TargetPK cipher.PubKey
}

// TPSGetTransportsResponse is the response for GetTransports via external TPS.
type TPSGetTransportsResponse struct {
	Transports []TPSSetupResponse
}
