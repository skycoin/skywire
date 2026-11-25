package transport

import "testing"

// TestVStreamCloseNilReceiver checks a nil *VStream, as an interface holding a
// failed dial's result carries, closes without a panic.
func TestVStreamCloseNilReceiver(t *testing.T) {
	var s *VStream
	if err := s.Close(); err != nil {
		t.Fatalf("Close on nil stream: %v", err)
	}
	s.closeSilently()
}
