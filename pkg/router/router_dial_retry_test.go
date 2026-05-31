// router_dial_retry_test.go — unit tests for the dial-failure retry
// helpers added to compose with #2746 (ExcludeIntermediatePKs) and
// #2750 (per-intermediate breaker). When the setup-node returns a
// DialError on a multi-hop attempt, DialRoutes / PingRoute extract
// the failed intermediate's PK and add it to opts.ExcludeIntermediatePKs
// so the next fetchBestRoutes iteration picks a different candidate
// from the route-finder's slice. The helpers below are pure, so
// covering them in isolation lets the integration retry loop stay
// simple — caller doesn't have to re-derive the "is it src/dst"
// check or worry about idempotency.

package router

import (
	"errors"
	"fmt"
	"testing"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestExtractFailedIntermediatePK_NoError(t *testing.T) {
	pk, ok := extractFailedIntermediatePK(nil, cipher.PubKey{}, cipher.PubKey{})
	if ok {
		t.Errorf("nil err: ok=true, want false")
	}
	if !pk.Null() {
		t.Errorf("nil err: pk=%v, want zero", pk)
	}
}

func TestExtractFailedIntermediatePK_NonDialError(t *testing.T) {
	// A plain error wrapped without DialError. The walker can't
	// identify an intermediate to blame, so we get (zero, false).
	err := fmt.Errorf("some other failure: %w", errors.New("inner"))
	pk, ok := extractFailedIntermediatePK(err, mustPK(t), mustPK(t))
	if ok {
		t.Errorf("non-DialError: ok=true, want false")
	}
	if !pk.Null() {
		t.Errorf("non-DialError: pk=%v, want zero", pk)
	}
}

func TestExtractFailedIntermediatePK_DialErrorAtSrc(t *testing.T) {
	// If the dial failure was the source visor itself (e.g. a local
	// dmsg-session hiccup), we MUST NOT exclude src from the next
	// candidate — there's nothing to substitute. Same for dst (the
	// hypothesis test would be vacuous; the operator demanded the
	// route TO this specific dst).
	src := mustPK(t)
	dst := mustPK(t)
	for _, who := range []struct {
		name string
		pk   cipher.PubKey
	}{
		{"src", src},
		{"dst", dst},
	} {
		t.Run(who.name, func(t *testing.T) {
			err := &DialError{PK: who.pk, Err: errors.New("dmsg error 202")}
			gotPK, ok := extractFailedIntermediatePK(err, src, dst)
			if ok {
				t.Errorf("DialError on %s: ok=true, want false (endpoint)", who.name)
			}
			if !gotPK.Null() {
				t.Errorf("DialError on %s: pk=%v, want zero", who.name, gotPK)
			}
		})
	}
}

func TestExtractFailedIntermediatePK_DialErrorAtIntermediate(t *testing.T) {
	src := mustPK(t)
	dst := mustPK(t)
	intermediate := mustPK(t)

	err := &DialError{PK: intermediate, Err: errors.New("connection is shut down")}
	gotPK, ok := extractFailedIntermediatePK(err, src, dst)
	if !ok {
		t.Fatalf("intermediate DialError: ok=false, want true")
	}
	if gotPK != intermediate {
		t.Errorf("intermediate DialError: pk=%v, want %v", gotPK, intermediate)
	}
}

func TestExtractFailedIntermediatePK_WrappedDialError(t *testing.T) {
	// Real error chain from the setup-node: DialError wrapped a few
	// layers deep inside fmt.Errorf("...: %w", err). Walker must find
	// the DialError via errors.As regardless of depth.
	src := mustPK(t)
	dst := mustPK(t)
	intermediate := mustPK(t)

	dialErr := &DialError{PK: intermediate, Err: errors.New("connection is shut down")}
	wrapped := fmt.Errorf("route setup: failed to reserve route ids: %w", dialErr)
	wrappedTwice := fmt.Errorf("Dial: %w", wrapped)

	gotPK, ok := extractFailedIntermediatePK(wrappedTwice, src, dst)
	if !ok {
		t.Fatalf("wrapped DialError: ok=false, want true")
	}
	if gotPK != intermediate {
		t.Errorf("wrapped DialError: pk=%v, want %v", gotPK, intermediate)
	}
}

func TestAppendExcludeIntermediate_NilOpts(t *testing.T) {
	pk := mustPK(t)
	opts := appendExcludeIntermediate(nil, pk)
	if opts == nil {
		t.Fatal("nil opts: returned nil; want non-nil")
	}
	if len(opts.ExcludeIntermediatePKs) != 1 || opts.ExcludeIntermediatePKs[0] != pk {
		t.Errorf("nil opts: ExcludeIntermediatePKs=%v, want [%v]", opts.ExcludeIntermediatePKs, pk)
	}
}

func TestAppendExcludeIntermediate_AppendsNew(t *testing.T) {
	first := mustPK(t)
	second := mustPK(t)
	opts := &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{first}}
	opts = appendExcludeIntermediate(opts, second)
	if len(opts.ExcludeIntermediatePKs) != 2 {
		t.Fatalf("len=%d, want 2", len(opts.ExcludeIntermediatePKs))
	}
	if opts.ExcludeIntermediatePKs[1] != second {
		t.Errorf("second pk=%v, want %v", opts.ExcludeIntermediatePKs[1], second)
	}
}

func TestAppendExcludeIntermediate_Idempotent(t *testing.T) {
	// Same intermediate failing twice within a retry cycle (transient
	// flake during the burst): the dedup ensures the exclude list
	// doesn't grow unbounded across retries.
	pk := mustPK(t)
	opts := &DialOptions{ExcludeIntermediatePKs: []cipher.PubKey{pk}}
	opts = appendExcludeIntermediate(opts, pk)
	if len(opts.ExcludeIntermediatePKs) != 1 {
		t.Errorf("after re-add: len=%d, want 1 (dedup)", len(opts.ExcludeIntermediatePKs))
	}
}
