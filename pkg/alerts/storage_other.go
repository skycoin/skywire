//go:build !linux

package alerts

// CheckStorage reports nothing outside Linux.
func CheckStorage() (readOnly bool, ioErrors int, last string) {
	return false, 0, ""
}
