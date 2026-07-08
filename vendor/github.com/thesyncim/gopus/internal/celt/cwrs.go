// Package celt implements the CELT (Constrained-Energy Lapped Transform) layer
// of the Opus codec as specified in RFC 6716 Section 4.3.
package celt

// CWRS (Combinatorial Radix-based With Signs) implements combinatorial indexing
// for PVQ (Pyramid Vector Quantization) decoding. This is the core algorithm
// for decoding normalized band vectors from compact indices.
//
// This implementation uses a precomputed static table matching libopus celt/cwrs.c
// for O(1) lookup of U(N,K) values, eliminating map lookups and recursive calls
// in the hot path.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/cwrs.c

// Constants for CWRS
const (
	// MaxPVQK is the maximum number of pulses we support in PVQ coding.
	MaxPVQK = 128
	// MaxPVQN is the maximum number of dimensions we support.
	MaxPVQN = 256
)

// pvqUData is the precomputed U(N,K) table from libopus celt/cwrs.c.
// U(N,K) = U(K,N) is symmetric, and the table stores rows for small N values.
//
// For each row N, we store U(N, K) for K values starting from N up to the maximum
// that fits in 32 bits or is needed for Opus decoding.
//
// Reference: libopus celt/cwrs.c lines 232-431 (CELT_PVQ_U_DATA)
var pvqUData = [...]uint32{
	// N=0, K=0...176:
	1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0,
	// N=1, K=1...176:
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1,
	// N=2, K=2...176:
	3, 5, 7, 9, 11, 13, 15, 17, 19, 21, 23, 25, 27, 29, 31, 33, 35, 37, 39, 41,
	43, 45, 47, 49, 51, 53, 55, 57, 59, 61, 63, 65, 67, 69, 71, 73, 75, 77, 79,
	81, 83, 85, 87, 89, 91, 93, 95, 97, 99, 101, 103, 105, 107, 109, 111, 113,
	115, 117, 119, 121, 123, 125, 127, 129, 131, 133, 135, 137, 139, 141, 143,
	145, 147, 149, 151, 153, 155, 157, 159, 161, 163, 165, 167, 169, 171, 173,
	175, 177, 179, 181, 183, 185, 187, 189, 191, 193, 195, 197, 199, 201, 203,
	205, 207, 209, 211, 213, 215, 217, 219, 221, 223, 225, 227, 229, 231, 233,
	235, 237, 239, 241, 243, 245, 247, 249, 251, 253, 255, 257, 259, 261, 263,
	265, 267, 269, 271, 273, 275, 277, 279, 281, 283, 285, 287, 289, 291, 293,
	295, 297, 299, 301, 303, 305, 307, 309, 311, 313, 315, 317, 319, 321, 323,
	325, 327, 329, 331, 333, 335, 337, 339, 341, 343, 345, 347, 349, 351,
	// N=3, K=3...176:
	13, 25, 41, 61, 85, 113, 145, 181, 221, 265, 313, 365, 421, 481, 545, 613,
	685, 761, 841, 925, 1013, 1105, 1201, 1301, 1405, 1513, 1625, 1741, 1861,
	1985, 2113, 2245, 2381, 2521, 2665, 2813, 2965, 3121, 3281, 3445, 3613, 3785,
	3961, 4141, 4325, 4513, 4705, 4901, 5101, 5305, 5513, 5725, 5941, 6161, 6385,
	6613, 6845, 7081, 7321, 7565, 7813, 8065, 8321, 8581, 8845, 9113, 9385, 9661,
	9941, 10225, 10513, 10805, 11101, 11401, 11705, 12013, 12325, 12641, 12961,
	13285, 13613, 13945, 14281, 14621, 14965, 15313, 15665, 16021, 16381, 16745,
	17113, 17485, 17861, 18241, 18625, 19013, 19405, 19801, 20201, 20605, 21013,
	21425, 21841, 22261, 22685, 23113, 23545, 23981, 24421, 24865, 25313, 25765,
	26221, 26681, 27145, 27613, 28085, 28561, 29041, 29525, 30013, 30505, 31001,
	31501, 32005, 32513, 33025, 33541, 34061, 34585, 35113, 35645, 36181, 36721,
	37265, 37813, 38365, 38921, 39481, 40045, 40613, 41185, 41761, 42341, 42925,
	43513, 44105, 44701, 45301, 45905, 46513, 47125, 47741, 48361, 48985, 49613,
	50245, 50881, 51521, 52165, 52813, 53465, 54121, 54781, 55445, 56113, 56785,
	57461, 58141, 58825, 59513, 60205, 60901, 61601,
	// N=4, K=4...176:
	63, 129, 231, 377, 575, 833, 1159, 1561, 2047, 2625, 3303, 4089, 4991, 6017,
	7175, 8473, 9919, 11521, 13287, 15225, 17343, 19649, 22151, 24857, 27775,
	30913, 34279, 37881, 41727, 45825, 50183, 54809, 59711, 64897, 70375, 76153,
	82239, 88641, 95367, 102425, 109823, 117569, 125671, 134137, 142975, 152193,
	161799, 171801, 182207, 193025, 204263, 215929, 228031, 240577, 253575,
	267033, 280959, 295361, 310247, 325625, 341503, 357889, 374791, 392217,
	410175, 428673, 447719, 467321, 487487, 508225, 529543, 551449, 573951,
	597057, 620775, 645113, 670079, 695681, 721927, 748825, 776383, 804609,
	833511, 863097, 893375, 924353, 956039, 988441, 1021567, 1055425, 1090023,
	1125369, 1161471, 1198337, 1235975, 1274393, 1313599, 1353601, 1394407,
	1436025, 1478463, 1521729, 1565831, 1610777, 1656575, 1703233, 1750759,
	1799161, 1848447, 1898625, 1949703, 2001689, 2054591, 2108417, 2163175,
	2218873, 2275519, 2333121, 2391687, 2451225, 2511743, 2573249, 2635751,
	2699257, 2763775, 2829313, 2895879, 2963481, 3032127, 3101825, 3172583,
	3244409, 3317311, 3391297, 3466375, 3542553, 3619839, 3698241, 3777767,
	3858425, 3940223, 4023169, 4107271, 4192537, 4278975, 4366593, 4455399,
	4545401, 4636607, 4729025, 4822663, 4917529, 5013631, 5110977, 5209575,
	5309433, 5410559, 5512961, 5616647, 5721625, 5827903, 5935489, 6044391,
	6154617, 6266175, 6379073, 6493319, 6608921, 6725887, 6844225, 6963943,
	7085049, 7207551,
	// N=5, K=5...176:
	321, 681, 1289, 2241, 3649, 5641, 8361, 11969, 16641, 22569, 29961, 39041,
	50049, 63241, 78889, 97281, 118721, 143529, 172041, 204609, 241601, 283401,
	330409, 383041, 441729, 506921, 579081, 658689, 746241, 842249, 947241,
	1061761, 1186369, 1321641, 1468169, 1626561, 1797441, 1981449, 2179241,
	2391489, 2618881, 2862121, 3121929, 3399041, 3694209, 4008201, 4341801,
	4695809, 5071041, 5468329, 5888521, 6332481, 6801089, 7295241, 7815849,
	8363841, 8940161, 9545769, 10181641, 10848769, 11548161, 12280841, 13047849,
	13850241, 14689089, 15565481, 16480521, 17435329, 18431041, 19468809,
	20549801, 21675201, 22846209, 24064041, 25329929, 26645121, 28010881,
	29428489, 30899241, 32424449, 34005441, 35643561, 37340169, 39096641,
	40914369, 42794761, 44739241, 46749249, 48826241, 50971689, 53187081,
	55473921, 57833729, 60268041, 62778409, 65366401, 68033601, 70781609,
	73612041, 76526529, 79526721, 82614281, 85790889, 89058241, 92418049,
	95872041, 99421961, 103069569, 106816641, 110664969, 114616361, 118672641,
	122835649, 127107241, 131489289, 135983681, 140592321, 145317129, 150160041,
	155123009, 160208001, 165417001, 170752009, 176215041, 181808129, 187533321,
	193392681, 199388289, 205522241, 211796649, 218213641, 224775361, 231483969,
	238341641, 245350569, 252512961, 259831041, 267307049, 274943241, 282741889,
	290705281, 298835721, 307135529, 315607041, 324252609, 333074601, 342075401,
	351257409, 360623041, 370174729, 379914921, 389846081, 399970689, 410291241,
	420810249, 431530241, 442453761, 453583369, 464921641, 476471169, 488234561,
	500214441, 512413449, 524834241, 537479489, 550351881, 563454121, 576788929,
	590359041, 604167209, 618216201, 632508801,
	// N=6, K=6...96:
	1683, 3653, 7183, 13073, 22363, 36365, 56695, 85305, 124515, 177045, 246047,
	335137, 448427, 590557, 766727, 982729, 1244979, 1560549, 1937199, 2383409,
	2908411, 3522221, 4235671, 5060441, 6009091, 7095093, 8332863, 9737793,
	11326283, 13115773, 15124775, 17372905, 19880915, 22670725, 25765455,
	29189457, 32968347, 37129037, 41699767, 46710137, 52191139, 58175189,
	64696159, 71789409, 79491819, 87841821, 96879431, 106646281, 117185651,
	128542501, 140763503, 153897073, 167993403, 183104493, 199284183, 216588185,
	235074115, 254801525, 275831935, 298228865, 322057867, 347386557, 374284647,
	402823977, 433078547, 465124549, 499040399, 534906769, 572806619, 612825229,
	655050231, 699571641, 746481891, 795875861, 847850911, 902506913, 959946283,
	1020274013, 1083597703, 1150027593, 1219676595, 1292660325, 1369097135,
	1449108145, 1532817275, 1620351277, 1711839767, 1807415257, 1907213187,
	2011371957, 2120032959,
	// N=7, K=7...54
	8989, 19825, 40081, 75517, 134245, 227305, 369305, 579125, 880685, 1303777,
	1884961, 2668525, 3707509, 5064793, 6814249, 9041957, 11847485, 15345233,
	19665841, 24957661, 31388293, 39146185, 48442297, 59511829, 72616013,
	88043969, 106114625, 127178701, 151620757, 179861305, 212358985, 249612805,
	292164445, 340600625, 395555537, 457713341, 527810725, 606639529, 695049433,
	793950709, 904317037, 1027188385, 1163673953, 1314955181, 1482288821,
	1667010073, 1870535785, 2094367717,
	// N=8, K=8...37
	48639, 108545, 224143, 433905, 795455, 1392065, 2340495, 3800305, 5984767,
	9173505, 13726991, 20103025, 28875327, 40754369, 56610575, 77500017,
	104692735, 139703809, 184327311, 240673265, 311207743, 398796225, 506750351,
	638878193, 799538175, 993696769, 1226990095, 1505789553, 1837271615,
	2229491905,
	// N=9, K=9...28:
	265729, 598417, 1256465, 2485825, 4673345, 8405905, 14546705, 24331777,
	39490049, 62390545, 96220561, 145198913, 214828609, 312193553, 446304145,
	628496897, 872893441, 1196924561, 1621925137, 2173806145,
	// N=10, K=10...24:
	1462563, 3317445, 7059735, 14218905, 27298155, 50250765, 89129247, 152951073,
	254831667, 413442773, 654862247, 1014889769, 1541911931, 2300409629,
	3375210671,
	// N=11, K=11...19:
	8097453, 18474633, 39753273, 81270333, 158819253, 298199265, 540279585,
	948062325, 1616336765,
	// N=12, K=12...18:
	45046719, 103274625, 224298231, 464387817, 921406335, 1759885185,
	3248227095,
	// N=13, K=13...16:
	251595969, 579168825, 1267854873, 2653649025,
	// N=14, K=14:
	1409933619,
}

