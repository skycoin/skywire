//go:build darwin && !ios && (!cgo || tinygo)

package metrics

func getMemory() (*memoryInfo, error) {
	return nil, errNotImplemented
}
