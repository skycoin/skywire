package safecast

import "math"

const intBits = 32 << (^uint(0) >> 63)

// int8ToInt8 converts the int8 value to int8 safely.
func int8ToInt8(value int8) (result int8, ok bool) {
	return int8(value), true
}

// int8ToInt16 converts the int8 value to int16 safely.
func int8ToInt16(value int8) (result int16, ok bool) {
	return int16(value), true
}

// int8ToInt32 converts the int8 value to int32 safely.
func int8ToInt32(value int8) (result int32, ok bool) {
	return int32(value), true
}

// int8ToInt64 converts the int8 value to int64 safely.
func int8ToInt64(value int8) (result int64, ok bool) {
	return int64(value), true
}

// int8ToUint8 converts the int8 value to uint8 safely.
func int8ToUint8(value int8) (result uint8, ok bool) {
	return uint8(value), value >= 0
}

// int8ToUint16 converts the int8 value to uint16 safely.
func int8ToUint16(value int8) (result uint16, ok bool) {
	return uint16(value), value >= 0
}

// int8ToUint32 converts the int8 value to uint32 safely.
func int8ToUint32(value int8) (result uint32, ok bool) {
	return uint32(value), value >= 0
}

// int8ToUint64 converts the int8 value to uint64 safely.
func int8ToUint64(value int8) (result uint64, ok bool) {
	return uint64(value), value >= 0
}

// int16ToInt8 converts the int16 value to int8 safely.
func int16ToInt8(value int16) (result int8, ok bool) {
	return int8(value), int16(int8(value)) == value
}

// int16ToInt16 converts the int16 value to int16 safely.
func int16ToInt16(value int16) (result int16, ok bool) {
	return int16(value), true
}

// int16ToInt32 converts the int16 value to int32 safely.
func int16ToInt32(value int16) (result int32, ok bool) {
	return int32(value), true
}

// int16ToInt64 converts the int16 value to int64 safely.
func int16ToInt64(value int16) (result int64, ok bool) {
	return int64(value), true
}

// int16ToUint8 converts the int16 value to uint8 safely.
func int16ToUint8(value int16) (result uint8, ok bool) {
	return uint8(value), value >= 0 && int16(uint8(value)) == value
}

// int16ToUint16 converts the int16 value to uint16 safely.
func int16ToUint16(value int16) (result uint16, ok bool) {
	return uint16(value), value >= 0
}

// int16ToUint32 converts the int16 value to uint32 safely.
func int16ToUint32(value int16) (result uint32, ok bool) {
	return uint32(value), value >= 0
}

// int16ToUint64 converts the int16 value to uint64 safely.
func int16ToUint64(value int16) (result uint64, ok bool) {
	return uint64(value), value >= 0
}

// int32ToInt8 converts the int32 value to int8 safely.
func int32ToInt8(value int32) (result int8, ok bool) {
	return int8(value), int32(int8(value)) == value
}

// int32ToInt16 converts the int32 value to int16 safely.
func int32ToInt16(value int32) (result int16, ok bool) {
	return int16(value), int32(int16(value)) == value
}

// int32ToInt32 converts the int32 value to int32 safely.
func int32ToInt32(value int32) (result int32, ok bool) {
	return int32(value), true
}

// int32ToInt64 converts the int32 value to int64 safely.
func int32ToInt64(value int32) (result int64, ok bool) {
	return int64(value), true
}

// int32ToUint8 converts the int32 value to uint8 safely.
func int32ToUint8(value int32) (result uint8, ok bool) {
	return uint8(value), value >= 0 && int32(uint8(value)) == value
}

// int32ToUint16 converts the int32 value to uint16 safely.
func int32ToUint16(value int32) (result uint16, ok bool) {
	return uint16(value), value >= 0 && int32(uint16(value)) == value
}

// int32ToUint32 converts the int32 value to uint32 safely.
func int32ToUint32(value int32) (result uint32, ok bool) {
	return uint32(value), value >= 0
}

// int32ToUint64 converts the int32 value to uint64 safely.
func int32ToUint64(value int32) (result uint64, ok bool) {
	return uint64(value), value >= 0
}

