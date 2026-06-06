// Package router pkg/router/cascade_source.go
//
// Source-driven cascade orchestration.
//
// In the source-driven design the RSN is a pure dmsg-reachable SIGNING
// ORACLE: it signs the per-hop reserve/install cascades but never dials hops
// and is never on the data path. The SOURCE (the requesting visor) drives
// the cascade down its OWN transports:
//
//  1. Source -> RSN (RPC): CascadeSignReserve -> signed reserve cascade bytes
//     + session IDs for forward and reverse.
//  2. Source injects the reserve cascades into Forward[0].TpID / Reverse[0].TpID
//     via its own CascadeBuilder.SendCascade and collects the reserved
//     route-ID ACKs.
//  3. Source -> RSN (RPC): CascadeSignInstall (route + session IDs + collected
//     route IDs) -> signed install cascade bytes + the initiating EdgeRules.
//     The RSN recomputes the rules deterministically; it does not trust
//     source-supplied rules.
//  4. Source injects the install cascades via SendCascade. Done.
package router

import (
	"context"
	"fmt"
	"net/rpc"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/routing"
)

// cascadeSourceProvider is implemented by route-group dialers that own a
// source-side CascadeBuilder. The router uses it at Serve time to share the
// builder's ack registry with the visor's single CascadeHandler.
type cascadeSourceProvider interface {
	CascadeAckRegistry() *ackRegistry
}

// cascadeOriginSetter is implemented by route-group dialers that drive
// source-driven cascade. The router calls SetCascadeOrigin at Serve time to
// hand the dialer the visor's CascadeHandler, which the dialer uses to consume
// the source-addressed outermost cascade layer locally.
type cascadeOriginSetter interface {
	SetCascadeOrigin(p cascadeOriginProcessor)
}

// errCascadeSignUnimplemented signals that the RSN does not implement the
// CascadeSign* RPCs (an un-upgraded setup node). Callers fall back to the
// legacy DMSG @136 path.
var errCascadeSignUnimplemented = fmt.Errorf("cascade: RSN does not implement source-driven cascade")

// isUnimplementedRPC reports whether err is net/rpc's "can't find method"
// server error, i.e. the RSN is an older build without the CascadeSign* RPCs.
func isUnimplementedRPC(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "can't find method") ||
		strings.Contains(err.Error(), "can't find service")
}

// runSourceCascade performs the full source-driven cascade against the RSN
// reachable via rpcC, using srcCB (the source's send-only CascadeBuilder) to
// inject the signed cascades down this visor's own transports.
//
// On success it returns the initiating EdgeRules. If the RSN does not
// implement the CascadeSign* RPCs it returns errCascadeSignUnimplemented so
// the caller can fall back to the legacy DMSG path.
func runSourceCascade(
	ctx context.Context,
	log logrus.FieldLogger,
	rpcC *rpc.Client,
	originProc cascadeOriginProcessor,
	biRt routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	if originProc == nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: no source-origin processor")
	}
	if len(biRt.Forward) == 0 || len(biRt.Reverse) == 0 {
		return routing.EdgeRules{}, fmt.Errorf("cascade: route has no hops")
	}

	log.Debug("Source-driven cascade: requesting RSN reserve signatures")

	// --- Phase 1: ask RSN to sign reserve cascades ---
	var reserveReply CascadeSignReserveReply
	if err := rpcCall(ctx, rpcC, rpcName+".CascadeSignReserve",
		&CascadeSignReserveArgs{Route: biRt}, &reserveReply); err != nil {
		if isUnimplementedRPC(err) {
			return routing.EdgeRules{}, errCascadeSignUnimplemented
		}
		return routing.EdgeRules{}, fmt.Errorf("cascade: sign reserve RPC: %w", err)
	}

	// --- Phase 1 (cont): consume our own (outermost) layer locally, which
	// reserves our route IDs and relays the inner payload down our own
	// transport to the first hop, collecting the cascade ACK. ---
	fwdAck, err := originProc.ProcessLocalOrigin(reserveReply.FwdReserveBytes)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve: %w", err)
	}
	if fwdAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve rejected: %s", fwdAck.Error)
	}

	revAck, err := originProc.ProcessLocalOrigin(reserveReply.RevReserveBytes)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve: %w", err)
	}
	if revAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve rejected: %s", revAck.Error)
	}

	log.WithField("fwd_ids", fmt.Sprintf("%v", fwdAck.RouteIDs)).
		WithField("rev_ids", fmt.Sprintf("%v", revAck.RouteIDs)).
		Debug("Source-driven cascade: reserved route IDs, requesting install signatures")

	// --- Phase 2: ask RSN to sign install cascades (recomputes rules) ---
	var installReply CascadeSignInstallReply
	if err := rpcCall(ctx, rpcC, rpcName+".CascadeSignInstall", &CascadeSignInstallArgs{
		Route:        biRt,
		FwdSessionID: reserveReply.FwdSessionID,
		RevSessionID: reserveReply.RevSessionID,
		FwdRouteIDs:  fwdAck.RouteIDs,
		RevRouteIDs:  revAck.RouteIDs,
	}, &installReply); err != nil {
		if isUnimplementedRPC(err) {
			return routing.EdgeRules{}, errCascadeSignUnimplemented
		}
		return routing.EdgeRules{}, fmt.Errorf("cascade: sign install RPC: %w", err)
	}

	// --- Phase 2 (cont): consume our own install layer locally and relay. ---
	fwdInstAck, err := originProc.ProcessLocalOrigin(installReply.FwdInstallBytes)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install: %w", err)
	}
	if fwdInstAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install rejected: %s", fwdInstAck.Error)
	}

	revInstAck, err := originProc.ProcessLocalOrigin(installReply.RevInstallBytes)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev install: %w", err)
	}
	if revInstAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev install rejected: %s", revInstAck.Error)
	}

	log.Info("Source-driven cascade route setup succeeded")
	return installReply.InitEdge, nil
}

// rpcCall performs a context-aware net/rpc call.
func rpcCall(ctx context.Context, rpcC *rpc.Client, serviceMethod string, args, reply interface{}) error {
	call := rpcC.Go(serviceMethod, args, reply, nil)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-call.Done:
		return call.Error
	}
}