// pvqURow contains pointers (as offsets) into pvqUData for each row N.
// CELT_PVQ_U_ROW[N] points to the start of U(N, K) values for row N.
//
// The table is structured so that for row N:
//   - N=0: U(0, 0..176)
//   - N=1: U(1, 1..176)
//   - N=2: U(2, 2..176)
//   - etc.
//
// To access U(N, K) where N <= K, use: pvqUData[pvqURow[N] + (K - N)]
// Due to symmetry U(N,K) = U(K,N), we always access with min(N,K) as the row.
var pvqURow = [15]int{
	0,    // N=0: starts at offset 0, length 177
	177,  // N=1: starts at offset 177, length 176
	353,  // N=2: starts at offset 353, length 175
	528,  // N=3: starts at offset 528, length 174
	702,  // N=4: starts at offset 702, length 173
	875,  // N=5: starts at offset 875, length 172
	1047, // N=6: starts at offset 1047, length 91
	1138, // N=7: starts at offset 1138, length 48
	1186, // N=8: starts at offset 1186, length 30
	1216, // N=9: starts at offset 1216, length 20
	1236, // N=10: starts at offset 1236, length 15
	1251, // N=11: starts at offset 1251, length 9
	1260, // N=12: starts at offset 1260, length 7
	1267, // N=13: starts at offset 1267, length 4
	1271, // N=14: starts at offset 1271, length 1
}

