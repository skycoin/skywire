//go:build js && wasm

// Package deskhost pkg/wasmhv/deskhost/shellenv_js.go c3-vis-wasm
package deskhost

import (
	"sort"
	"syscall/js"
)

// deskShellEnv is the environment the desk declares for its shells, as
// "NAME=value" — its shell profile. desk-boot.js publishes it in
// globalThis.__skywireShellEnv ({NAME: value}) when the tab runs its own visor:
// ALL_PROXY names the resolving proxy on the tab's loopback, the one a shell
// beside a native visor would be pointed at, and NO_PROXY keeps the tab's own
// loopback out of it. A desk bridged to a host visor publishes none.
func deskShellEnv() []string {
	obj := js.Global().Get("__skywireShellEnv")
	if obj.Type() != js.TypeObject {
		return nil
	}
	keys := js.Global().Get("Object").Call("keys", obj)
	var out []string
	for i := 0; i < keys.Length(); i++ {
		k := keys.Index(i).String()
		if v := obj.Get(k); v.Type() == js.TypeString {
			out = append(out, k+"="+v.String())
		}
	}
	sort.Strings(out)
	return out
}