// int64ToInt8 converts the int64 value to int8 safely.
func int64ToInt8(value int64) (result int8, ok bool) {
	return int8(value), int64(int8(value)) == value
}

// int64ToInt16 converts the int64 value to int16 safely.
func int64ToInt16(value int64) (result int16, ok bool) {
	return int16(value), int64(int16(value)) == value
}

// int64ToInt32 converts the int64 value to int32 safely.
func int64ToInt32(value int64) (result int32, ok bool) {
	return int32(value), int64(int32(value)) == value
}

// int64ToInt64 converts the int64 value to int64 safely.
func int64ToInt64(value int64) (result int64, ok bool) {
	return int64(value), true
}

// int64ToUint8 converts the int64 value to uint8 safely.
func int64ToUint8(value int64) (result uint8, ok bool) {
	return uint8(value), value >= 0 && int64(uint8(value)) == value
}

// int64ToUint16 converts the int64 value to uint16 safely.
func int64ToUint16(value int64) (result uint16, ok bool) {
	return uint16(value), value >= 0 && int64(uint16(value)) == value
}

// int64ToUint32 converts the int64 value to uint32 safely.
func int64ToUint32(value int64) (result uint32, ok bool) {
	return uint32(value), value >= 0 && int64(uint32(value)) == value
}

// int64ToUint64 converts the int64 value to uint64 safely.
func int64ToUint64(value int64) (result uint64, ok bool) {
	return uint64(value), value >= 0
}

// uint8ToInt8 converts the uint8 value to int8 safely.
func uint8ToInt8(value uint8) (result int8, ok bool) {
	return int8(value), value <= math.MaxInt8
}

// uint8ToInt16 converts the uint8 value to int16 safely.
func uint8ToInt16(value uint8) (result int16, ok bool) {
	return int16(value), true
}

// uint8ToInt32 converts the uint8 value to int32 safely.
func uint8ToInt32(value uint8) (result int32, ok bool) {
	return int32(value), true
}

// uint8ToInt64 converts the uint8 value to int64 safely.
func uint8ToInt64(value uint8) (result int64, ok bool) {
	return int64(value), true
}

// uint8ToUint8 converts the uint8 value to uint8 safely.
func uint8ToUint8(value uint8) (result uint8, ok bool) {
	return uint8(value), true
}

// uint8ToUint16 converts the uint8 value to uint16 safely.
func uint8ToUint16(value uint8) (result uint16, ok bool) {
	return uint16(value), true
}

// uint8ToUint32 converts the uint8 value to uint32 safely.
func uint8ToUint32(value uint8) (result uint32, ok bool) {
	return uint32(value), true
}

// uint8ToUint64 converts the uint8 value to uint64 safely.
func uint8ToUint64(value uint8) (result uint64, ok bool) {
	return uint64(value), true
}

// uint16ToInt8 converts the uint16 value to int8 safely.
func uint16ToInt8(value uint16) (result int8, ok bool) {
	return int8(value), uint16(int8(value)) == value
}

// uint16ToInt16 converts the uint16 value to int16 safely.
func uint16ToInt16(value uint16) (result int16, ok bool) {
	return int16(value), value <= math.MaxInt16
}

// uint16ToInt32 converts the uint16 value to int32 safely.
func uint16ToInt32(value uint16) (result int32, ok bool) {
	return int32(value), true
}

// uint16ToInt64 converts the uint16 value to int64 safely.
func uint16ToInt64(value uint16) (result int64, ok bool) {
	return int64(value), true
}

// uint16ToUint8 converts the uint16 value to uint8 safely.
func uint16ToUint8(value uint16) (result uint8, ok bool) {
	return uint8(value), uint16(uint8(value)) == value
}

// uint16ToUint16 converts the uint16 value to uint16 safely.
func uint16ToUint16(value uint16) (result uint16, ok bool) {
	return uint16(value), true
}

// uint16ToUint32 converts the uint16 value to uint32 safely.
func uint16ToUint32(value uint16) (result uint32, ok bool) {
	return uint32(value), true
}

