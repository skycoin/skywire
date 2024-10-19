package safecast

type numericType interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// To converts a numeric value from the FromType to the specified ToType type safely.
// result will always be same as the usual type cast (type(value)),
// but ok is false when overflow or underflow occured.
func To[ToType numericType, FromType numericType](value FromType) (result ToType, ok bool) {
	ok = true
	switch t := any(result).(type) {
	case int8:
		t, ok = ToInt8(value)
		result = ToType(t)
	case int16:
		t, ok = ToInt16(value)
		result = ToType(t)
	case int32:
		t, ok = ToInt32(value)
		result = ToType(t)
	case int64:
		t, ok = ToInt64(value)
		result = ToType(t)
	case int:
		t, ok = ToInt(value)
		result = ToType(t)
	case uint8:
		t, ok = ToUint8(value)
		result = ToType(t)
	case uint16:
		t, ok = ToUint16(value)
		result = ToType(t)
	case uint32:
		t, ok = ToUint32(value)
		result = ToType(t)
	case uint64:
		t, ok = ToUint64(value)
		result = ToType(t)
	case uint:
		t, ok = ToUint(value)
		result = ToType(t)
	case float32:
		t, ok = ToFloat32(value)
		result = ToType(t)
	case float64:
		t, ok = ToFloat64(value)
		result = ToType(t)
	}
	return result, ok
}

// ToInt8 converts value to int8 type safely.
// result will always be same as the usual type cast(int8(value)),
// but ok is false when overflow or underflow occured.
func ToInt8[FromType numericType](value FromType) (int8, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8(value), true
	case int16:
		return int16ToInt8(int16(value))
	case int32:
		return int32ToInt8(int32(value))
	case int64:
		return int64ToInt8(int64(value))
	case int:
		return intToInt8(int(value))
	case uint8:
		return uint8ToInt8(uint8(value))
	case uint16:
		return uint16ToInt8(uint16(value))
	case uint32:
		return uint32ToInt8(uint32(value))
	case uint64:
		return uint64ToInt8(uint64(value))
	case uint:
		return uintToInt8(uint(value))
	case float32:
		return float32ToInt8(float32(value))
	case float64:
		return float64ToInt8(float64(value))
	}
	return int8(value), false
}

// ToInt16 converts value to int16 type safely.
// result will always be same as the usual type cast(int16(value)),
// but ok is false when overflow or underflow occured.
func ToInt16[FromType numericType](value FromType) (int16, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToInt16(int8(value))
	case int16:
		return int16(value), true
	case int32:
		return int32ToInt16(int32(value))
	case int64:
		return int64ToInt16(int64(value))
	case int:
		return intToInt16(int(value))
	case uint8:
		return uint8ToInt16(uint8(value))
	case uint16:
		return uint16ToInt16(uint16(value))
	case uint32:
		return uint32ToInt16(uint32(value))
	case uint64:
		return uint64ToInt16(uint64(value))
	case uint:
		return uintToInt16(uint(value))
	case float32:
		return float32ToInt16(float32(value))
	case float64:
		return float64ToInt16(float64(value))
	}
	return int16(value), false
}

// ToInt32 converts value to int32 type safely.
// result will always be same as the usual type cast(int32(value)),
// but ok is false when overflow or underflow occured.
func ToInt32[FromType numericType](value FromType) (int32, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToInt32(int8(value))
	case int16:
		return int16ToInt32(int16(value))
	case int32:
		return int32(value), true
	case int64:
		return int64ToInt32(int64(value))
	case int:
		return intToInt32(int(value))
	case uint8:
		return uint8ToInt32(uint8(value))
	case uint16:
		return uint16ToInt32(uint16(value))
	case uint32:
		return uint32ToInt32(uint32(value))
	case uint64:
		return uint64ToInt32(uint64(value))
	case uint:
		return uintToInt32(uint(value))
	case float32:
		return float32ToInt32(float32(value))
	case float64:
		return float64ToInt32(float64(value))
	}
	return int32(value), false
}

