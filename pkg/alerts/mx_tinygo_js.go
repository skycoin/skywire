//go:build tinygo && js

package alerts

import (
	"context"
	"errors"
)

// TinyGo's js/wasm net package has no MX lookup.
func lookupMX(context.Context, string) ([]*MX, error) {
	return nil, errors.New("mx lookup is not available in this build")
}