// uint16ToUint64 converts the uint16 value to uint64 safely.
func uint16ToUint64(value uint16) (result uint64, ok bool) {
	return uint64(value), true
}

// uint32ToInt8 converts the uint32 value to int8 safely.
func uint32ToInt8(value uint32) (result int8, ok bool) {
	return int8(value), uint32(int8(value)) == value
}

// uint32ToInt16 converts the uint32 value to int16 safely.
func uint32ToInt16(value uint32) (result int16, ok bool) {
	return int16(value), uint32(int16(value)) == value
}

// uint32ToInt32 converts the uint32 value to int32 safely.
func uint32ToInt32(value uint32) (result int32, ok bool) {
	return int32(value), value <= math.MaxInt32
}

// uint32ToInt64 converts the uint32 value to int64 safely.
func uint32ToInt64(value uint32) (result int64, ok bool) {
	return int64(value), true
}

// uint32ToUint8 converts the uint32 value to uint8 safely.
func uint32ToUint8(value uint32) (result uint8, ok bool) {
	return uint8(value), uint32(uint8(value)) == value
}

// uint32ToUint16 converts the uint32 value to uint16 safely.
func uint32ToUint16(value uint32) (result uint16, ok bool) {
	return uint16(value), uint32(uint16(value)) == value
}

// uint32ToUint32 converts the uint32 value to uint32 safely.
func uint32ToUint32(value uint32) (result uint32, ok bool) {
	return uint32(value), true
}

// uint32ToUint64 converts the uint32 value to uint64 safely.
func uint32ToUint64(value uint32) (result uint64, ok bool) {
	return uint64(value), true
}

// uint64ToInt8 converts the uint64 value to int8 safely.
func uint64ToInt8(value uint64) (result int8, ok bool) {
	return int8(value), uint64(int8(value)) == value
}

// uint64ToInt16 converts the uint64 value to int16 safely.
func uint64ToInt16(value uint64) (result int16, ok bool) {
	return int16(value), uint64(int16(value)) == value
}

// uint64ToInt32 converts the uint64 value to int32 safely.
func uint64ToInt32(value uint64) (result int32, ok bool) {
	return int32(value), uint64(int32(value)) == value
}

// uint64ToInt64 converts the uint64 value to int64 safely.
func uint64ToInt64(value uint64) (result int64, ok bool) {
	return int64(value), value <= math.MaxInt64
}

// uint64ToUint8 converts the uint64 value to uint8 safely.
func uint64ToUint8(value uint64) (result uint8, ok bool) {
	return uint8(value), uint64(uint8(value)) == value
}

// uint64ToUint16 converts the uint64 value to uint16 safely.
func uint64ToUint16(value uint64) (result uint16, ok bool) {
	return uint16(value), uint64(uint16(value)) == value
}

// uint64ToUint32 converts the uint64 value to uint32 safely.
func uint64ToUint32(value uint64) (result uint32, ok bool) {
	return uint32(value), uint64(uint32(value)) == value
}

// uint64ToUint64 converts the uint64 value to uint64 safely.
func uint64ToUint64(value uint64) (result uint64, ok bool) {
	return uint64(value), true
}

// int8ToInt converts the int8 value to int safely.
func int8ToInt(value int8) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = int8ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = int8ToInt64(value)
	result = int(r)
	return
}

// int8ToUint converts the int8 value to uint safely.
func int8ToUint(value int8) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = int8ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = int8ToUint64(value)
	result = uint(r)
	return
}

// int16ToInt converts the int16 value to int safely.
func int16ToInt(value int16) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = int16ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = int16ToInt64(value)
	result = int(r)
	return
}

// int16ToUint converts the int16 value to uint safely.
func int16ToUint(value int16) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = int16ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = int16ToUint64(value)
	result = uint(r)
	return
}

// int32ToInt converts the int32 value to int safely.
func int32ToInt(value int32) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = int32ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = int32ToInt64(value)
	result = int(r)
	return
}

