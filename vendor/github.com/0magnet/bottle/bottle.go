// Package bottle is the OS layer for running Go programs in a browser tab
// the way they run on Linux — the bottle a wasm ship sails in.
//
// Two page-global primitives, one JS file each:
//
//	jsfs.js — an in-memory Linux-layout filesystem installed as
//	          globalThis.fs / globalThis.process. Go's js/wasm runtime routes
//	          the entire os package through this contract, so every wasm
//	          instance on the page shares one POSIX-ish root: one program
//	          writes /etc files, another reads them back.
//	vnet.js — a virtual loopback network: a page-global port table with
//	          in-memory byte pipes, so an instance that LISTENS on
//	          127.0.0.1:<port> can be DIALED from another instance — or from
//	          page JS via vnet.httpFetch.
//
// The vnet subpackage is the Go adapter: vnet.Listen / vnet.DialTimeout are
// net.Listen / net.DialTimeout on native builds, and route loopback addresses
// through the page table under js/wasm.
//
// Load order matters: both scripts must run BEFORE a Go wasm instance starts
// (Go captures globalThis.fs at instance start). Application-specific
// filesystem layout is the page's job, via jsfs.mkdirp / jsfs.writeFile, in a
// script loaded after jsfs.js.
package bottle

import (
	_ "embed"
)

//go:embed jsfs.js
var jsfs []byte

//go:embed vnet.js
var vnetJS []byte

// JSFS returns jsfs.js — the globalThis.fs / globalThis.process filesystem.
func JSFS() []byte { return jsfs }

// VNetJS returns vnet.js — the globalThis.vnet virtual loopback network.
func VNetJS() []byte { return vnetJS }
