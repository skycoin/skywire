package atomicbool

// Copied from https://golang.org/src/internal/poll/fd_plan9.go#L14

import (
	"sync/atomic"
)

// Bool is boolean that can be read and written atomically.
type Bool int32

// IsSet returns whether boolean is set.
func (b *Bool) IsSet() bool { return atomic.LoadInt32((*int32)(b)) != 0 }

// SetFalse sets boolean to false.
func (b *Bool) SetFalse() { atomic.StoreInt32((*int32)(b), 0) }

// SetTrue sets boolean to true.
func (b *Bool) SetTrue() { atomic.StoreInt32((*int32)(b), 1) }
