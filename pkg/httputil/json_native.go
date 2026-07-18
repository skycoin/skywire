//go:build !tinygo

// Package httputil pkg/httputil/json_native.go c0-com-http
//
// Native builds use jsoniter.ConfigFastest for the package-wide `json` codec.
// It relies on github.com/modern-go/reflect2, whose unsafe reflection
// (reflect.mapassign / unsafe_New / typedmemmove …) TinyGo's runtime does not
// provide, so the TinyGo build uses the standard library instead — see
// json_tinygo.go. Only NewEncoder/NewDecoder are used here, so the wire format
// is identical either way.
package httputil

import (
	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigFastest
