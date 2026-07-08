package dnnblob

import (
	"encoding/binary"
	"math"
)

// Float32View is a zero-copy typed view over a validated float32 record payload.
type Float32View struct {
	data []byte
}

// Len reports the number of float32 values in the view.
func (v Float32View) Len() int {
	return len(v.data) / 4
}

// Empty reports whether the view is empty.
func (v Float32View) Empty() bool {
	return len(v.data) == 0
}

// At returns the i-th float32 value.
func (v Float32View) At(i int) float32 {
	offset := 4 * i
	return math.Float32frombits(binary.LittleEndian.Uint32(v.data[offset : offset+4]))
}

// Fill copies the view into dst and returns the number of values written.
func (v Float32View) Fill(dst []float32) int {
	n := min(v.Len(), len(dst))
	for i := 0; i < n; i++ {
		dst[i] = v.At(i)
	}
	return n
}

// Int32View is a zero-copy typed view over a validated int32 record payload.
type Int32View struct {
	data []byte
}

// Len reports the number of int32 values in the view.
func (v Int32View) Len() int {
	return len(v.data) / 4
}

// Empty reports whether the view is empty.
func (v Int32View) Empty() bool {
	return len(v.data) == 0
}

// At returns the i-th int32 value.
func (v Int32View) At(i int) int32 {
	offset := 4 * i
	return int32(binary.LittleEndian.Uint32(v.data[offset : offset+4]))
}

// Fill copies the view into dst and returns the number of values written.
func (v Int32View) Fill(dst []int32) int {
	n := min(v.Len(), len(dst))
	for i := 0; i < n; i++ {
		dst[i] = v.At(i)
	}
	return n
}

// Int8View is a zero-copy typed view over a validated int8 record payload.
type Int8View struct {
	data []byte
}

// Len reports the number of int8 values in the view.
func (v Int8View) Len() int {
	return len(v.data)
}

// Empty reports whether the view is empty.
func (v Int8View) Empty() bool {
	return len(v.data) == 0
}

// At returns the i-th int8 value.
func (v Int8View) At(i int) int8 {
	return int8(v.data[i])
}

// Fill copies the view into dst and returns the number of values written.
func (v Int8View) Fill(dst []int8) int {
	n := min(len(v.data), len(dst))
	for i := 0; i < n; i++ {
		dst[i] = int8(v.data[i])
	}
	return n
}

// Float32View returns a zero-copy float32 view over the record payload.
func (r Record) Float32View() (Float32View, error) {
	if r.Type != TypeFloat {
		return Float32View{}, errInvalidBlob
	}
	return Float32ViewFromBytes(r.Data, r.Size)
}

// Float32ViewFromBytes returns a zero-copy float32 view over raw record bytes,
// validating only the byte size and alignment. This matches libopus's
// size-driven layer binding behavior.
func Float32ViewFromBytes(data []byte, size int32) (Float32View, error) {
	if size < 0 || len(data) != int(size) || len(data)%4 != 0 {
		return Float32View{}, errInvalidBlob
	}
	return Float32View{data: data}, nil
}

// Int32View returns a zero-copy int32 view over the record payload.
func (r Record) Int32View() (Int32View, error) {
	if r.Type != TypeInt {
		return Int32View{}, errInvalidBlob
	}
	return Int32ViewFromBytes(r.Data, r.Size)
}

// Int32ViewFromBytes returns a zero-copy int32 view over raw record bytes,
// validating only the byte size and alignment.
func Int32ViewFromBytes(data []byte, size int32) (Int32View, error) {
	if size < 0 || len(data) != int(size) || len(data)%4 != 0 {
		return Int32View{}, errInvalidBlob
	}
	return Int32View{data: data}, nil
}

// Int8View returns a zero-copy int8 view over the record payload.
func (r Record) Int8View() (Int8View, error) {
	if r.Type != TypeInt8 {
		return Int8View{}, errInvalidBlob
	}
	return Int8ViewFromBytes(r.Data, r.Size)
}

// Int8ViewFromBytes returns a zero-copy int8 view over raw record bytes,
// validating only the byte size.
func Int8ViewFromBytes(data []byte, size int32) (Int8View, error) {
	if size < 0 || len(data) != int(size) {
		return Int8View{}, errInvalidBlob
	}
	return Int8View{data: data}, nil
}
