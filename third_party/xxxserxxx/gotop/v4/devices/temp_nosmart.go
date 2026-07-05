//go:build darwin && !cgo
// +build darwin,!cgo

package devices

// Disk SMART temperatures on macOS require cgo (github.com/anatol/smart.go
// uses IOKit). Under a cgo-disabled build these become no-ops so the package
// still compiles; host sensor temperatures in temp_nix.go remain available.

func startBlock(map[string]string) error { return nil }

func endBlock() error { return nil }

func readDiskTemps(map[string]int) {}
