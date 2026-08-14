// Package vt implements the terminal emulation core of xterm.js 6.0.0
// (https://github.com/xtermjs/xterm.js, MIT) as pure Go: the VT500
// escape-sequence parser, the terminal buffer and the input handler.
// It has no browser dependencies and is fully testable with go test.
package vt

const (
	// maxParamValue is the max value supported for a single param/subparam
	// (clamped to the positive int32 range).
	maxParamValue = 0x7FFFFFFF
	// maxSubParamsLimit is the max allowed subparams for a single sequence
	// (hardcoded limitation).
	maxSubParamsLimit = 256
)

// Params is the parameter storage used by the parser to accumulate
// sequence parameters and sub parameters (the "1;2:3:4;5" part of CSI
// sequences).
//
// Notes carried over from the original:
//   - the params object passed to handlers is borrowed; use ToArray or
//     Clone for a copy
//   - never read beyond Length-1 (likely to contain arbitrary data)
//   - max value for a single (sub) param is 2^31-1, greater values clamp
//   - negative values other than -1 (default placeholder) are not allowed
//
// About ZDM (Zero Default Mode): the parser adds 0 for empty params;
// empty subparams are always added as -1.
type Params struct {
	Params []int32
	Length int

	maxLength          int
	maxSubParamsLength int
	subParams          []int32
	subParamsLength    int
	subParamsIdx       []uint16
	rejectDigits       bool
	rejectSubDigits    bool
	digitIsSub         bool
}

// NewParams creates a Params store with default capacity (32/32).
func NewParams() *Params {
	return NewParamsWithLimits(32, 32)
}

// NewParamsWithLimits creates a Params store with explicit limits.
func NewParamsWithLimits(maxLength, maxSubParamsLength int) *Params {
	if maxSubParamsLength > maxSubParamsLimit {
		panic("maxSubParamsLength must not be greater than 256")
	}
	return &Params{
		Params:             make([]int32, maxLength),
		maxLength:          maxLength,
		maxSubParamsLength: maxSubParamsLength,
		subParams:          make([]int32, maxSubParamsLength),
		subParamsIdx:       make([]uint16, maxLength),
	}
}

// ParamsFromArray builds Params from a slice where plain values are
// params and nested slices are sub params of the preceding param
// (mirroring Params.fromArray).
func ParamsFromArray(values []interface{}) *Params {
	params := NewParams()
	if len(values) == 0 {
		return params
	}
	start := 0
	if _, isSub := values[0].([]int); isSub {
		start = 1 // skip leading sub params
	}
	for i := start; i < len(values); i++ {
		switch v := values[i].(type) {
		case []int:
			for _, s := range v {
				params.AddSubParam(int32(s)) // #nosec G115 -- the parser clamps VT parameters to 0-9999
			}
		case int:
			params.AddParam(int32(v)) // #nosec G115 -- the parser clamps VT parameters to 0-9999
		}
	}
	return params
}

// Clone returns a deep copy.
func (p *Params) Clone() *Params {
	clone := NewParamsWithLimits(p.maxLength, p.maxSubParamsLength)
	copy(clone.Params, p.Params)
	clone.Length = p.Length
	copy(clone.subParams, p.subParams)
	clone.subParamsLength = p.subParamsLength
	copy(clone.subParamsIdx, p.subParamsIdx)
	clone.rejectDigits = p.rejectDigits
	clone.rejectSubDigits = p.rejectSubDigits
	clone.digitIsSub = p.digitIsSub
	return clone
}

// ToArray returns a representation where plain params are int and sub
// params appear as []int after their param: "1;2:3:4;5" → [1, 2, [3,4], 5].
func (p *Params) ToArray() []interface{} {
	var res []interface{}
	for i := 0; i < p.Length; i++ {
		res = append(res, int(p.Params[i]))
		start := int(p.subParamsIdx[i] >> 8)
		end := int(p.subParamsIdx[i] & 0xFF)
		if end-start > 0 {
			sub := make([]int, end-start)
			for k := start; k < end; k++ {
				sub[k-start] = int(p.subParams[k])
			}
			res = append(res, sub)
		}
	}
	return res
}

// Reset returns the store to its initial empty state.
func (p *Params) Reset() {
	p.Length = 0
	p.subParamsLength = 0
	p.rejectDigits = false
	p.rejectSubDigits = false
	p.digitIsSub = false
}

// AddParam adds a parameter value. Only up to maxLength parameters are
// stored; later ones are ignored.
func (p *Params) AddParam(value int32) {
	p.digitIsSub = false
	if p.Length >= p.maxLength {
		p.rejectDigits = true
		return
	}
	if value < -1 {
		panic("values lesser than -1 are not allowed")
	}
	p.subParamsIdx[p.Length] = uint16(p.subParamsLength)<<8 | uint16(p.subParamsLength) // #nosec G115 -- a sub-parameter count, bounded by the parser's fixed buffer
	if value > maxParamValue {
		value = maxParamValue
	}
	p.Params[p.Length] = value
	p.Length++
}

// AddSubParam adds a sub parameter to the last parameter added.
func (p *Params) AddSubParam(value int32) {
	p.digitIsSub = true
	if p.Length == 0 {
		return
	}
	if p.rejectDigits || p.subParamsLength >= p.maxSubParamsLength {
		p.rejectSubDigits = true
		return
	}
	if value < -1 {
		panic("values lesser than -1 are not allowed")
	}
	if value > maxParamValue {
		value = maxParamValue
	}
	p.subParams[p.subParamsLength] = value
	p.subParamsLength++
	p.subParamsIdx[p.Length-1]++
}

// HasSubParams reports whether the parameter at idx has sub parameters.
func (p *Params) HasSubParams(idx int) bool {
	return int(p.subParamsIdx[idx]&0xFF)-int(p.subParamsIdx[idx]>>8) > 0
}

// GetSubParams returns the borrowed sub parameters of the parameter at
// idx (nil if none).
func (p *Params) GetSubParams(idx int) []int32 {
	start := int(p.subParamsIdx[idx] >> 8)
	end := int(p.subParamsIdx[idx] & 0xFF)
	if end-start > 0 {
		return p.subParams[start:end]
	}
	return nil
}

// GetSubParamsAll returns all sub parameters as an idx → values map
// (values copied).
func (p *Params) GetSubParamsAll() map[int][]int32 {
	result := map[int][]int32{}
	for i := 0; i < p.Length; i++ {
		start := int(p.subParamsIdx[i] >> 8)
		end := int(p.subParamsIdx[i] & 0xFF)
		if end-start > 0 {
			sub := make([]int32, end-start)
			copy(sub, p.subParams[start:end])
			result[i] = sub
		}
	}
	return result
}

// AddDigit adds a single digit to the current (sub) parameter; used by
// the parser to account digits char by char.
func (p *Params) AddDigit(value int32) {
	var length int
	if p.digitIsSub {
		length = p.subParamsLength
	} else {
		length = p.Length
	}
	if p.rejectDigits || length == 0 || (p.digitIsSub && p.rejectSubDigits) {
		return
	}

	store := p.Params
	if p.digitIsSub {
		store = p.subParams
	}
	cur := store[length-1]
	if cur != -1 {
		v := int64(cur)*10 + int64(value)
		if v > maxParamValue {
			v = maxParamValue
		}
		store[length-1] = int32(v)
	} else {
		store[length-1] = value
	}
}
