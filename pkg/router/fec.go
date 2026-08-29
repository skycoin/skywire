// Package router pkg/router/fec.go — forward-error-correction primitive for the
// mux data plane (skycoin/skywire #4270). Aggregating one ordered stream across
// heterogeneous-RTT legs is head-of-line-bound: the no-skip reorder frontier
// waits for whatever frames were striped onto a slow leg. Retransmit recovery is
// coupled to that slow leg's RTT. Erasure coding removes the wait: the receiver
// reconstructs a missing/late data symbol from REPAIR symbols that arrived on the
// FAST legs, so recovery delay is independent of the slow leg (the escape route
// (c) from the in-order-stream wall; see the research survey in memory).
//
// This file is the coding core only — a systematic Cauchy-Reed-Solomon block
// erasure code over GF(2^8): K data symbols are transmitted unchanged (systematic
// → zero decode cost when nothing is lost) plus R repair symbols; the receiver
// recovers ALL K data symbols from ANY K of the K+R (MDS). Integration into the
// mux striping / reorder buffer is a separate step; keeping the code standalone
// makes it exhaustively unit-testable without the live mesh.
package router

import "errors"

// --- GF(2^8) arithmetic (polynomial 0x11d, the RS/AES field) ---

var (
	gfExp [512]byte // gfExp[i] = generator^i, doubled so a+b (<510) needs no mod
	gfLog [256]byte // gfLog[gfExp[i]] = i
)

