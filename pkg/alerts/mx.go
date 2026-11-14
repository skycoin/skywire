//go:build !(tinygo && js)

// Package alerts pkg/alerts/mx.go c3-vis-core
package alerts

import (
	"context"
	"net"
)

func lookupMX(ctx context.Context, domain string) ([]*MX, error) {
	mxs, err := net.DefaultResolver.LookupMX(ctx, domain)
	out := make([]*MX, 0, len(mxs))
	for _, mx := range mxs {
		out = append(out, &MX{Host: mx.Host, Pref: mx.Pref})
	}
	return out, err
}