// ToInt64 converts value to int64 type safely.
// result will always be same as the usual type cast(int64(value)),
// but ok is false when overflow or underflow occured.
func ToInt64[FromType numericType](value FromType) (int64, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToInt64(int8(value))
	case int16:
		return int16ToInt64(int16(value))
	case int32:
		return int32ToInt64(int32(value))
	case int64:
		return int64(value), true
	case int:
		return intToInt64(int(value))
	case uint8:
		return uint8ToInt64(uint8(value))
	case uint16:
		return uint16ToInt64(uint16(value))
	case uint32:
		return uint32ToInt64(uint32(value))
	case uint64:
		return uint64ToInt64(uint64(value))
	case uint:
		return uintToInt64(uint(value))
	case float32:
		return float32ToInt64(float32(value))
	case float64:
		return float64ToInt64(float64(value))
	}
	return int64(value), false
}

// ToInt converts value to int type safely.
// result will always be same as the usual type cast(int(value)),
// but ok is false when overflow or underflow occured.
func ToInt[FromType numericType](value FromType) (int, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToInt(int8(value))
	case int16:
		return int16ToInt(int16(value))
	case int32:
		return int32ToInt(int32(value))
	case int64:
		return int64ToInt(int64(value))
	case int:
		return int(value), true
	case uint8:
		return uint8ToInt(uint8(value))
	case uint16:
		return uint16ToInt(uint16(value))
	case uint32:
		return uint32ToInt(uint32(value))
	case uint64:
		return uint64ToInt(uint64(value))
	case uint:
		return uintToInt(uint(value))
	case float32:
		return float32ToInt(float32(value))
	case float64:
		return float64ToInt(float64(value))
	}
	return int(value), false
}

// ToUint8 converts value to uint8 type safely.
// result will always be same as the usual type cast(uint8(value)),
// but ok is false when overflow or underflow occured.
func ToUint8[FromType numericType](value FromType) (uint8, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToUint8(int8(value))
	case int16:
		return int16ToUint8(int16(value))
	case int32:
		return int32ToUint8(int32(value))
	case int64:
		return int64ToUint8(int64(value))
	case int:
		return intToUint8(int(value))
	case uint8:
		return uint8(value), true
	case uint16:
		return uint16ToUint8(uint16(value))
	case uint32:
		return uint32ToUint8(uint32(value))
	case uint64:
		return uint64ToUint8(uint64(value))
	case uint:
		return uintToUint8(uint(value))
	case float32:
		return float32ToUint8(float32(value))
	case float64:
		return float64ToUint8(float64(value))
	}
	return uint8(value), false
}

// ToUint16 converts value to uint16 type safely.
// result will always be same as the usual type cast(uint16(value)),
// but ok is false when overflow or underflow occured.
func ToUint16[FromType numericType](value FromType) (uint16, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToUint16(int8(value))
	case int16:
		return int16ToUint16(int16(value))
	case int32:
		return int32ToUint16(int32(value))
	case int64:
		return int64ToUint16(int64(value))
	case int:
		return intToUint16(int(value))
	case uint8:
		return uint8ToUint16(uint8(value))
	case uint16:
		return uint16(value), true
	case uint32:
		return uint32ToUint16(uint32(value))
	case uint64:
		return uint64ToUint16(uint64(value))
	case uint:
		return uintToUint16(uint(value))
	case float32:
		return float32ToUint16(float32(value))
	case float64:
		return float64ToUint16(float64(value))
	}
	return uint16(value), false
}

// ToUint32 converts value to uint32 type safely.
// result will always be same as the usual type cast(uint32(value)),
// but ok is false when overflow or underflow occured.
func ToUint32[FromType numericType](value FromType) (uint32, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToUint32(int8(value))
	case int16:
		return int16ToUint32(int16(value))
	case int32:
		return int32ToUint32(int32(value))
	case int64:
		return int64ToUint32(int64(value))
	case int:
		return intToUint32(int(value))
	case uint8:
		return uint8ToUint32(uint8(value))
	case uint16:
		return uint16ToUint32(uint16(value))
	case uint32:
		return uint32(value), true
	case uint64:
		return uint64ToUint32(uint64(value))
	case uint:
		return uintToUint32(uint(value))
	case float32:
		return float32ToUint32(float32(value))
	case float64:
		return float64ToUint32(float64(value))
	}
	return uint32(value), false
}

