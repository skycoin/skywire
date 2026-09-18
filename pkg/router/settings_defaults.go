// Package router pkg/router/settings_defaults.go c2-net-routing
//
// The compiled defaults that do not belong to any one mechanism's file, kept
// as named constants so the catalog has something to be asserted against
// (TestCatalogDefaultsMatchConstants) and so a reader of `route settings` can
// find the value the binary was built with.
package router

// forwardSpillDefault is OFF: the forward direction stays on its confined leg
// and a full window makes the writer WAIT rather than spill the frame onto a
// sibling, which is what kept a 50 MB upload from splitting across two skewed
// legs. forward.spill turns it back on.
const forwardSpillDefault = false
