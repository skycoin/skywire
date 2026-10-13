package visor

import (
	"runtime"
	"testing"
)

func TestDiagSnapshotReportsLiveHeap(t *testing.T) {
	runtime.GC()
	d := (&Visor{}).DiagSnapshot()
	if d.Runtime.HeapLiveMB <= 0 {
		t.Fatalf("heap_live_mb = %v, want > 0 after a GC", d.Runtime.HeapLiveMB)
	}
	if d.Runtime.HeapLiveMB > d.Runtime.SysMB {
		t.Fatalf("heap_live_mb %v exceeds sys_mb %v", d.Runtime.HeapLiveMB, d.Runtime.SysMB)
	}
}
