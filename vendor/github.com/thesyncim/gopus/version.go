package gopus

// gopusVersion is the gopus library version string.
const gopusVersion = "gopus 0.1.0"

// VersionString returns a human-readable version string for the gopus library.
//
// The returned string identifies gopus, not libopus. It is analogous to
// opus_get_version_string() from celt/celt.c in libopus 1.6.1, which returns
// "libopus <version>". Applications that need to detect libopus at runtime
// should not use this function; it will not contain the substring "libopus".
func VersionString() string {
	return gopusVersion
}

// ErrorString returns a human-readable string for an Opus error code.
//
// It mirrors opus_strerror() from celt/celt.c in libopus 1.6.1. The mapping
// for codes 0 through -7 is identical to libopus. Codes outside [-7, 0]
// return "unknown error".
//
// Standard Opus error codes:
//
//	 0  success
//	-1  invalid argument
//	-2  buffer too small
//	-3  internal error
//	-4  corrupted stream
//	-5  request not implemented
//	-6  invalid state
//	-7  memory allocation failed
func ErrorString(code int) string {
	// Mirrors:
	//   celt/celt.c opus_strerror(), libopus 1.6.1
	//   static const char * const error_strings[8] = { ... }
	//   if (error > 0 || error < -7) return "unknown error";
	//   else return error_strings[-error];
	switch code {
	case 0:
		return "success"
	case -1:
		return "invalid argument"
	case -2:
		return "buffer too small"
	case -3:
		return "internal error"
	case -4:
		return "corrupted stream"
	case -5:
		return "request not implemented"
	case -6:
		return "invalid state"
	case -7:
		return "memory allocation failed"
	default:
		return "unknown error"
	}
}
