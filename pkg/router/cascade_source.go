//go:build !tinygo || (js && wasm)

// Package router pkg/router/cascade_source.go c2-net-routing
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
	"strings"
	"time"

	rpc "github.com/0magnet/gobrpc"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/routing"
)

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
	start := time.Now()

	// --- Phase 1: ask RSN to sign reserve cascades ---
	var reserveReply CascadeSignReserveReply
	if err := rpcCall(ctx, rpcC, rpcName+".CascadeSignReserve",
		&CascadeSignReserveArgs{Route: biRt}, &reserveReply); err != nil {
		if isUnimplementedRPC(err) {
			return routing.EdgeRules{}, errCascadeSignUnimplemented
		}
		return routing.EdgeRules{}, fmt.Errorf("cascade: sign reserve RPC: %w", err)
	}
	signedReserve := time.Now()

	// --- Phase 1 (cont): consume our own (outermost) layer locally, which
	// reserves our route IDs and relays the inner payload down our own
	// transport to the first hop, collecting the cascade ACK. The forward and
	// reverse cascades are independent, so they travel at the same time.
	fwdAck, revAck, err := originBoth(originProc, reserveReply.FwdReserveBytes, reserveReply.RevReserveBytes, "reserve")
	if err != nil {
		return routing.EdgeRules{}, err
	}
	reserved := time.Now()

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
	signedInstall := time.Now()

	// --- Phase 2 (cont): consume our own install layers locally and relay. ---
	if _, _, err := originBoth(originProc, installReply.FwdInstallBytes, installReply.RevInstallBytes, "install"); err != nil {
		return routing.EdgeRules{}, err
	}

	ms := func(a, b time.Time) int64 { return b.Sub(a).Milliseconds() }
	log.WithField("sign_reserve_ms", ms(start, signedReserve)).
		WithField("reserve_ms", ms(signedReserve, reserved)).
		WithField("sign_install_ms", ms(reserved, signedInstall)).
		WithField("install_ms", ms(signedInstall, time.Now())).
		Info("Source-driven cascade route setup succeeded")
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

// originBoth processes the forward and reverse cascades of one phase at the
// same time and returns both ACKs, or the first failure.
func originBoth(p cascadeOriginProcessor, fwd, rev []byte, phase string) (fwdAck, revAck *routing.CascadeAck, err error) {
	var revErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		revAck, revErr = p.ProcessLocalOrigin(rev)
	}()
	fwdAck, err = p.ProcessLocalOrigin(fwd)
	<-done
	switch {
	case err != nil:
		return nil, nil, fmt.Errorf("cascade: fwd %s: %w", phase, err)
	case fwdAck.Error != "":
		return nil, nil, fmt.Errorf("cascade: fwd %s rejected: %s", phase, fwdAck.Error)
	case revErr != nil:
		return nil, nil, fmt.Errorf("cascade: rev %s: %w", phase, revErr)
	case revAck.Error != "":
		return nil, nil, fmt.Errorf("cascade: rev %s rejected: %s", phase, revAck.Error)
	}
	return fwdAck, revAck, nil
}