// int32ToUint converts the int32 value to uint safely.
func int32ToUint(value int32) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = int32ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = int32ToUint64(value)
	result = uint(r)
	return
}

// int64ToInt converts the int64 value to int safely.
func int64ToInt(value int64) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = int64ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = int64ToInt64(value)
	result = int(r)
	return
}

// int64ToUint converts the int64 value to uint safely.
func int64ToUint(value int64) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = int64ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = int64ToUint64(value)
	result = uint(r)
	return
}

// uint8ToInt converts the uint8 value to int safely.
func uint8ToInt(value uint8) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = uint8ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = uint8ToInt64(value)
	result = int(r)
	return
}

// uint8ToUint converts the uint8 value to uint safely.
func uint8ToUint(value uint8) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = uint8ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = uint8ToUint64(value)
	result = uint(r)
	return
}

// uint16ToInt converts the uint16 value to int safely.
func uint16ToInt(value uint16) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = uint16ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = uint16ToInt64(value)
	result = int(r)
	return
}

// uint16ToUint converts the uint16 value to uint safely.
func uint16ToUint(value uint16) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = uint16ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = uint16ToUint64(value)
	result = uint(r)
	return
}

// uint32ToInt converts the uint32 value to int safely.
func uint32ToInt(value uint32) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = uint32ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = uint32ToInt64(value)
	result = int(r)
	return
}

// uint32ToUint converts the uint32 value to uint safely.
func uint32ToUint(value uint32) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = uint32ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = uint32ToUint64(value)
	result = uint(r)
	return
}

// uint64ToInt converts the uint64 value to int safely.
func uint64ToInt(value uint64) (result int, ok bool) {
	if intBits == 32 {
		var r int32
		r, ok = uint64ToInt32(value)
		result = int(r)
	}
	var r int64
	r, ok = uint64ToInt64(value)
	result = int(r)
	return
}

// uint64ToUint converts the uint64 value to uint safely.
func uint64ToUint(value uint64) (result uint, ok bool) {
	if intBits == 32 {
		var r uint32
		r, ok = uint64ToUint32(value)
		result = uint(r)
	}
	var r uint64
	r, ok = uint64ToUint64(value)
	result = uint(r)
	return
}

// intToInt8 converts the int value to int8 safely.
func intToInt8(value int) (result int8, ok bool) {
	if intBits == 32 {
		return int32ToInt8(int32(value))
	}
	return int64ToInt8(int64(value))
}

// intToInt16 converts the int value to int16 safely.
func intToInt16(value int) (result int16, ok bool) {
	if intBits == 32 {
		return int32ToInt16(int32(value))
	}
	return int64ToInt16(int64(value))
}

// intToInt32 converts the int value to int32 safely.
func intToInt32(value int) (result int32, ok bool) {
	if intBits == 32 {
		return int32ToInt32(int32(value))
	}
	return int64ToInt32(int64(value))
}

// intToInt64 converts the int value to int64 safely.
func intToInt64(value int) (result int64, ok bool) {
	if intBits == 32 {
		return int32ToInt64(int32(value))
	}
	return int64ToInt64(int64(value))
}

// intToUint8 converts the int value to uint8 safely.
func intToUint8(value int) (result uint8, ok bool) {
	if intBits == 32 {
		return int32ToUint8(int32(value))
	}
	return int64ToUint8(int64(value))
}

// intToUint16 converts the int value to uint16 safely.
func intToUint16(value int) (result uint16, ok bool) {
	if intBits == 32 {
		return int32ToUint16(int32(value))
	}
	return int64ToUint16(int64(value))
}

// intToUint32 converts the int value to uint32 safely.
func intToUint32(value int) (result uint32, ok bool) {
	if intBits == 32 {
		return int32ToUint32(int32(value))
	}
	return int64ToUint32(int64(value))
}

// intToUint64 converts the int value to uint64 safely.
func intToUint64(value int) (result uint64, ok bool) {
	if intBits == 32 {
		return int32ToUint64(int32(value))
	}
	return int64ToUint64(int64(value))
}

