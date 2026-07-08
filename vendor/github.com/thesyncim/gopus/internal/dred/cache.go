package dred

import "errors"

var errCacheBufferTooSmall = errors.New("dred: cache buffer too small")

// Cache retains the low-cost parsed DRED metadata plus the cached payload
// length. The payload bytes themselves stay in caller-owned storage.
type Cache struct {
	Len    int
	Parsed Parsed
}

// Clear resets the cached DRED state.
func (c *Cache) Clear() {
	*c = Cache{}
}

// Invalidate drops cached payload visibility without zeroing the retained
// parsed metadata, which keeps packet-entry cache invalidation cheap.
func (c *Cache) Invalidate() {
	if c == nil {
		return
	}
	c.Len = 0
}

// Empty reports whether any DRED payload is cached.
func (c Cache) Empty() bool {
	return c.Len == 0
}

// Store validates, parses, and copies a DRED payload into caller-owned
// storage while retaining the low-cost parsed state.
func (c *Cache) Store(dst, payload []byte, dredFrameOffset int) error {
	if len(payload) > len(dst) {
		return errCacheBufferTooSmall
	}
	parsed, err := ParsePayload(payload, dredFrameOffset)
	if err != nil {
		return err
	}
	copy(dst, payload)
	c.Len = len(payload)
	c.Parsed = parsed
	return nil
}

// Result evaluates the cached payload against an opus_dred_parse()-style
// request. Empty caches yield the zero Result.
func (c Cache) Result(req Request) Result {
	if c.Empty() {
		return Result{}
	}
	return c.Parsed.ForRequest(req)
}
