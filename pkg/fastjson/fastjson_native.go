//go:build !tinygo

// Package fastjson is jsoniter.ConfigFastest on native builds and
// encoding/json under TinyGo, where reflect2 has no implementation.
package fastjson

import jsoniter "github.com/json-iterator/go"

// JSON is the codec.
var JSON = jsoniter.ConfigFastest
