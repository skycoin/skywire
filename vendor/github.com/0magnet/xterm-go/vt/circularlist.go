package vt

// CircularList is a list with a maximum size that wraps around,
// overriding values at the start when full (port of CircularList).
// Event emitters are replaced by optional callback fields.
type CircularList[T any] struct {
	array      []T
	startIndex int
	length     int
	maxLength  int

	// OnTrim is called with the number of trimmed items.
	OnTrim func(amount int)
	// OnInsert is called after an insertion at index with amount items.
	OnInsert func(index, amount int)
	// OnDelete is called after a deletion at index of amount items.
	OnDelete func(index, amount int)
}

// NewCircularList creates a circular list with the given max length.
func NewCircularList[T any](maxLength int) *CircularList[T] {
	return &CircularList[T]{
		array:     make([]T, maxLength),
		maxLength: maxLength,
	}
}

// MaxLength returns the maximum length.
func (c *CircularList[T]) MaxLength() int { return c.maxLength }

// SetMaxLength resizes the maximum length, compacting the list.
func (c *CircularList[T]) SetMaxLength(newMaxLength int) {
	if c.maxLength == newMaxLength {
		return
	}
	newArray := make([]T, newMaxLength)
	for i := 0; i < min(newMaxLength, c.length); i++ {
		newArray[i] = c.array[c.cyclicIndex(i)]
	}
	c.array = newArray
	c.maxLength = newMaxLength
	c.startIndex = 0
	if c.length > newMaxLength {
		c.length = newMaxLength
	}
}

// Length returns the current length.
func (c *CircularList[T]) Length() int { return c.length }

// SetLength sets the length, zero-filling on growth.
func (c *CircularList[T]) SetLength(newLength int) {
	if newLength > c.length {
		var zero T
		for i := c.length; i < newLength; i++ {
			c.array[i] = zero
		}
	}
	c.length = newLength
}

// Get returns the value at index (no bounds checking, cyclic).
func (c *CircularList[T]) Get(index int) T {
	return c.array[c.cyclicIndex(index)]
}

// Set sets the value at index (no bounds checking, cyclic).
func (c *CircularList[T]) Set(index int, value T) {
	c.array[c.cyclicIndex(index)] = value
}

// Push appends a value, overriding index 0 when full.
func (c *CircularList[T]) Push(value T) {
	c.array[c.cyclicIndex(c.length)] = value
	if c.length == c.maxLength {
		c.startIndex = (c.startIndex + 1) % c.maxLength
		if c.OnTrim != nil {
			c.OnTrim(1)
		}
	} else {
		c.length++
	}
}

// Recycle advances the ring buffer and returns the element for reuse.
// The buffer must be full.
func (c *CircularList[T]) Recycle() T {
	if c.length != c.maxLength {
		panic("Can only recycle when the buffer is full")
	}
	c.startIndex = (c.startIndex + 1) % c.maxLength
	if c.OnTrim != nil {
		c.OnTrim(1)
	}
	return c.array[c.cyclicIndex(c.length-1)]
}

// IsFull reports whether the ring buffer is at max length.
func (c *CircularList[T]) IsFull() bool { return c.length == c.maxLength }

// Pop removes and returns the last value.
func (c *CircularList[T]) Pop() T {
	value := c.array[c.cyclicIndex(c.length-1)]
	c.length--
	return value
}

// Splice deletes deleteCount items at start, then inserts items there.
func (c *CircularList[T]) Splice(start, deleteCount int, items ...T) {
	if deleteCount > 0 {
		for i := start; i < c.length-deleteCount; i++ {
			c.array[c.cyclicIndex(i)] = c.array[c.cyclicIndex(i+deleteCount)]
		}
		c.length -= deleteCount
		if c.OnDelete != nil {
			c.OnDelete(start, deleteCount)
		}
	}

	for i := c.length - 1; i >= start; i-- {
		c.array[c.cyclicIndex(i+len(items))] = c.array[c.cyclicIndex(i)]
	}
	for i := 0; i < len(items); i++ {
		c.array[c.cyclicIndex(start+i)] = items[i]
	}
	if len(items) > 0 && c.OnInsert != nil {
		c.OnInsert(start, len(items))
	}

	if c.length+len(items) > c.maxLength {
		countToTrim := c.length + len(items) - c.maxLength
		c.startIndex += countToTrim
		c.length = c.maxLength
		if c.OnTrim != nil {
			c.OnTrim(countToTrim)
		}
	} else {
		c.length += len(items)
	}
}

// TrimStart removes count items from the start.
func (c *CircularList[T]) TrimStart(count int) {
	if count > c.length {
		count = c.length
	}
	c.startIndex += count
	c.length -= count
	if c.OnTrim != nil {
		c.OnTrim(count)
	}
}

// ShiftElements moves count elements at start by offset.
func (c *CircularList[T]) ShiftElements(start, count, offset int) {
	if count <= 0 {
		return
	}
	if start < 0 || start >= c.length {
		panic("start argument out of range")
	}
	if start+offset < 0 {
		panic("Cannot shift elements in list beyond index 0")
	}

	if offset > 0 {
		for i := count - 1; i >= 0; i-- {
			c.Set(start+i+offset, c.Get(start+i))
		}
		expandListBy := start + count + offset - c.length
		if expandListBy > 0 {
			c.length += expandListBy
			for c.length > c.maxLength {
				c.length--
				c.startIndex++
				if c.OnTrim != nil {
					c.OnTrim(1)
				}
			}
		}
	} else {
		for i := 0; i < count; i++ {
			c.Set(start+i+offset, c.Get(start+i))
		}
	}
}

func (c *CircularList[T]) cyclicIndex(index int) int {
	return (c.startIndex + index) % c.maxLength
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
