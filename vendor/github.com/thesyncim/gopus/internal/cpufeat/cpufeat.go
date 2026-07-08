// Package cpufeat detects optional CPU instruction-set extensions at process
// start and exposes them through the AMD64 and ARM64 variables so the codec can
// gate its SIMD code paths at runtime. Detection runs in architecture-specific
// init functions; on the purego build, or on an architecture other than the one
// queried, the corresponding variable stays at its zero value (no extensions).
package cpufeat

// AMD64Features reports which optional amd64 instruction-set extensions the
// running CPU and OS support, used to gate SIMD code paths at runtime.
type AMD64Features struct {
	HasAVX2 bool
	HasFMA  bool
}

// ARM64Features reports which optional arm64 (AArch64) instruction-set
// extensions the running CPU supports, used to gate SIMD code paths at runtime.
type ARM64Features struct {
	HasASIMD     bool
	HasDotProd   bool
	HasFCMA      bool
	HasFHM       bool
	HasBF16      bool
	HasI8MM      bool
	HasSME       bool
	HasSMEF64F64 bool
}

// AMD64 reports the detected amd64 CPU feature support. It is populated once at
// package init and is the zero value (no extensions) on non-amd64 or purego
// builds.
var AMD64 AMD64Features

// ARM64 reports the detected arm64 CPU feature support. It is populated once at
// package init and is the zero value (no extensions) on non-arm64 or purego
// builds.
var ARM64 ARM64Features
