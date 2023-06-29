// Package lc internal/lc/data.go
package lc

import "github.com/skycoin/skywire/pkg/httputil"

// ServiceSummary summary of a visor connection
type ServiceSummary struct {
	Online          bool                          `json:"online"`
	Errors          []string                      `json:"errors,omitempty"`
	Timestamp       int64                         `json:"timestamp"`
	Health          *httputil.HealthCheckResponse `json:"health,omitempty"`
	CertificateInfo *CertificateInfo              `json:"certificate_info,omitempty"`
}

// CertificateInfo is the tls certificate info of the service
type CertificateInfo struct {
	Issuer string `json:"issuer,omitempty"`
	Expiry string `json:"expiry,omitempty"`
}
