package setupmetrics

import (
	"github.com/skycoin/skywire/pkg/routing"
)

// NewEmpty creates a new metrics implementation that does nothing.
func NewEmpty() Empty {
	return Empty{}
}

// Empty is a `Metrics` implementation which does nothing.
type Empty struct{}

// RecordRequest implements `Metrics`.
func (Empty) RecordRequest() func(*routing.EdgeRules, *error) {
	return func(*routing.EdgeRules, *error) {}
}

// RecordRoute implements `Metrics`.
func (Empty) RecordRoute() func(*error) {
	return func(*error) {}
}
