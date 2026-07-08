package silk

// silkLog2Lin converts from log scale to linear scale.
// Matches libopus silk/Log2Lin.c.
func silkLog2Lin(inLogQ7 int32) int32 {
	if inLogQ7 < 0 {
		return 0
	}
	if inLogQ7 >= 3967 {
		return 0x7fffffff
	}

	out := int32(1) << (inLogQ7 >> 7)
	fracQ7 := inLogQ7 & 0x7f

	// Piece-wise parabolic approximation
	if inLogQ7 < 2048 {
		out = silkADD_RSHIFT32(out, silkMUL(out, silkSMLAWB(fracQ7, silkSMULBB(fracQ7, 128-fracQ7), -174)), 7)
	} else {
		out = silkMLA(out, out>>7, silkSMLAWB(fracQ7, silkSMULBB(fracQ7, 128-fracQ7), -174))
	}

	return out
}

// silkLin2Log converts from linear scale to log scale.
// Matches libopus silk/Lin2Log.c.
func silkLin2Log(inLin int32) int32 {
	if inLin <= 0 {
		return 0
	}

	lz := int32(silkCLZ32(inLin))
	var fracQ7 int32
	if lz <= 24 {
		fracQ7 = (inLin >> (24 - lz)) & 0x7f
	} else {
		fracQ7 = (inLin << (lz - 24)) & 0x7f
	}

	return silkADD_LSHIFT32(silkSMLAWB(fracQ7, silkMUL(fracQ7, 128-fracQ7), 179), 31-lz, 7)
}
