package treestore

import (
	"errors"
	"testing"
)

// TestPublishStateFreezeSignature verifies that PublishState surfaces the
// exact freeze signature the visor-state diagnostic relies on: a standing
// publish error plus unpublished (dirty) changes reads back as Frozen,
// and a subsequent success clears it.
func TestPublishStateFreezeSignature(t *testing.T) {
	p := &Publisher{
		root: &memNode{
			leaves: map[string][]byte{"tp-list": []byte("blob")},
			subs: map[string]*memNode{
				"a": {leaves: map[string][]byte{"x": []byte("1"), "y": []byte("2")}},
			},
		},
	}

	// Healthy: no error recorded, not dirty → not frozen.
	if st := p.PublishState(); st.Frozen {
		t.Fatalf("clean publisher reported Frozen=true: %+v", st)
	}

	// Counts: 1 (tp-list) + 2 (a/x, a/y) leaves; 2 nodes (root + a).
	if st := p.PublishState(); st.LeafCount != 3 || st.NodeCount != 2 {
		t.Fatalf("countTree wrong: leaves=%d nodes=%d (want 3/2)", st.LeafCount, st.NodeCount)
	}

	// Simulate a standing publish failure with an unpublished change.
	p.dirty = true
	p.recordPublishErr(errors.New("treestore encode/save: not found"))

	st := p.PublishState()
	if !st.Frozen {
		t.Fatalf("dirty + standing error should be Frozen: %+v", st)
	}
	if st.LastErr == "" || st.LastErrType == "" {
		t.Fatalf("LastErr/LastErrType not captured: %+v", st)
	}
	if !st.LastErrMissingObj {
		t.Fatalf("a 'not found' error should read LastErrMissingObj=true: %+v", st)
	}

	// Recovery: a success clears the standing error → not frozen.
	p.clearPublishErr()
	if st := p.PublishState(); st.Frozen || st.LastErr != "" {
		t.Fatalf("after clearPublishErr should not be Frozen: %+v", st)
	}
}