// pvqURowLen contains the length of each row (max K value - N + 1).
var pvqURowLen = [15]int{
	177, // N=0: K=0..176
	176, // N=1: K=1..176
	175, // N=2: K=2..176
	174, // N=3: K=3..176
	173, // N=4: K=4..176
	172, // N=5: K=5..176
	91,  // N=6: K=6..96
	48,  // N=7: K=7..54
	30,  // N=8: K=8..37
	20,  // N=9: K=9..28
	15,  // N=10: K=10..24
	9,   // N=11: K=11..19
	7,   // N=12: K=12..18
	4,   // N=13: K=13..16
	1,   // N=14: K=14
}

// pvqUDense matches libopus's row/column access pattern directly:
// pvqUDense[n][k] == U(n,k) for every table-covered pair.
var pvqUDense = func() [15][177]uint32 {
	var dense [15][177]uint32
	for n, start := range pvqURow {
		rowLen := pvqURowLen[n]
		copy(dense[n][n:n+rowLen], pvqUData[start:start+rowLen])
	}
	return dense
}()

//go:nosplit
func pvqUDenseUnchecked(row, col int) uint32 {
	return pvqUDense[row][col]
}

// pvqUTableLookup performs a direct table lookup for U(n, k).
// Returns (value, ok) where ok is true if the lookup succeeded.
// If the lookup fails (n or k out of range), falls back to computation.
//
//go:nosplit
func pvqUTableLookup(n, k int) (uint32, bool) {
	// U(n,k) = U(k,n) due to symmetry - use smaller as row index
	if n > k {
		n, k = k, n
	}

	// Check if we have this value in the table
	if n < 0 || n >= 15 {
		return 0, false
	}

	// Calculate offset into the data
	offset := k - n
	if offset < 0 || offset >= pvqURowLen[n] {
		return 0, false
	}

	return pvqUData[pvqURow[n]+offset], true
}

