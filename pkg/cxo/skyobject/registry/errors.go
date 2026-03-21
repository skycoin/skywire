package registry

import (
	"errors"
)

// common errors
var (
	ErrInvalidEncodedSchema = errors.New("invalid encoded schema")
	ErrInvalidType          = errors.New("invalid type registering")
	ErrTypeNotFound         = errors.New("type not found")
	ErrEmptySkyobjectTag    = errors.New(
		`empty skyobject tag, expected "schema=XXX"`)

	ErrInvalidSchemaOrData     = errors.New("invalid schema or data")
	ErrNoSuchField             = errors.New("no such field")
	ErrInvalidDynamicReference = errors.New("invalid dynamic reference")
	ErrInvalidSchema           = errors.New("invalid schema")
	ErrReferenceRepresentsNil  = errors.New(
		"reference represents nil")

	ErrInvalidEncodedRefs = errors.New("invalid encoded Refs")
	ErrInvalidRefs        = errors.New("invalid Refs")
	ErrIndexOutOfRange    = errors.New("index out of range")
	ErrRefsIsIterating    = errors.New("Refs is iterating")
	ErrRefsElementIsNil   = errors.New("Refs element is nil")
	ErrInvalidSliceIndex  = errors.New("invalid slice index")
	ErrInvalidRefsState   = errors.New("invalid state of the Refs")
	ErrRefsIterating      = errors.New("Refs is iterating")
	ErrInvalidDegree      = errors.New("invalid degree")

	ErrNotFound        = errors.New("not found")
	ErrStopIteration   = errors.New("stop iteration")
	ErrMissingRegistry = errors.New("missing registry")
)