// ToUint64 converts value to uint64 type safely.
// result will always be same as the usual type cast(uint64(value)),
// but ok is false when overflow or underflow occured.
func ToUint64[FromType numericType](value FromType) (uint64, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToUint64(int8(value))
	case int16:
		return int16ToUint64(int16(value))
	case int32:
		return int32ToUint64(int32(value))
	case int64:
		return int64ToUint64(int64(value))
	case int:
		return intToUint64(int(value))
	case uint8:
		return uint8ToUint64(uint8(value))
	case uint16:
		return uint16ToUint64(uint16(value))
	case uint32:
		return uint32ToUint64(uint32(value))
	case uint64:
		return uint64(value), true
	case uint:
		return uintToUint64(uint(value))
	case float32:
		return float32ToUint64(float32(value))
	case float64:
		return float64ToUint64(float64(value))
	}
	return uint64(value), false
}

// ToUint converts value to uint type safely.
// result will always be same as the usual type cast(uint(value)),
// but ok is false when overflow or underflow occured.
func ToUint[FromType numericType](value FromType) (uint, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToUint(int8(value))
	case int16:
		return int16ToUint(int16(value))
	case int32:
		return int32ToUint(int32(value))
	case int64:
		return int64ToUint(int64(value))
	case int:
		return intToUint(int(value))
	case uint8:
		return uint8ToUint(uint8(value))
	case uint16:
		return uint16ToUint(uint16(value))
	case uint32:
		return uint32ToUint(uint32(value))
	case uint64:
		return uint64ToUint(uint64(value))
	case uint:
		return uint(value), true
	case float32:
		return float32ToUint(float32(value))
	case float64:
		return float64ToUint(float64(value))
	}
	return uint(value), false
}

// ToFloat32 converts value to float32 type safely.
// result will always be same as the usual type cast(float32(value)),
// but ok is false when overflow or underflow occured.
func ToFloat32[FromType numericType](value FromType) (float32, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToFloat32(int8(value))
	case int16:
		return int16ToFloat32(int16(value))
	case int32:
		return int32ToFloat32(int32(value))
	case int64:
		return int64ToFloat32(int64(value))
	case int:
		return intToFloat32(int(value))
	case uint8:
		return uint8ToFloat32(uint8(value))
	case uint16:
		return uint16ToFloat32(uint16(value))
	case uint32:
		return uint32ToFloat32(uint32(value))
	case uint64:
		return uint64ToFloat32(uint64(value))
	case uint:
		return uintToFloat32(uint(value))
	case float32:
		return float32(value), true
	case float64:
		return float64ToFloat32(float64(value))
	}
	return float32(value), false
}

// ToFloat64 converts value to float64 type safely.
// result will always be same as the usual type cast(float64(value)),
// but ok is false when overflow or underflow occured.
func ToFloat64[FromType numericType](value FromType) (float64, bool) {
	var zero FromType // Use zero to any for type switch to avoid malloc
	switch any(zero).(type) {
	case int8:
		return int8ToFloat64(int8(value))
	case int16:
		return int16ToFloat64(int16(value))
	case int32:
		return int32ToFloat64(int32(value))
	case int64:
		return int64ToFloat64(int64(value))
	case int:
		return intToFloat64(int(value))
	case uint8:
		return uint8ToFloat64(uint8(value))
	case uint16:
		return uint16ToFloat64(uint16(value))
	case uint32:
		return uint32ToFloat64(uint32(value))
	case uint64:
		return uint64ToFloat64(uint64(value))
	case uint:
		return uintToFloat64(uint(value))
	case float32:
		return float32ToFloat64(float32(value))
	case float64:
		return float64(value), true
	}
	return float64(value), false
}