// pvqUHasLookup reports whether U(n,k) is available in the static table.
func pvqUHasLookup(n, k int) bool {
	if n > k {
		n, k = k, n
	}
	if n < 0 || n >= len(pvqURowLen) {
		return false
	}
	offset := k - n
	return offset >= 0 && offset < pvqURowLen[n]
}

// canUseCWRSFast reports whether cwrsiFast can decode (n,k) using only table lookups.
func canUseCWRSFast(n, k int) bool {
	if k <= 0 {
		return false
	}
	if n == 2 {
		return true
	}
	if n < 2 {
		return false
	}
	maxRows := len(pvqURowLen)
	if k >= n {
		if n >= maxRows {
			return false
		}
		// Needs U(n,k+1), i.e. offset (k+1-n) in row n.
		return (k + 1 - n) < pvqURowLen[n]
	}
	if k+1 >= maxRows {
		return false
	}
	// Needs U(k,n) and U(k+1,n), i.e. offsets (n-k) and (n-k-1).
	offset0 := n - k
	offset1 := offset0 - 1
	return offset0 < pvqURowLen[k] && offset1 < pvqURowLen[k+1]
}

// canUseICWRSLookupFast reports whether the encoder-side CWRS index walk for
// (n,k) can stay entirely on direct U-table lookups without per-step range
// checks. This is slightly broader than canUseCWRSFast because encode only
// needs the table walk, not the decoder's specialized cwrsiFast path.
func canUseICWRSLookupFast(n, k int) bool {
	if n < 2 || k <= 0 {
		return false
	}
	if n <= k {
		if n < 0 || n >= len(pvqURowLen) {
			return false
		}
		return k+1-n < pvqURowLen[n]
	}
	if k+1 >= len(pvqURowLen) {
		return false
	}
	return n-k < pvqURowLen[k] && n-k-1 < pvqURowLen[k+1]
}

// PVQ_V computes V(N,K), the total number of PVQ codewords with N dimensions
// and K pulses (where the sum of absolute values equals K).
//
// V(N,K) = U(N,K) + U(N,K+1)
//
// This uses O(1) table lookup when possible, falling back to computation
// only for values outside the table range.
//
// Reference: RFC 6716 Section 4.3.4.1, libopus celt/cwrs.c
func PVQ_V(n, k int) uint32 {
	if k < 0 {
		return 0
	}
	if k == 0 {
		return 1 // Only the zero vector
	}
	if n <= 0 {
		return 0 // No dimensions
	}
	if n == 1 {
		return 2 // Only +K and -K
	}

	if n <= k {
		if n >= 0 && n < len(pvqURowLen) && k+1-n < pvqURowLen[n] {
			row := pvqUDense[n][:]
			return row[k] + row[k+1]
		}
	} else if k+1 < len(pvqURowLen) && n-k < pvqURowLen[k] && n-k-1 < pvqURowLen[k+1] {
		return pvqUDense[k][n] + pvqUDense[k+1][n]
	}

	// Try table lookup: V(n,k) = U(n,k) + U(n,k+1)
	u1, ok1 := pvqUTableLookup(n, k)
	u2, ok2 := pvqUTableLookup(n, k+1)

	if ok1 && ok2 {
		return u1 + u2
	}

	// Fallback to computation for values outside table
	return pvqVCompute(n, k)
}

// pvqVCompute computes V(N,K) using the recurrence relation.
// This is the fallback for values outside the precomputed table.
func pvqVCompute(n, k int) uint32 {
	if k < 0 {
		return 0
	}
	if k == 0 {
		return 1
	}
	if n <= 0 {
		return 0
	}
	if n == 1 {
		return 2
	}

	// For larger values, compute using the row-based algorithm
	// This is more efficient than recursive computation
	u := make([]uint32, k+2)
	return ncwrsUrow(n, k, u)
}

// PVQ_U computes U(N,K), the number of codewords where the first position has no pulse.
// U(N,K) = V(N-1, K) for N > 1
// U(1,K) = K (special handling in libopus)
//
// Note: This returns the "counting" U function where U(N,K) = V(N-1,K),
// not the internal table U used in the recurrence relation.
func PVQ_U(n, k int) uint32 {
	if k <= 0 {
		return 0
	}
	if n <= 1 {
		return uint32(k)
	}

	// U(N,K) = V(N-1, K) by definition
	return PVQ_V(n-1, k)
}

// abs returns the absolute value of x.
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

//go:nosplit
func pvqUTableLookupFast(n, k int) uint32 {
	if n > k {
		n, k = k, n
	}
	return pvqUDenseUnchecked(n, k)
}

// unext computes the next row/column of any recurrence that obeys the relation
// u[i][j]=u[i-1][j]+u[i][j-1]+u[i-1][j-1].
// u0 is the base case for the new row/column.
func unext(u []uint32, length int, u0 uint32) {
	if length < 2 {
		return
	}
	_ = u[length-1] // BCE
	j := 1
	// Unroll by 2: the recurrence is sequential (each step depends on previous),
	// but we can reduce loop overhead by processing two iterations per cycle.
	for ; j+1 < length; j += 2 {
		u1 := u[j] + u[j-1] + u0
		u[j-1] = u0
		u2 := u[j+1] + u[j] + u1
		u[j] = u1
		u0 = u2
	}
	for ; j < length; j++ {
		u1 := u[j] + u[j-1] + u0
		u[j-1] = u0
		u0 = u1
	}
	u[length-1] = u0
}

