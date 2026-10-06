//go:build js && wasm

package dmsg

import (
	"syscall/js"
	"testing"
)

func TestJSReasonAnyValue(t *testing.T) {
	errObj := js.Global().Get("Error").New("boom")
	for _, tc := range []struct {
		v    js.Value
		want string
	}{
		{js.Undefined(), "undefined"},
		{js.Null(), "null"},
		{js.ValueOf("plain"), "plain"},
		{errObj, "boom"},
	} {
		if got := jsReason(tc.v); got != tc.want {
			t.Errorf("jsReason = %q, want %q", got, tc.want)
		}
	}
}

func TestAwaitJSRejectedWithUndefined(t *testing.T) {
	p := js.Global().Get("Promise").Call("reject", js.Undefined())
	if _, err := awaitJS(p); err == nil || err.Error() != "undefined" {
		t.Fatalf("awaitJS error = %v, want undefined", err)
	}
}
