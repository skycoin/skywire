// atomic_alignment_test.go: the counters touched by 64-bit atomics must be
// sync/atomic types, not bare int64/uint64.
//
// On a 32-bit GOARCH (arm, 386, mips) an int64 field inside a struct is only
// FOUR-byte aligned, while a 64-bit atomic operation needs eight. The mismatch
// is not a compile error and not a vet finding — it is a runtime panic,
// "unaligned 64-bit atomic operation", that takes the process down. Reported
// from the fleet 2026-09-19: armhf visors crash-looping in VStream.Write, the
// counter having landed at offset 36 behind id + appName + openedAt (time.Time
// is 20 bytes on a 32-bit build, so it leaves the next field 4-aligned).
//
// atomic.Int64 / atomic.Uint64 embed the runtime's align64 marker, so the
// compiler places them correctly on every GOARCH and the bug cannot recur by
// field reordering. This test pins that choice: a counter quietly changed back
// to a bare int64 fails here rather than on someone's board.
//
// The whole-suite version of this check is `make test-32bit`, which runs the
// packages under GOARCH=386 — that is what found the three siblings of this bug
// in pkg/router, pkg/dmsg/dmsg and pkg/logging.
package transport

import (
	"reflect"
	"testing"
)

func requireAtomicField(t *testing.T, v interface{}, field string) {
	t.Helper()
	f, ok := reflect.TypeOf(v).FieldByName(field)
	if !ok {
		t.Fatalf("%T has no field %q", v, field)
	}
	if got := f.Type.PkgPath(); got != "sync/atomic" {
		t.Errorf("%T.%s is %s, want a sync/atomic type: a bare 64-bit counter "+
			"panics on 32-bit GOARCHes when its offset is not 8-aligned",
			v, field, f.Type)
	}
}

func TestVStreamCountersAreTypedAtomics(t *testing.T) {
	for _, f := range []string{"sentBytes", "recvBytes"} {
		requireAtomicField(t, VStream{}, f)
	}
	for _, f := range []string{"streamID", "framesUnknownStream", "stalledStreams", "acceptDropped", "relayCount"} {
		requireAtomicField(t, VStreamMux{}, f)
	}
}