func init() {
	x := byte(1)
	for i := 0; i < 255; i++ {
		gfExp[i] = x
		gfLog[x] = byte(i)
		// multiply x by the generator (2) in GF(2^8) with poly 0x11d
		hi := x & 0x80
		x <<= 1
		if hi != 0 {
			x ^= 0x1d
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[int(gfLog[a])+int(gfLog[b])]
}

func gfInv(a byte) byte {
	// a^(254) = a^(-1) for a != 0; via logs: exp[255 - log[a]].
	return gfExp[255-int(gfLog[a])]
}

// --- systematic Cauchy-Reed-Solomon block coder ---

// fecBlockCoder encodes K data symbols into R repair symbols and decodes any K of
// the K+R back to the original data. Symbols are equal-length byte slices; the
// caller pads/chunks the stream to symLen. Immutable after construction; safe for
// concurrent Encode/Decode.
type fecBlockCoder struct {
	k, r, symLen int
	cauchy       [][]byte // R x K generator rows for the repair symbols
}

// newFECBlockCoder builds a coder for k data + r repair symbols of symLen bytes.
// The R×K generator is a Cauchy matrix cauchy[i][j] = 1 / (x_i XOR y_j) with
// x_i = i and y_j = R+j (all distinct in GF(2^8)), which — stacked under the K×K
// identity for the systematic data rows — makes EVERY K-row subset invertible, so
// any K received symbols recover the data (the MDS property). Requires
// k+r <= 256. Returns nil for invalid dimensions.
func newFECBlockCoder(k, r, symLen int) *fecBlockCoder {
	if k <= 0 || r < 0 || symLen <= 0 || k+r > 256 {
		return nil
	}
	cauchy := make([][]byte, r)
	for i := 0; i < r; i++ {
		row := make([]byte, k)
		xi := byte(i)
		for j := 0; j < k; j++ {
			yj := byte(r + j)       //nolint:gosec // r+j < r+k <= 256, rejected above
			row[j] = gfInv(xi ^ yj) // xi != yj always (i<r<=r+j), so xor != 0
		}
		cauchy[i] = row
	}
	return &fecBlockCoder{k: k, r: r, symLen: symLen, cauchy: cauchy}
}

// Encode returns the R repair symbols for the K data symbols. data must have
// exactly K entries, each symLen bytes. repair[i] = Σ_j cauchy[i][j] * data[j].
func (c *fecBlockCoder) Encode(data [][]byte) ([][]byte, error) {
	if len(data) != c.k {
		return nil, errors.New("fec: Encode needs exactly k data symbols")
	}
	for _, d := range data {
		if len(d) != c.symLen {
			return nil, errors.New("fec: data symbol length mismatch")
		}
	}
	repair := make([][]byte, c.r)
	for i := 0; i < c.r; i++ {
		out := make([]byte, c.symLen)
		for j := 0; j < c.k; j++ {
			coeff := c.cauchy[i][j]
			if coeff == 0 {
				continue
			}
			d := data[j]
			for b := 0; b < c.symLen; b++ {
				out[b] ^= gfMul(coeff, d[b])
			}
		}
		repair[i] = out
	}
	return repair, nil
}

// Decode recovers all K data symbols. recv has K+R slots (indices 0..K-1 are the
// data symbols, K..K+R-1 the repair symbols); present[i] reports whether slot i
// arrived. Requires at least K present slots. Present data slots are returned
// as-is (systematic); missing ones are reconstructed from the repair rows.
func (c *fecBlockCoder) Decode(recv [][]byte, present []bool) ([][]byte, error) {
	n := c.k + c.r
	if len(recv) != n || len(present) != n {
		return nil, errors.New("fec: Decode needs k+r recv/present slots")
	}
	// Fast path: all data present → nothing to reconstruct.
	allData := true
	for i := 0; i < c.k; i++ {
		if !present[i] {
			allData = false
			break
		}
	}
	if allData {
		out := make([][]byte, c.k)
		copy(out, recv[:c.k])
		return out, nil
	}

	// Collect K present rows of the full generator G = [ I_k ; Cauchy ].
	// Row for data slot j is e_j; row for repair slot i is cauchy[i].
	rows := make([][]byte, 0, c.k) // K x K coefficient rows
	rhs := make([][]byte, 0, c.k)  // the corresponding received symbols
	for idx := 0; idx < n && len(rows) < c.k; idx++ {
		if !present[idx] {
			continue
		}
		if len(recv[idx]) != c.symLen {
			return nil, errors.New("fec: present symbol length mismatch")
		}
		row := make([]byte, c.k)
		if idx < c.k { // systematic data row = unit vector
			row[idx] = 1
		} else {
			copy(row, c.cauchy[idx-c.k])
		}
		rows = append(rows, row)
		rhs = append(rhs, recv[idx])
	}
	if len(rows) < c.k {
		return nil, errors.New("fec: not enough symbols to decode (need k)")
	}

	// Solve rows * data = rhs for data via Gauss-Jordan over GF(2^8), applying
	// the same row ops to rhs (each rhs entry is a whole symbol vector).
	if err := gfSolve(rows, rhs, c.symLen); err != nil {
		return nil, err
	}
	out := make([][]byte, c.k)
	for j := 0; j < c.k; j++ {
		out[j] = rhs[j]
	}
	return out, nil
}

// gfSolve solves a[K][K] * x = b (b is K symbol-vectors of symLen bytes) in place
// by Gauss-Jordan elimination over GF(2^8); on return b holds x. The matrix is a
// K-subset of the MDS generator so it is always invertible; a zero pivot with no
// swap available therefore signals a caller/logic error.
func gfSolve(a [][]byte, b [][]byte, symLen int) error {
	k := len(a)
	for col := 0; col < k; col++ {
		// find pivot
		piv := -1
		for r := col; r < k; r++ {
			if a[r][col] != 0 {
				piv = r
				break
			}
		}
		if piv < 0 {
			return errors.New("fec: singular matrix (should not happen for MDS subset)")
		}
		if piv != col {
			a[piv], a[col] = a[col], a[piv]
			b[piv], b[col] = b[col], b[piv]
		}
		// normalize pivot row so a[col][col] == 1
		inv := gfInv(a[col][col])
		if inv != 1 {
			for j := col; j < k; j++ {
				a[col][j] = gfMul(a[col][j], inv)
			}
			for x := 0; x < symLen; x++ {
				b[col][x] = gfMul(b[col][x], inv)
			}
		}
		// eliminate the column from every other row
		for r := 0; r < k; r++ {
			if r == col || a[r][col] == 0 {
				continue
			}
			f := a[r][col]
			for j := col; j < k; j++ {
				a[r][j] ^= gfMul(f, a[col][j])
			}
			br, bc := b[r], b[col]
			for x := 0; x < symLen; x++ {
				br[x] ^= gfMul(f, bc[x])
			}
		}
	}
	return nil
}
