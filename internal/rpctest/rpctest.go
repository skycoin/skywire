// Package rpctest checks gobrpc service registrations, in particular the
// reflection-free handlers rpcgen writes for TinyGo builds. Run the tests with
// -tags tinygo under standard Go to exercise the gobimpl HandleFunc path.
package rpctest

import (
	"net"
	"reflect"
	"strings"
	"testing"
	"time"

	rpc "github.com/0magnet/gobrpc"
)

// RegisterFunc registers a receiver under name, like the functions rpcgen writes.
type RegisterFunc func(s *rpc.Server, name string) error

// probe is sent as an argument that no real method argument can decode, so a
// registered handler fails in gob and never reaches the method.
type probe struct{ RPCTestProbe int }

var typeOfError = reflect.TypeOf((*error)(nil)).Elem()

// Methods returns the names of the methods net/rpc registers for rcvr.
func Methods(rcvr any) []string {
	var names []string
	t := reflect.TypeOf(rcvr)
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		mt := m.Type
		if !m.IsExported() || mt.NumIn() != 3 || mt.NumOut() != 1 || mt.Out(0) != typeOfError {
			continue
		}
		if mt.In(2).Kind() != reflect.Pointer || !exportedOrBuiltin(mt.In(1)) || !exportedOrBuiltin(mt.In(2)) {
			continue
		}
		names = append(names, m.Name)
	}
	return names
}

func exportedOrBuiltin(t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.PkgPath() == "" || (t.Name() != "" && strings.ToUpper(t.Name()[:1]) == t.Name()[:1])
}

// Serve registers a receiver under the name "Svc" on a new server and returns a
// client connected to it over net.Pipe.
func Serve(t testing.TB, register RegisterFunc) *rpc.Client {
	t.Helper()
	srv := rpc.NewServer()
	if err := register(srv, "Svc"); err != nil {
		t.Fatalf("register: %v", err)
	}
	a, b := net.Pipe()
	go srv.ServeConn(a)
	c := rpc.NewClient(b)
	t.Cleanup(func() { c.Close() }) //nolint:errcheck,gosec
	return c
}

// CheckHandlers fails t unless register serves every method net/rpc would
// register for rcvr. Each method is called with an undecodable argument, so a
// served method answers with a gob error and an unserved one with a lookup error.
func CheckHandlers(t testing.TB, rcvr any, register RegisterFunc) {
	t.Helper()
	names := Methods(rcvr)
	if len(names) == 0 {
		t.Fatalf("%T has no net/rpc methods", rcvr)
	}
	rt := reflect.TypeOf(rcvr)
	for _, name := range names {
		m, _ := rt.MethodByName(name)
		arg := m.Type.In(1)
		for arg.Kind() == reflect.Pointer {
			arg = arg.Elem()
		}
		var args any = probe{1}
		if arg.Kind() == reflect.Struct && arg.NumField() == 0 {
			args = 1
		}
		var reply int
		err := Serve(t, register).Call("Svc."+name, args, &reply)
		if err == nil || strings.Contains(err.Error(), "can't find") || !strings.Contains(err.Error(), "gob: ") {
			t.Errorf("%T.%s: want a gob decode error from a served method, got %v", rcvr, name, err)
		}
	}
	err := Serve(t, register).Call("Svc.RPCTestNoSuchMethod", 1, new(int))
	if err == nil || !strings.Contains(err.Error(), "can't find") {
		t.Errorf("unknown method: want a lookup error, got %v", err)
	}
	t.Logf("%T: %d methods checked", rcvr, len(names))
}

// CompareCall calls method through register and through reflection based
// RegisterName on rcvr, and fails t unless both give the same reply and error.
// It returns the reply from register.
func CompareCall(t testing.TB, rcvr any, register RegisterFunc, method string, args any, newReply func() any) any {
	t.Helper()
	call := func(reg RegisterFunc) (any, string) {
		reply := newReply()
		errStr := ""
		if err := Serve(t, reg).Call("Svc."+method, args, reply); err != nil {
			errStr = err.Error()
		}
		return reply, errStr
	}
	gotReply, gotErr := call(register)
	wantReply, wantErr := call(func(s *rpc.Server, name string) error { return s.RegisterName(name, rcvr) })
	if gotErr != wantErr {
		t.Errorf("%s: error %q, reflection path gave %q", method, gotErr, wantErr)
	}
	if !reflect.DeepEqual(gotReply, wantReply) {
		t.Errorf("%s: reply %+v, reflection path gave %+v", method, reflect.Indirect(reflect.ValueOf(gotReply)), reflect.Indirect(reflect.ValueOf(wantReply)))
	}
	return gotReply
}

// CheckNotBlocked starts blocked, a call that does not return until unblock
// does its work, then runs unblock on the same connection. It fails t if
// unblock does not finish within timeout, which is what serial dispatch causes.
func CheckNotBlocked(t testing.TB, c *rpc.Client, blocked *rpc.Call, unblock func() error, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- unblock() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("call behind a blocked call: %v", err)
		}
	case <-time.After(timeout):
		t.Fatalf("a blocked %s call held up the connection for %v", blocked.ServiceMethod, timeout)
	}
	select {
	case <-blocked.Done:
		if blocked.Error != nil {
			t.Fatalf("%s: %v", blocked.ServiceMethod, blocked.Error)
		}
	case <-time.After(timeout):
		t.Fatalf("%s did not return after it was unblocked", blocked.ServiceMethod)
	}
}
