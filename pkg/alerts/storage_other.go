//go:build !linux

// Package alerts pkg/alerts/storage_other.go c3-vis-core
package alerts

// CheckStorage reports nothing outside Linux.
func CheckStorage() (readOnly bool, ioErrors int, last string) {
	return false, 0, ""
}
