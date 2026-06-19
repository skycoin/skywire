// Package visor pkg/visor/rpc_client_mock_test.go: exercises the mock
// RPC client (the in-process API test double) across its full method
// set. The mock returns canned/random data with no network, so invoking
// every API method via reflection with zero-value arguments covers the
// bulk of rpc_client_mock.go.
//
// We iterate the API *interface* method set (not the concrete type) so
// the sweep skips the sync.RWMutex methods the mock promotes via
// embedding — calling those (e.g. Lock) would acquire the mock's lock
// and deadlock every do()-based method. A per-call recover handles the
// few methods that panic on zero-value input (e.g. RouteGroups builds a
// rule eagerly).
package visor

import (
	"math/rand"
	"reflect"
	"testing"
	"time"
)

func TestMockRPCClient_AllMethods(t *testing.T) {
	_, api, err := NewMockRPCClient(rand.New(rand.NewSource(1)), 5, 5) //nolint:gosec
	if err != nil {
		t.Fatal(err)
	}
	if api == nil {
		t.Fatal("nil mock")
	}

	apiType := reflect.TypeOf((*API)(nil)).Elem()
	v := reflect.ValueOf(api)

	for i := 0; i < apiType.NumMethod(); i++ {
		name := apiType.Method(i).Name
		fn := v.MethodByName(name)
		ft := fn.Type()

		args := make([]reflect.Value, ft.NumIn())
		for j := 0; j < ft.NumIn(); j++ {
			args[j] = reflect.Zero(ft.In(j))
		}

		done := make(chan struct{})
		go func() {
			defer func() { _ = recover() }()
			defer close(done)
			if ft.IsVariadic() {
				fn.CallSlice(args)
			} else {
				fn.Call(args)
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Errorf("method %s did not return within 5s on zero-value input", name)
		}
	}
}
