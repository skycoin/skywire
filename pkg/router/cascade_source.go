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
	srcCB *CascadeBuilder,
	biRt routing.BidirectionalRoute,
) (routing.EdgeRules, error) {
	if srcCB == nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: no source-side builder")
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

	// --- Phase 1 (cont): inject reserve cascades over our own transports ---
	firstFwdTpID := biRt.Forward[0].TpID
	fwdAck, err := srcCB.SendCascade(ctx, firstFwdTpID, reserveReply.FwdSessionID, reserveReply.FwdReserveBytes, srcCB.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve send: %w", err)
	}
	if fwdAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd reserve rejected: %s", fwdAck.Error)
	}

	firstRevTpID := biRt.Reverse[0].TpID
	revAck, err := srcCB.SendCascade(ctx, firstRevTpID, reserveReply.RevSessionID, reserveReply.RevReserveBytes, srcCB.reserveTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve send: %w", err)
	}
	if revAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev reserve rejected: %s", revAck.Error)
	}

	log.WithField("fwd_ids", len(fwdAck.RouteIDs)).
		WithField("rev_ids", len(revAck.RouteIDs)).
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

	// --- Phase 2 (cont): inject install cascades over our own transports ---
	fwdInstAck, err := srcCB.SendCascade(ctx, firstFwdTpID, reserveReply.FwdSessionID, installReply.FwdInstallBytes, srcCB.installTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install send: %w", err)
	}
	if fwdInstAck.Error != "" {
		return routing.EdgeRules{}, fmt.Errorf("cascade: fwd install rejected: %s", fwdInstAck.Error)
	}

	revInstAck, err := srcCB.SendCascade(ctx, firstRevTpID, reserveReply.RevSessionID, installReply.RevInstallBytes, srcCB.installTimeout)
	if err != nil {
		return routing.EdgeRules{}, fmt.Errorf("cascade: rev install send: %w", err)
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
