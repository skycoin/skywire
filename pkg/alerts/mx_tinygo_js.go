//go:build tinygo && js

// Package alerts pkg/alerts/mx_tinygo_js.go c3-vis-core
package alerts

import (
	"context"
	"errors"
)

// TinyGo's js/wasm net package has no MX lookup.
func lookupMX(context.Context, string) ([]*MX, error) {
	return nil, errors.New("mx lookup is not available in this build")
}
