// Package addrresolver — pkg/transport/network/addrresolver/ipv6_bind_test.go
//
// Coverage for the Phase 2b additions: the AR client's optional
// v6-forced auth-client field, and its no-op fallback when callers
// don't supply a v6 http.Client. The actual two-POST bind path is
// integration-tested live against the AR (#2715 captures the family
// from the connecting socket); this file pins the in-process
// contract for backward compat + nil-safety.
package addrresolver

import (
	"context"
	"testing"

	"github.com/skycoin/skywire/pkg/logging"
)

// TestPostV6BindSTCPR_NilClientIsNoOp verifies the secondary v6 POST
// is silently skipped when httpClientV6 is nil — the path a
// pre-#1525 caller (or a caller against a dmsg://-routed AR) takes.
// The test exercises postV6BindSTCPR directly with a zeroed-out
// httpClient and asserts no panic + no error surface.
func TestPostV6BindSTCPR_NilClientIsNoOp(t *testing.T) {
	c := &httpClient{
		log: logging.MustGetLogger("test-ipv6-noop"),
	}
	logEntry := c.log.WithField("test", "nil-v6")
	c.postV6BindSTCPR(context.Background(), LocalAddresses{}, logEntry)
	// no panic, no observable side effect — the function returns
	// early on c.httpClientV6 == nil and the caller's BindSTCPR
	// keeps the v4 bind's success/failure as the primary result.
}
