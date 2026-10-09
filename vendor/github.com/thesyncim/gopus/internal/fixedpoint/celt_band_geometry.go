//go:build gopus_fixed_point

package fixedpoint

// celtBandGeometry holds the static band tables and limits for one CELT mode.
type celtBandGeometry struct {
	eBands      []int16
	logN        []int16
	nbEBands    int
	effEBands   int
	qextMode    bool
	customCache fixedCustomTables
}
