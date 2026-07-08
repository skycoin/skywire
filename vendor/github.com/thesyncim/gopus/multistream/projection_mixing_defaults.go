package multistream

type projectionMixingDefault struct {
	streams int
	coupled int
	matrix  []int16
}

func defaultProjectionMixingMatrix(channels, streams, coupled int) ([]int16, bool) {
	def, ok := projectionMixingDefaults[channels]
	if !ok {
		return nil, false
	}
	if def.streams != streams || def.coupled != coupled {
		return nil, false
	}
	matrix := make([]int16, len(def.matrix))
	copy(matrix, def.matrix)
	return matrix, true
}