// uprev computes the previous row/column of any recurrence that obeys the relation
// u[i-1][j]=u[i][j]-u[i][j-1]-u[i-1][j-1].
// u0 is the base case for the new row/column.
func uprev(u []uint32, length int, u0 uint32) {
	if length < 2 {
		return
	}
	_ = u[length-1] // BCE
	j := 1
	for ; j+1 < length; j += 2 {
		u1 := u[j] - u[j-1] - u0
		u[j-1] = u0
		u2 := u[j+1] - u[j] - u1
		u[j] = u1
		u0 = u2
	}
	for ; j < length; j++ {
		u1 := u[j] - u[j-1] - u0
		u[j-1] = u0
		u0 = u1
	}
	u[length-1] = u0
}

// findLargestLEInU returns the largest index idx in u[0:hi+1] such that u[idx] <= target.
// u must be non-decreasing in the searched range.
func findLargestLEInU(u []uint32, hi int, target uint32) int {
	if hi <= 0 {
		return 0
	}
	if hi >= len(u) {
		hi = len(u) - 1
	}
	if target >= u[hi] {
		return hi
	}
	lo := 0
	for lo < hi {
		mid := (lo + hi + 1) >> 1
		if u[mid] <= target {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo
}

// ncwrsUrow computes V(n,k) and fills u with U(n,0..k+1).
// u must have length at least k+2.
func ncwrsUrow(n, k int, u []uint32) uint32 {
	if n < 2 || k <= 0 || len(u) < k+2 {
		return 0
	}
	_ = u[k+1] // BCE
	u[0] = 0
	u[1] = 1
	j := 2
	for ; j+1 < k+2; j += 2 {
		u[j] = uint32((j << 1) - 1)
		u[j+1] = uint32(((j + 1) << 1) - 1)
	}
	for ; j < k+2; j++ {
		u[j] = uint32((j << 1) - 1)
	}
	for j := 2; j < n; j++ {
		unext(u[1:], k+1, 1)
	}
	return u[k] + u[k+1]
}

// cwrsiFast is the optimized decoder using the precomputed table.
// For small n where table lookup is available, it uses O(1) lookups.
// For larger n, it falls back to the row-based algorithm.
func setCWRSOnePulse(y []int, start, end int, index uint32) {
	n := end - start
	idx := int(index)
	if idx < n {
		y[start+idx] = 1
	} else {
		y[start+(n<<1)-1-idx] = -1
	}
}

func finishCWRSOnePulse(y []int, start, end int, index uint32) {
	clear(y[start:end])
	setCWRSOnePulse(y, start, end, index)
}

func finishCWRSTwoPulses(y []int, n int, index uint32) uint32 {
	clear(y[:n])
	start := 0
	rem := n
	idx := int(index)
	for {
		if rem == 1 {
			if idx&1 == 1 {
				y[start] = -2
			} else {
				y[start] = 2
			}
			return 4
		}
		if idx == 0 {
			y[start] = 2
			return 4
		}
		idx--
		oneCount := (rem - 1) << 1
		if idx < oneCount {
			y[start] = 1
			setCWRSOnePulse(y, start+1, n, uint32(idx))
			return 2
		}
		idx -= oneCount
		zeroCount := ((rem - 1) * (rem - 1)) << 1
		if idx < zeroCount {
			start++
			rem--
			continue
		}
		idx -= zeroCount
		if idx == 0 {
			y[start] = -2
			return 4
		}
		idx--
		y[start] = -1
		setCWRSOnePulse(y, start+1, n, uint32(idx))
		return 2
	}
}

func setCWRSOnePulse32(y []int32, start, end int, index uint32) {
	n := end - start
	idx := int(index)
	if idx < n {
		y[start+idx] = 1
	} else {
		y[start+(n<<1)-1-idx] = -1
	}
}

func finishCWRSOnePulse32(y []int32, start, end int, index uint32) {
	clear(y[start:end])
	setCWRSOnePulse32(y, start, end, index)
}

func finishCWRSTwoPulses32(y []int32, n int, index uint32) uint32 {
	clear(y[:n])
	return finishCWRSTwoPulses32NoClear(y, n, index)
}

func finishCWRSTwoPulses32NoClear(y []int32, n int, index uint32) uint32 {
	start := 0
	rem := n
	idx := int(index)
	for {
		if rem == 1 {
			if idx&1 == 1 {
				y[start] = -2
			} else {
				y[start] = 2
			}
			return 4
		}
		if idx == 0 {
			y[start] = 2
			return 4
		}
		idx--
		oneCount := (rem - 1) << 1
		if idx < oneCount {
			y[start] = 1
			setCWRSOnePulse32(y, start+1, n, uint32(idx))
			return 2
		}
		idx -= oneCount
		zeroCount := ((rem - 1) * (rem - 1)) << 1
		if idx < zeroCount {
			start++
			rem--
			continue
		}
		idx -= zeroCount
		if idx == 0 {
			y[start] = -2
			return 4
		}
		idx--
		y[start] = -1
		setCWRSOnePulse32(y, start+1, n, uint32(idx))
		return 2
	}
}

//go:nosplit
func cwrsiFast(n, k int, i uint32, y []int) uint32 {
	if n < 2 || k <= 0 || len(y) < n {
		return 0
	}
	y = y[:n:n]
	return cwrsiFastCore(n, k, i, y)
}

// cwrsi is the fallback decoder using dynamic row computation.
// Used when table lookup is not available.
func cwrsi(n, k int, i uint32, y []int, u []uint32) uint32 {
	if n <= 0 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0
	}
	j := 0
	var yy uint32
	for {
		p := u[k+1]
		sign := 0
		if i >= p {
			sign = -1
			i -= p
		}
		yj := k
		k = findLargestLEInU(u, k, i)
		p = u[k]
		i -= p
		yj -= k
		val := yj
		if sign != 0 {
			val = -yj
		}
		y[j] = val
		yy += uint32(val * val)
		uprev(u, k+2, 0)
		j++
		if j >= n {
			break
		}
	}
	return yy
}

func cwrsi32(n, k int, i uint32, y []int32, u []uint32) uint32 {
	if n <= 0 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0
	}
	j := 0
	var yy uint32
	for {
		p := u[k+1]
		sign := 0
		if i >= p {
			sign = -1
			i -= p
		}
		yj := k
		k = findLargestLEInU(u, k, i)
		p = u[k]
		i -= p
		yj -= k
		val := yj
		if sign != 0 {
			val = -yj
		}
		y[j] = int32(val)
		yy += uint32(val * val)
		uprev(u, k+2, 0)
		j++
		if j >= n {
			break
		}
	}
	return yy
}

