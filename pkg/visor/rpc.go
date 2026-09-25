// Package visor pkg/visor/rpc.go c3-vis-core
package visor

import (
	"errors"
	"fmt"
	"net/rpc"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/setupmetrics"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/rpcutil"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

const (

	// HealthTimeout defines timeout for /health endpoint calls done from hypervisor.
	HealthTimeout = skyenv.HealthTimeout
	// InnerHealthTimeout defines timeout for /health endpoint calls done from visor.
	InnerHealthTimeout = skyenv.InnerHealthTimeout
)

var (

	// ErrNotFound is returned when a requested resource is not found.
	ErrNotFound = errors.New("not found")
)

type RPC struct {
	visor visorapi.API
	log   logrus.FieldLogger
}

func newRPCServer(v *Visor, remoteName string) (*rpc.Server, error) {
	rpcS := rpc.NewServer()
	rpcG := &RPC{
		visor: v,
		log:   v.MasterLogger().PackageLogger("visor_rpc:" + remoteName),
	}

	if err := rpcS.RegisterName(visorapi.RPCPrefix, rpcG); err != nil {
		return nil, fmt.Errorf("failed to create visor RPC server: %w", err)
	}

	return rpcS, nil
}

func newTransportSummary(tm *transport.Manager, tp *transport.ManagedTransport, includeLogs, isSetup bool) *visorapi.TransportSummary {
	summary := &visorapi.TransportSummary{
		ID:            tp.Entry.ID,
		Local:         tm.Local(),
		Remote:        tp.Remote(),
		Type:          tp.Type(),
		IsSetup:       isSetup,
		Label:         tp.Entry.Label,
		LatencyMS:     tp.GetLatency(),
		ThroughputBps: tp.GetThroughputBps(),
		Initiator:     tp.IsInitiator(),
	}
	if includeLogs {
		summary.Log = tp.LogEntry
	}
	summary.Endpoint = tp.ConnDetails()
	summary.RemoteIP = tp.RemoteIP()
	if summary.RemoteIP != "" {
		summary.RemoteCountry = geoCountryForIP(summary.RemoteIP)
	}
	return summary
}

func (r *RPC) GetTransportSetupNodes(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodes", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodes()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetTransportSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetTransportSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetTransportSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetRouteSetupNodesSorted(_ *struct{}, out *[]cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "GetRouteSetupNodesSorted", nil)(out, &err)

	nodes, err := r.visor.GetRouteSetupNodesSorted()
	if err != nil {
		return err
	}
	*out = nodes
	return nil
}
func (r *RPC) GetTPSHealth(_ *struct{}, out *[]visorapi.NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetTPSHealth", nil)(out, &err)

	health, err := r.visor.GetTPSHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}
func (r *RPC) GetRSNHealth(_ *struct{}, out *[]visorapi.NodeHealth) (err error) {
	defer rpcutil.LogCall(r.log, "GetRSNHealth", nil)(out, &err)

	health, err := r.visor.GetRSNHealth()
	if err != nil {
		return err
	}
	*out = health
	return nil
}
func (r *RPC) RouteSetupStats(_ *struct{}, out *setupmetrics.StatsSnapshot) (err error) {
	defer rpcutil.LogCall(r.log, "RouteSetupStats", nil)(out, &err)
	snap, err := r.visor.RouteSetupStats()
	if err != nil {
		return err
	}
	*out = *snap
	return nil
}
func (r *RPC) ResetRouteSetupStats(_ *struct{}, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "ResetRouteSetupStats", nil)(nil, &err)
	return r.visor.ResetRouteSetupStats()
}

type DHTGetIn struct {
	PK   string `json:"pk"`
	Salt string `json:"salt"`
}
type DHTPutIn struct {
	Value []byte `json:"value"`
	Seq   uint64 `json:"seq"`
	Salt  string `json:"salt"`
}

func (r *RPC) AddHypervisor(in *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "AddHypervisor", in)(nil, &err)
	return r.visor.AddHypervisor(*in)
}

// PendingHypervisors lists peers waiting to be approved as hypervisors.
func (r *RPC) PendingHypervisors(_ *struct{}, out *[]visorapi.PendingHypervisor) (err error) {
	defer rpcutil.LogCall(r.log, "PendingHypervisors", nil)(out, &err)
	*out, err = r.visor.PendingHypervisors()
	return err
}

// ApproveHypervisor approves a pending key by public key or fingerprint.
func (r *RPC) ApproveHypervisor(sel *string, out *cipher.PubKey) (err error) {
	defer rpcutil.LogCall(r.log, "ApproveHypervisor", sel)(out, &err)
	*out, err = r.visor.ApproveHypervisor(*sel)
	return err
}

// NewPairCode mints a one-time pairing code.
func (r *RPC) NewPairCode(ttl *time.Duration, out *visorapi.PairCode) (err error) {
	defer rpcutil.LogCall(r.log, "NewPairCode", ttl)(out, &err)
	*out, err = r.visor.NewPairCode(*ttl)
	return err
}

// RemoveHypervisor tears down a runtime-added hypervisor connection
// by PK. Idempotent: succeeds with nil if the PK is not currently a
// runtime-added hypervisor on this visor (covers double-rm, rm of a
// config-loaded hypervisor, and rm of an already-disconnected PK).
func (r *RPC) RemoveHypervisor(in *cipher.PubKey, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveHypervisor", in)(nil, &err)
	return r.visor.RemoveHypervisor(*in)
}

// RemoveAllHypervisors tears down every runtime-added hypervisor
// connection on this visor. Returns the count disconnected.
func (r *RPC) RemoveAllHypervisors(_ *struct{}, out *int) (err error) {
	defer rpcutil.LogCall(r.log, "RemoveAllHypervisors", nil)(out, &err)
	n, err := r.visor.RemoveAllHypervisors()
	*out = n
	return err
}

// SetHypervisorPassword changes the hypervisor UI admin password.
// Local-only RPC; the HTTP session check that /api/change-password
// enforces is intentionally absent here.
func (r *RPC) SetHypervisorPassword(in *visorapi.HypervisorPasswordChangeIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetHypervisorPassword", nil)(nil, &err)
	return r.visor.SetHypervisorPassword(in.OldPassword, in.NewPassword)
}

// SetHypervisorPasswordForce sets the hypervisor UI admin password
// without the old one (reset / first-time set). Local-only RPC; same
// privileged-local rationale as SetHypervisorPassword. OldPassword in the
// request is ignored.
func (r *RPC) SetHypervisorPasswordForce(in *visorapi.HypervisorPasswordChangeIn, _ *struct{}) (err error) {
	defer rpcutil.LogCall(r.log, "SetHypervisorPasswordForce", nil)(nil, &err)
	return r.visor.SetHypervisorPasswordForce(in.NewPassword)
}

type DHTSyncRequest struct {
	RemotePK string `json:"remote_pk"`
	Salt     string `json:"salt"`
}

type HVMuxArgs struct {
	PK cipher.PubKey
	N  int
}