// uintToInt8 converts the uint value to int8 safely.
func uintToInt8(value uint) (result int8, ok bool) {
	if intBits == 32 {
		return uint32ToInt8(uint32(value))
	}
	return uint64ToInt8(uint64(value))
}

// uintToInt16 converts the uint value to int16 safely.
func uintToInt16(value uint) (result int16, ok bool) {
	if intBits == 32 {
		return uint32ToInt16(uint32(value))
	}
	return uint64ToInt16(uint64(value))
}

// uintToInt32 converts the uint value to int32 safely.
func uintToInt32(value uint) (result int32, ok bool) {
	if intBits == 32 {
		return uint32ToInt32(uint32(value))
	}
	return uint64ToInt32(uint64(value))
}

// uintToInt64 converts the uint value to int64 safely.
func uintToInt64(value uint) (result int64, ok bool) {
	if intBits == 32 {
		return uint32ToInt64(uint32(value))
	}
	return uint64ToInt64(uint64(value))
}

// uintToUint8 converts the uint value to uint8 safely.
func uintToUint8(value uint) (result uint8, ok bool) {
	if intBits == 32 {
		return uint32ToUint8(uint32(value))
	}
	return uint64ToUint8(uint64(value))
}

// uintToUint16 converts the uint value to uint16 safely.
func uintToUint16(value uint) (result uint16, ok bool) {
	if intBits == 32 {
		return uint32ToUint16(uint32(value))
	}
	return uint64ToUint16(uint64(value))
}

// uintToUint32 converts the uint value to uint32 safely.
func uintToUint32(value uint) (result uint32, ok bool) {
	if intBits == 32 {
		return uint32ToUint32(uint32(value))
	}
	return uint64ToUint32(uint64(value))
}

// uintToUint64 converts the uint value to uint64 safely.
func uintToUint64(value uint) (result uint64, ok bool) {
	if intBits == 32 {
		return uint32ToUint64(uint32(value))
	}
	return uint64ToUint64(uint64(value))
}

// intToUint converts the int value to uint safely.
func intToUint(value int) (result uint, ok bool) {
	return uint(value), value >= 0
}

// uintToInt converts the uint value to int safely.
func uintToInt(value uint) (result int, ok bool) {
	return int(value), value <= math.MaxInt
}

// float32ToInt8 converts the float32 value to int8 safely.
func float32ToInt8(value float32) (result int8, ok bool) {
	return int8(value), value >= math.MinInt8 && value <= math.MaxInt8
}

func int8ToFloat32(value int8) (float32, bool) {
	return float32(value), true
}

// float32ToInt16 converts the float32 value to int16 safely.
func float32ToInt16(value float32) (result int16, ok bool) {
	return int16(value), value >= math.MinInt16 && value <= math.MaxInt16
}

func int16ToFloat32(value int16) (float32, bool) {
	return float32(value), true
}

// float32ToInt32 converts the float32 value to int32 safely.
func float32ToInt32(value float32) (result int32, ok bool) {
	return int32(value), value >= math.MinInt32 && value <= math.MaxInt32
}

func int32ToFloat32(value int32) (float32, bool) {
	return float32(value), true
}

// float32ToInt64 converts the float32 value to int64 safely.
func float32ToInt64(value float32) (result int64, ok bool) {
	return int64(value), value >= math.MinInt64 && value <= math.MaxInt64
}

func int64ToFloat32(value int64) (float32, bool) {
	return float32(value), true
}

// float32ToInt converts the float32 value to int safely.
func float32ToInt(value float32) (result int, ok bool) {
	return int(value), value >= math.MinInt && value <= math.MaxInt
}

func intToFloat32(value int) (float32, bool) {
	return float32(value), true
}

// float32ToUint8 converts the float32 value to uint8 safely.
func float32ToUint8(value float32) (result uint8, ok bool) {
	return uint8(value), value >= 0 && value <= math.MaxUint8
}

func uint8ToFloat32(value uint8) (float32, bool) {
	return float32(value), true
}

// float32ToUint16 converts the float32 value to uint16 safely.
func float32ToUint16(value float32) (result uint16, ok bool) {
	return uint16(value), value >= 0 && value <= math.MaxUint16
}

