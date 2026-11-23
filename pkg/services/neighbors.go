// Package services pkg/services/neighbors.go c3-vis-appsvc
//
// Services that share a process can read each other's records directly,
// rather than over dmsg from the same process. The host (a visor, or
// `skywire svc run`) hands each NeighborsUser a Neighbors before Run.
package services

// Neighbors finds the other services running in the same process.
type Neighbors interface {
	// NeighborByType returns the running service of type svcType, or nil
	// when none runs. A service under its own key may be restarted, so ask
	// again on each use rather than keeping the answer.
	NeighborByType(svcType string) Service
}

// NeighborsUser is a service that reads its neighbors' records.
type NeighborsUser interface {
	NeighborsSet(n Neighbors)
}

// NeighborsSetFor hands n to svc when svc reads its neighbors.
func NeighborsSetFor(svc Service, n Neighbors) {
	if u, ok := svc.(NeighborsUser); ok {
		u.NeighborsSet(n)
	}
}
