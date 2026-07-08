//go:build arm64 && !darwin

package cpufeat

func init() {
	ARM64.HasASIMD = true
}
