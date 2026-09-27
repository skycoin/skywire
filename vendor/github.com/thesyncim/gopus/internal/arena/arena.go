// Package arena provides a generic single-type bump allocator used to back many
// logical scratch buffers with one contiguous allocation.
//
// Codec scratch is a set of fixed-purpose buffers that are sized once (to their
// worst case) and then reused in place every frame. Backing each with its own
// make() scatters them across the heap (poor cache locality) and inflates the
// live heap object count (more GC bookkeeping, slower construction). A Bump owns
// one backing slice and hands out non-overlapping sub-slices for those buffers,
// so a whole family of same-typed scratch fields costs a single allocation and
// sits in one contiguous region.
//
// Bump performs no synchronization; a Bump (and the slices it hands out) belongs
// to a single codec instance, exactly like the scratch fields it replaces.
package arena

// Bump is a bump allocator over one contiguous backing slice of T.
//
// Typical use: Ensure the total once (when sizes are known or grow), then Alloc
// each logical buffer in a fixed order. The carved sub-slices alias the backing
// and stay valid until the next Ensure.
type Bump[T any] struct {
	buf []T
	off int
}

// Ensure makes the backing able to hold total elements and rewinds the carve
// offset to the start. It allocates only when the current capacity is too small;
// when the backing is reused (the common steady-state path) it produces no
// garbage. Call Ensure once before a run of Alloc calls.
func (b *Bump[T]) Ensure(total int) {
	total = max(total, 0)
	if cap(b.buf) < total {
		b.buf = make([]T, total)
	} else {
		b.buf = b.buf[:total]
	}
	b.off = 0
}

// Alloc carves the next n elements and returns them as buf[off:off:off+n] — a
// slice with length 0 and capacity n.
//
// Length 0 means the returned slice behaves like a freshly-grown buffer: the
// caller's existing ensure-slice helper still controls the visible length and
// whether it is zero-filled, so clear-vs-no-clear semantics are unchanged.
// Capacity n (pinned via the three-index slice) confines any clear(s[:cap(s)])
// or reslice s[:k<=n] to this slot, so it can never reach into the next buffer.
//
// Alloc panics if the carves overrun the Ensure'd total, which surfaces an
// under-sized Ensure immediately rather than corrupting an adjacent slot.
func (b *Bump[T]) Alloc(n int) []T {
	n = max(n, 0)
	s := b.buf[b.off : b.off : b.off+n]
	b.off += n
	return s
}

// AllocN carves the next n elements and returns them as buf[off:off+n:off+n] — a
// slice with length n and capacity n. Use this for buffers that are used at their
// full carved length without going through a resize/ensure helper. The elements
// retain whatever the backing held (zero on a fresh backing), matching make().
// Like Alloc, it panics if the carves overrun the Ensure'd total.
func (b *Bump[T]) AllocN(n int) []T {
	n = max(n, 0)
	s := b.buf[b.off : b.off+n : b.off+n]
	b.off += n
	return s
}

// Tail returns the uncarved remainder of the backing (buf[off:]). It is for
// callers that write a variable number of elements into the remainder and then
// commit exactly how many they wrote with AllocN(n) — which returns that same
// just-written region and advances the offset past it.
func (b *Bump[T]) Tail() []T { return b.buf[b.off:] }

// Used reports how many elements have been carved since the last Ensure.
func (b *Bump[T]) Used() int { return b.off }

// Cap reports the backing capacity in elements.
func (b *Bump[T]) Cap() int { return cap(b.buf) }
