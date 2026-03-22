// Package router pkg/router/route_status.go
package router

import (
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// RouteStatus contains detailed status of a single active route connection,
// including all mux transports and live traffic statistics.
type RouteStatus struct {
	// Connection identity
	LocalPK    cipher.PubKey `json:"local_pk"`
	RemotePK   cipher.PubKey `json:"remote_pk"`
	LocalPort  routing.Port  `json:"local_port"`
	RemotePort routing.Port  `json:"remote_port"`

	// Status
	Alive   bool          `json:"alive"`
	Latency time.Duration `json:"latency_ns"`

	// Traffic stats
	BandwidthSent     uint64 `json:"bandwidth_sent"`
	BandwidthReceived uint64 `json:"bandwidth_received"`
	UploadSpeed       uint32 `json:"upload_speed"`
	DownloadSpeed     uint32 `json:"download_speed"`

	// Mux info
	MuxEnabled bool             `json:"mux_enabled"`
	Transports []RouteTransport `json:"transports"`
}

// RouteTransport describes one transport within a muxed route.
type RouteTransport struct {
	ID        uuid.UUID       `json:"id"`
	Type      string          `json:"type"`
	RemotePK  cipher.PubKey   `json:"remote_pk"`
	Latency   float64         `json:"latency_ms"`
	FwdRuleID routing.RouteID `json:"fwd_rule_id"`
}

// ActiveRouteStatuses returns the status of all active route groups.
func (r *router) ActiveRouteStatuses() []RouteStatus {
	r.mx.Lock()
	defer r.mx.Unlock()

	statuses := make([]RouteStatus, 0, len(r.rgsNs))

	for desc, nrg := range r.rgsNs {
		rg := nrg.rg

		status := RouteStatus{
			LocalPK:           desc.DstPK(),
			RemotePK:          desc.SrcPK(),
			LocalPort:         desc.DstPort(),
			RemotePort:        desc.SrcPort(),
			Alive:             nrg.IsAlive(),
			Latency:           nrg.Latency(),
			BandwidthSent:     nrg.BandwidthSent(),
			BandwidthReceived: nrg.BandwidthReceived(),
			UploadSpeed:       nrg.UploadSpeed(),
			DownloadSpeed:     nrg.DownloadSpeed(),
			MuxEnabled:        rg.mux != nil,
		}

		rg.mu.Lock()
		for i, tp := range rg.tps {
			if tp == nil {
				continue
			}
			rt := RouteTransport{
				ID:       tp.Entry.ID,
				Type:     string(tp.Entry.Type),
				RemotePK: tp.Entry.RemoteEdge(desc.DstPK()),
				Latency:  tp.GetLatency(),
			}
			if i < len(rg.fwd) {
				rt.FwdRuleID = rg.fwd[i].KeyRouteID()
			}
			status.Transports = append(status.Transports, rt)
		}
		rg.mu.Unlock()

		statuses = append(statuses, status)
	}

	return statuses
}