func uint16ToFloat32(value uint16) (float32, bool) {
	return float32(value), true
}

// float32ToUint32 converts the float32 value to uint32 safely.
func float32ToUint32(value float32) (result uint32, ok bool) {
	return uint32(value), value >= 0 && value <= math.MaxUint32
}

func uint32ToFloat32(value uint32) (float32, bool) {
	return float32(value), true
}

// float32ToUint64 converts the float32 value to uint64 safely.
func float32ToUint64(value float32) (result uint64, ok bool) {
	return uint64(value), value >= 0 && value <= math.MaxUint64
}

func uint64ToFloat32(value uint64) (float32, bool) {
	return float32(value), true
}

// float32ToUint converts the float32 value to uint safely.
func float32ToUint(value float32) (result uint, ok bool) {
	return uint(value), value >= 0 && value <= math.MaxUint
}

func uintToFloat32(value uint) (float32, bool) {
	return float32(value), true
}

// float64ToInt8 converts the float64 value to int8 safely.
func float64ToInt8(value float64) (result int8, ok bool) {
	return int8(value), value >= math.MinInt8 && value <= math.MaxInt8
}

func int8ToFloat64(value int8) (float64, bool) {
	return float64(value), true
}

// float64ToInt16 converts the float64 value to int16 safely.
func float64ToInt16(value float64) (result int16, ok bool) {
	return int16(value), value >= math.MinInt16 && value <= math.MaxInt16
}

func int16ToFloat64(value int16) (float64, bool) {
	return float64(value), true
}

// float64ToInt32 converts the float64 value to int32 safely.
func float64ToInt32(value float64) (result int32, ok bool) {
	return int32(value), value >= math.MinInt32 && value <= math.MaxInt32
}

func int32ToFloat64(value int32) (float64, bool) {
	return float64(value), true
}

// float64ToInt64 converts the float64 value to int64 safely.
func float64ToInt64(value float64) (result int64, ok bool) {
	return int64(value), value >= math.MinInt64 && value <= math.MaxInt64
}

func int64ToFloat64(value int64) (float64, bool) {
	return float64(value), true
}

// float64ToInt converts the float64 value to int safely.
func float64ToInt(value float64) (result int, ok bool) {
	return int(value), value >= math.MinInt && value <= math.MaxInt
}

func intToFloat64(value int) (float64, bool) {
	return float64(value), true
}

// float64ToUint8 converts the float64 value to uint8 safely.
func float64ToUint8(value float64) (result uint8, ok bool) {
	return uint8(value), value >= 0 && value <= math.MaxUint8
}

func uint8ToFloat64(value uint8) (float64, bool) {
	return float64(value), true
}

// float64ToUint16 converts the float64 value to uint16 safely.
func float64ToUint16(value float64) (result uint16, ok bool) {
	return uint16(value), value >= 0 && value <= math.MaxUint16
}

func uint16ToFloat64(value uint16) (float64, bool) {
	return float64(value), true
}

// float64ToUint32 converts the float64 value to uint32 safely.
func float64ToUint32(value float64) (result uint32, ok bool) {
	return uint32(value), value >= 0 && value <= math.MaxUint32
}

func uint32ToFloat64(value uint32) (float64, bool) {
	return float64(value), true
}

// float64ToUint64 converts the float64 value to uint64 safely.
func float64ToUint64(value float64) (result uint64, ok bool) {
	return uint64(value), value >= 0 && value <= math.MaxUint64
}

func uint64ToFloat64(value uint64) (float64, bool) {
	return float64(value), true
}

// float64ToUint converts the float64 value to uint safely.
func float64ToUint(value float64) (result uint, ok bool) {
	return uint(value), value >= 0 && value <= math.MaxUint
}

func uintToFloat64(value uint) (float64, bool) {
	return float64(value), true
}

func float32ToFloat64(value float32) (float64, bool) {
	return float64(value), true
}

func float64ToFloat32(value float64) (float32, bool) {
	return float32(value), value >= -math.MaxFloat32 && value <= math.MaxFloat32
}
