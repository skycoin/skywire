package visor

import (
	"errors"
	"testing"
)

// stubAPI is a complete API that answers ErrProxyNotSupported from every
// method — enough to tell "the call was forwarded" from "the wrapper answered
// it itself", which is the only behavior these tests are about.
//
// The id field is load-bearing: proxyDefaultAPI is an empty struct, and two
// pointers to distinct ZERO-SIZE allocations may compare EQUAL in Go. Without
// a field, "a different visor unregistering" and "the same visor
// unregistering" would be the same test.
type stubAPI struct {
	proxyDefaultAPI
	id int
}

// The suspend trio is on API but not on the generated stub this embeds.
func (*stubAPI) Suspend() error             { return ErrProxyNotSupported }
func (*stubAPI) Resume() error              { return ErrProxyNotSupported }
func (*stubAPI) IsSuspended() (bool, error) { return false, ErrProxyNotSupported }

func TestLocalAPIUnsetIsNil(t *testing.T) {
	t.Cleanup(func() { RegisterLocalAPI(nil) })
	RegisterLocalAPI(nil)

	if got := LocalAPI(); got != nil {
		t.Fatalf("LocalAPI() = %v, want nil when no visor is registered", got)
	}
}

// The registered visor must be reachable, and reachable in a form whose Close
// cannot shut it down: skychat closes what it holds whenever it redials.
func TestLocalAPICloseDoesNotReachTheVisor(t *testing.T) {
	t.Cleanup(func() { RegisterLocalAPI(nil) })
	RegisterLocalAPI(&stubAPI{id: 1})

	api := LocalAPI()
	if api == nil {
		t.Fatal("LocalAPI() = nil after registering")
	}
	if err := api.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil — Close must not reach the visor", err)
	}
	// Everything else still goes through to it.
	if _, err := api.Health(); !errors.Is(err, ErrProxyNotSupported) {
		t.Fatalf("Health() = %v, want it forwarded to the registered visor", err)
	}
}

// Unregister is compare-and-clear: a visor finishing its shutdown after a
// replacement has registered must not take the live one out.
func TestUnregisterLocalAPIOnlyClearsItsOwn(t *testing.T) {
	t.Cleanup(func() { RegisterLocalAPI(nil) })

	first, second := &stubAPI{id: 1}, &stubAPI{id: 2}
	RegisterLocalAPI(first)
	RegisterLocalAPI(second)

	UnregisterLocalAPI(first)
	if LocalAPI() == nil {
		t.Fatal("the replacement was cleared by the outgoing visor's unregister")
	}

	UnregisterLocalAPI(second)
	if got := LocalAPI(); got != nil {
		t.Fatalf("LocalAPI() = %v, want nil after the current visor unregistered", got)
	}
}