// cwrsiTableLookup32 decodes the pulse vector for index i by reading U(n,k)
// directly from the static table, mirroring libopus celt/cwrs.c cwrsi. At
// position j the row-based cwrsi32 holds u == U(n-j, ·), so each access u[idx]
// equals pvqUTableLookupFast(n-j, idx); reading those by lookup as nCur (=n-j)
// decrements lets us skip materializing and stepping the U-row (ncwrsUrow + the
// per-position uprev), which dominate CELT band decode. Bit-identical to
// ncwrsUrow+cwrsi32, valid only when canUseCWRSFast(n,k) covers every lookup.
func cwrsiTableLookup32(n, k int, i uint32, y []int32) uint32 {
	if n <= 0 || k <= 0 || len(y) < n {
		return 0
	}
	var yy uint32
	for j := range n {
		nCur := n - j
		p := pvqUTableLookupFast(nCur, k+1)
		sign := 0
		if i >= p {
			sign = -1
			i -= p
		}
		k0 := k
		for k > 0 {
			p = pvqUTableLookupFast(nCur, k)
			if p <= i {
				break
			}
			k--
		}
		if k == 0 {
			p = 0
		}
		i -= p
		yj := k0 - k
		val := yj
		if sign != 0 {
			val = -yj
		}
		y[j] = int32(val)
		yy += uint32(yj * yj)
	}
	return yy
}

func icwrs1(y int) (uint32, int) {
	k := abs(y)
	if y < 0 {
		return 1, k
	}
	return 0, k
}

func icwrsLookupFast(n, k int, y []int) (uint32, bool) {
	if len(y) < n || !canUseICWRSLookupFast(n, k) {
		return 0, false
	}

	i, k1 := icwrs1(y[n-1])
	i += pvqUTableLookupFast(2, k1)

	j := n - 2
	k1 += abs(y[j])
	if y[j] < 0 {
		i += pvqUTableLookupFast(2, k1+1)
	}

	for j--; j >= 0; j-- {
		remDims := n - j
		i += pvqUTableLookupFast(remDims, k1)
		k1 += abs(y[j])
		if y[j] < 0 {
			i += pvqUTableLookupFast(remDims, k1+1)
		}
	}

	return i, true
}

func icwrsLookupFast32(n, k int, y []int32) (uint32, bool) {
	if len(y) < n || !canUseICWRSLookupFast(n, k) {
		return 0, false
	}

	i, k1 := icwrs1(int(y[n-1]))
	i += pvqUTableLookupFast(2, k1)

	j := n - 2
	k1 += int(absInt32(y[j]))
	if y[j] < 0 {
		i += pvqUTableLookupFast(2, k1+1)
	}

	for j--; j >= 0; j-- {
		remDims := n - j
		i += pvqUTableLookupFast(remDims, k1)
		k1 += int(absInt32(y[j]))
		if y[j] < 0 {
			i += pvqUTableLookupFast(remDims, k1+1)
		}
	}

	return i, true
}

func icwrs(n, k int, y []int, u []uint32) (uint32, uint32) {
	if n < 2 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0, 0
	}
	_ = u[k+1] // BCE
	_ = y[n-1] // BCE
	u[0] = 0
	kk := 1
	for ; kk+1 <= k+1; kk += 2 {
		u[kk] = uint32((kk << 1) - 1)
		u[kk+1] = uint32(((kk + 1) << 1) - 1)
	}
	for ; kk <= k+1; kk++ {
		u[kk] = uint32((kk << 1) - 1)
	}
	i, k1 := icwrs1(y[n-1])
	j := n - 2
	i += u[k1]
	k1 += abs(y[j])
	if y[j] < 0 {
		i += u[k1+1]
	}
	for j--; j >= 0; j-- {
		unext(u, k+2, 0)
		i += u[k1]
		k1 += abs(y[j])
		if y[j] < 0 {
			i += u[k1+1]
		}
	}
	return i, u[k1] + u[k1+1]
}

func icwrs32(n, k int, y []int32, u []uint32) (uint32, uint32) {
	if n < 2 || k <= 0 || len(y) < n || len(u) < k+2 {
		return 0, 0
	}
	_ = u[k+1] // BCE
	_ = y[n-1] // BCE
	u[0] = 0
	kk := 1
	for ; kk+1 <= k+1; kk += 2 {
		u[kk] = uint32((kk << 1) - 1)
		u[kk+1] = uint32(((kk + 1) << 1) - 1)
	}
	for ; kk <= k+1; kk++ {
		u[kk] = uint32((kk << 1) - 1)
	}
	i, k1 := icwrs1(int(y[n-1]))
	j := n - 2
	i += u[k1]
	k1 += int(absInt32(y[j]))
	if y[j] < 0 {
		i += u[k1+1]
	}
	for j--; j >= 0; j-- {
		unext(u, k+2, 0)
		i += u[k1]
		k1 += int(absInt32(y[j]))
		if y[j] < 0 {
			i += u[k1+1]
		}
	}
	return i, u[k1] + u[k1+1]
}

// DecodePulses converts a CWRS index to a pulse vector.
//
// Parameters:
//   - index: the combinatorial index (0 to V(n,k)-1)
//   - n: number of dimensions (band width)
//   - k: total number of pulses (sum of absolute values)
//
// Returns: pulse vector of length n, where sum(|v[i]|) == k
//
// The algorithm walks through positions, determining how many pulses
// go at each position by counting codewords in the combinatorial structure.
//
// This implementation uses the precomputed table for O(1) lookups when possible.
//
// Reference: libopus celt/cwrs.c decode_pulses() / cwrsi()
func DecodePulses(index uint32, n, k int) []int {
	if n <= 0 || k < 0 {
		return nil
	}

	y := make([]int, n)

	if k == 0 {
		return y
	}

	if n == 1 {
		if index&1 == 1 {
			y[0] = -k
		} else {
			y[0] = k
		}
		return y
	}
	if k == 1 {
		finishCWRSOnePulse(y, 0, n, index)
		return y
	}
	if k == 2 {
		_ = finishCWRSTwoPulses(y, n, index)
		return y
	}

	if canUseCWRSFast(n, k) {
		_ = cwrsiFast(n, k, index, y)
	} else {
		// Fallback to row-based algorithm
		u := make([]uint32, k+2)
		ncwrsUrow(n, k, u)
		_ = cwrsi(n, k, index, y, u)
	}

	return y
}

// decodePulsesInto decodes a CWRS codeword into a pre-allocated buffer.
// This is the zero-allocation version of DecodePulses for use in hot paths.
//
// Parameters:
//   - index: CWRS codeword to decode
//   - n: number of dimensions (band width)
//   - k: number of pulses
//   - y: pre-allocated output buffer of length at least n
//   - scratch: optional scratch buffer for row computation
func decodePulsesInto(index uint32, n, k int, y []int, scratch *bandDecodeScratch) uint32 {
	if n <= 0 || k < 0 || len(y) < n {
		return 0
	}

	if k == 0 {
		// Only k==0 needs an explicit clear. For k>0 both CWRS decode paths
		// write all n outputs, so pre-clearing is redundant work.
		clear(y[:n])
		return 0
	}

	if n == 1 {
		if index&1 == 1 {
			y[0] = -k
		} else {
			y[0] = k
		}
		return uint32(k * k)
	}
	if k == 1 {
		finishCWRSOnePulse(y, 0, n, index)
		return 1
	}
	if k == 2 {
		return finishCWRSTwoPulses(y, n, index)
	}
	if n == 2 {
		_ = y[1]
		p := uint32(2*k + 1)
		neg0 := false
		if index >= p {
			neg0 = true
			index -= p
		}
		k1 := int((index + 1) >> 1)
		if k1 != 0 {
			index -= uint32(2*k1 - 1)
		}
		y0 := k - k1
		if neg0 {
			y0 = -y0
		}
		y1 := k1
		if index != 0 {
			y1 = -y1
		}
		y[0] = y0
		y[1] = y1
		return uint32(y0*y0 + y1*y1)
	}

	if canUseCWRSFast(n, k) {
		if n == 3 {
			p := pvqUDenseUnchecked(3, k+1)
			neg0 := false
			if index >= p {
				neg0 = true
				index -= p
			}

			k0 := k
			q := pvqUDenseUnchecked(3, 3)
			if q > index {
				k = 3
				for {
					k--
					p = pvqUDenseUnchecked(k, 3)
					if p <= index {
						break
					}
				}
			} else {
				for p = pvqUDenseUnchecked(3, k); p > index; p = pvqUDenseUnchecked(3, k) {
					k--
				}
			}
			index -= p
			y0 := k0 - k
			if neg0 {
				y0 = -y0
			}

			p = uint32(2*k + 1)
			neg1 := false
			if index >= p {
				neg1 = true
				index -= p
			}
			k2 := int((index + 1) >> 1)
			if k2 != 0 {
				index -= uint32(2*k2 - 1)
			}
			y1 := k - k2
			if neg1 {
				y1 = -y1
			}
			y2 := k2
			if index != 0 {
				y2 = -y2
			}
			y[0] = y0
			y[1] = y1
			y[2] = y2
			return uint32(y0*y0 + y1*y1 + y2*y2)
		}
		return cwrsiFastCore(n, k, index, y[:n:n])
	} else {
		// Use scratch buffer for u row if available, otherwise allocate
		var u []uint32
		if scratch != nil {
			u = scratch.ensureCWRSU(k + 2)
		} else {
			u = make([]uint32, k+2)
		}
		ncwrsUrow(n, k, u)
		return cwrsi(n, k, index, y, u)
	}
}

func decodePulsesInto32(index uint32, n, k int, y []int32, scratch *bandDecodeScratch) uint32 {
	if n <= 0 || k < 0 || len(y) < n {
		return 0
	}
	if k == 0 {
		clear(y[:n])
		return 0
	}

	if n == 1 {
		if index&1 == 1 {
			y[0] = int32(-k)
		} else {
			y[0] = int32(k)
		}
		return uint32(k * k)
	}
	if k == 1 {
		finishCWRSOnePulse32(y, 0, n, index)
		return 1
	}
	if k == 2 {
		return finishCWRSTwoPulses32(y, n, index)
	}
	if n == 2 {
		_ = y[1]
		p := uint32(2*k + 1)
		neg0 := false
		if index >= p {
			neg0 = true
			index -= p
		}
		k1 := int((index + 1) >> 1)
		if k1 != 0 {
			index -= uint32(2*k1 - 1)
		}
		y0 := k - k1
		if neg0 {
			y0 = -y0
		}
		y1 := k1
		if index != 0 {
			y1 = -y1
		}
		y[0] = int32(y0)
		y[1] = int32(y1)
		return uint32(y0*y0 + y1*y1)
	}
	if canUseCWRSFast(n, k) {
		return cwrsiTableLookup32(n, k, index, y)
	}
	var u []uint32
	if scratch != nil {
		u = scratch.ensureCWRSU(k + 2)
	} else {
		u = make([]uint32, k+2)
	}
	ncwrsUrow(n, k, u)
	return cwrsi32(n, k, index, y, u)
}

// EncodePulses converts a pulse vector to a CWRS index.
// This is the inverse of DecodePulses, useful for testing round-trip.
//
// Parameters:
//   - y: pulse vector of length n
//   - n: number of dimensions
//   - k: total number of pulses (should equal sum(|y[i]|))
//
// Returns: the combinatorial index (0 to V(n,k)-1)
func EncodePulses(y []int, n, k int) uint32 {
	return EncodePulsesScratch(y, n, k, nil)
}

// EncodePulsesScratch is the scratch-aware version of EncodePulses.
// It uses a pre-allocated u buffer to avoid allocations in the hot path.
func EncodePulsesScratch(y []int, n, k int, uBuf *[]uint32) uint32 {
	if n <= 0 || k < 0 || len(y) != n {
		return 0
	}

	// Verify sum of absolute values equals k
	sum := 0
	for _, v := range y {
		sum += abs(v)
	}
	if sum != k {
		return 0 // Invalid input
	}

	if n == 1 {
		if y[0] < 0 {
			return 1
		}
		return 0
	}
	if index, ok := icwrsLookupFast(n, k, y); ok {
		return index
	}

	var u []uint32
	if uBuf != nil {
		u = ensureUint32Slice(uBuf, k+2)
	} else {
		u = make([]uint32, k+2)
	}
	index, _ := icwrs(n, k, y, u)
	return index
}

func encodePulsesFast32(y []int32, n, k int, uBuf *[]uint32) uint32 {
	if n <= 0 || k < 0 {
		return 0
	}
	if n == 1 {
		if y[0] < 0 {
			return 1
		}
		return 0
	}
	if index, ok := icwrsLookupFast32(n, k, y); ok {
		return index
	}
	var u []uint32
	if uBuf != nil {
		u = ensureUint32Slice(uBuf, k+2)
	} else {
		u = make([]uint32, k+2)
	}
	index, _ := icwrs32(n, k, y, u)
	return index
}

// ClearCache is a no-op for compatibility.
// The new implementation uses a static table and doesn't need cache clearing.
func ClearCache() {
	// No-op: static table doesn't need clearing
}
